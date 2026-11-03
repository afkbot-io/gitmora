package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"

	"workgraph.local/platform/internal/identifier"
	"workgraph.local/platform/internal/retrieval/application"
	"workgraph.local/platform/internal/retrieval/domain"
)

// ClaimEmbeddings lazily backfills bounded batches, then leases independently of source indexing.
func (s *RetrievalStore) ClaimEmbeddings(ctx context.Context, profile string) ([]application.EmbeddingLease, error) {
	if !domain.Digest(profile) {
		return nil, domain.ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	// Current dialogue parts are useful first; archived revisions follow without rewriting captures.
	_, err = tx.Exec(ctx, `INSERT INTO retrieval_embeddings(profile,chunk_id)
 SELECT $1,d.id FROM retrieval_chunks d JOIN retrieval_jobs j ON j.id=d.job_id
 LEFT JOIN capture_sessions cs ON (cs.repository_id,cs.session_id)=(j.repository_id,j.session_id)
 WHERE j.completed_at IS NOT NULL
 AND NOT EXISTS(SELECT 1 FROM retrieval_embeddings ready WHERE ready.profile=$1 AND ready.completed_at IS NULL AND ready.available_at<=now() AND (ready.lease_until IS NULL OR ready.lease_until<=now()) OFFSET 7 LIMIT 1)
 AND NOT EXISTS(SELECT 1 FROM retrieval_embeddings e WHERE e.profile=$1 AND e.chunk_id=d.id)
 ORDER BY (j.source_kind='capture' AND j.revision=cs.latest_revision) DESC NULLS LAST,d.id LIMIT 32
 ON CONFLICT DO NOTHING`, profile)
	if err != nil {
		return nil, err
	}
	// Repeated immutable revisions often contain identical message chunks. Reuse
	// only exact content under the same project/model profile; citations and producer
	// metadata stay attached to each original chunk, never to the reused vector.
	_, err = tx.Exec(ctx, `WITH due AS MATERIALIZED (
 SELECT e.chunk_id,d.content_digest,j.project_id FROM retrieval_embeddings e JOIN retrieval_chunks d ON d.id=e.chunk_id JOIN retrieval_jobs j ON j.id=d.job_id
 WHERE e.profile=$1 AND e.completed_at IS NULL AND (e.lease_until IS NULL OR e.lease_until<=now()) ORDER BY e.available_at,e.chunk_id LIMIT 32 FOR UPDATE OF e SKIP LOCKED
 ), reused AS (
 SELECT due.chunk_id,known.embedding FROM due CROSS JOIN LATERAL (
 SELECT old.embedding FROM retrieval_chunks same JOIN retrieval_jobs source ON source.id=same.job_id
 JOIN retrieval_embeddings old ON old.chunk_id=same.id AND old.profile=$1 AND old.completed_at IS NOT NULL
 WHERE same.content_digest=due.content_digest AND source.project_id=due.project_id LIMIT 1
 ) known
 ) UPDATE retrieval_embeddings e SET embedding=reused.embedding,completed_at=clock_timestamp(),last_error_code=NULL,attempts=0,lease_owner=NULL,lease_until=NULL FROM reused WHERE e.profile=$1 AND e.chunk_id=reused.chunk_id`, profile)
	if err != nil {
		return nil, err
	}
	owner, err := identifier.New()
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `WITH due AS (SELECT chunk_id FROM retrieval_embeddings WHERE profile=$1 AND completed_at IS NULL AND available_at<=now() AND (lease_until IS NULL OR lease_until<=now()) ORDER BY available_at,chunk_id LIMIT 2 FOR UPDATE SKIP LOCKED)
 UPDATE retrieval_embeddings e SET lease_owner=$2,lease_until=now()+interval '60 seconds' FROM due,retrieval_chunks d WHERE e.profile=$1 AND e.chunk_id=due.chunk_id AND d.id=e.chunk_id RETURNING e.chunk_id,d.content`, profile, owner)
	if err != nil {
		return nil, err
	}
	batch := []application.EmbeddingLease{}
	for rows.Next() {
		b := application.EmbeddingLease{Owner: owner}
		if err = rows.Scan(&b.ID, &b.Content); err != nil {
			rows.Close()
			return nil, err
		}
		batch = append(batch, b)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return batch, tx.Commit(ctx)
}

// FinishEmbeddings rejects stale writers. Failures retain a durable retry and never alter lexical evidence.
func (s *RetrievalStore) FinishEmbeddings(ctx context.Context, profile string, batch []application.EmbeddingLease, vectors [][]float32, code string) error {
	if !domain.Digest(profile) || len(batch) < 1 || len(batch) > 8 || code != "" && code != "embedding_unavailable" || code == "" && len(vectors) != len(batch) {
		return domain.ErrInvalid
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for i, b := range batch {
		if !domain.Digest(b.ID) || !identifier.Valid(b.Owner) {
			return domain.ErrInvalid
		}
		var tag pgconn.CommandTag
		if code != "" {
			tag, err = tx.Exec(ctx, `UPDATE retrieval_embeddings SET attempts=attempts+1,last_error_code=$4,lease_owner=NULL,lease_until=NULL,available_at=now()+LEAST(900,power(2,LEAST(attempts,8))*5)*interval '1 second' WHERE profile=$1 AND chunk_id=$2 AND lease_owner=$3 AND lease_until>now() AND completed_at IS NULL`, profile, b.ID, b.Owner, code)
		} else {
			vector, e := domain.NormalizeVector(vectors[i], len(vectors[i]))
			if e != nil {
				return e
			}
			raw, _ := json.Marshal(vector)
			tag, err = tx.Exec(ctx, `UPDATE retrieval_embeddings SET embedding=$4::public.vector,completed_at=now(),attempts=0,last_error_code=NULL,lease_owner=NULL,lease_until=NULL WHERE profile=$1 AND chunk_id=$2 AND lease_owner=$3 AND lease_until>now() AND completed_at IS NULL`, profile, b.ID, b.Owner, string(raw))
		}
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return domain.ErrLease
		}
	}
	return tx.Commit(ctx)
}

// VerifyEmbeddingProfile fails configuration early rather than mixing incompatible vector dimensions.
func (s *RetrievalStore) VerifyEmbeddingProfile(ctx context.Context, profile string, dimensions int) error {
	var incompatible bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM retrieval_embeddings WHERE profile=$1 AND embedding IS NOT NULL AND public.vector_dims(embedding)<>$2)`, profile, dimensions).Scan(&incompatible)
	if err == nil && incompatible {
		return errors.New("embedding profile dimensions changed")
	}
	return err
}
