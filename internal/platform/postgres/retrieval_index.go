package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	capture "workgraph.local/platform/internal/capture/domain"
	forge "workgraph.local/platform/internal/forge/domain"
	"workgraph.local/platform/internal/identifier"
	"workgraph.local/platform/internal/retrieval/application"
	"workgraph.local/platform/internal/retrieval/domain"
)

// RetrievalStore owns rebuildable source projections and their independently leased indexing work.
type RetrievalStore struct{ pool *pgxpool.Pool }

// NewRetrievalStore uses the shared pool; source writes remain owned by their original contexts.
func NewRetrievalStore(pool *pgxpool.Pool) *RetrievalStore { return &RetrievalStore{pool} }

// enqueueCaptureIndex projects an existing transactional capture outbox event into the source queue.
func enqueueCaptureIndex(ctx context.Context, tx pgx.Tx, repository, session string, revision, outbox int64) error {
	id, err := identifier.New()
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO retrieval_jobs(id,source_kind,project_id,repository_id,session_id,revision,source_digest,source_observed_at,outbox_id)
 SELECT $1,'capture',r.project_id,cr.repository_id,cr.session_id,cr.revision,cr.digest,cr.created_at,$5 FROM capture_revisions cr JOIN repositories r ON r.id=cr.repository_id WHERE cr.repository_id=$2 AND cr.session_id=$3 AND cr.revision=$4 ON CONFLICT DO NOTHING`, id, repository, session, revision, outbox)
	return err
}

const sourceColumns = `j.source_kind,j.project_id::text,COALESCE(j.repository_id::text,''),COALESCE(j.session_id::text,''),COALESCE(j.revision,0),j.source_digest,COALESCE(j.code_sha,''),COALESCE(j.code_path,''),COALESCE(j.code_blob_id,'')`

func sourceDest(s *domain.Source) []any {
	return []any{&s.Kind, &s.ProjectID, &s.RepositoryID, &s.SessionID, &s.Revision, &s.Digest, &s.CommitSHA, &s.FilePath, &s.BlobID}
}

// Claim leases one immutable source, keeping every provider operation outside SQL transactions.
func (s *RetrievalStore) Claim(ctx context.Context) (application.Lease, bool, error) {
	var lease application.Lease
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return lease, false, err
	}
	defer tx.Rollback(ctx)
	dest := append([]any{&lease.ID}, sourceDest(&lease.Source)...)
	dest = append(dest, &lease.ObservedAt, &lease.OutboxID)
	err = tx.QueryRow(ctx, `SELECT j.id::text,`+sourceColumns+`,j.source_observed_at,COALESCE(j.outbox_id,0) FROM retrieval_jobs j WHERE (j.completed_at IS NULL OR j.projection_version<2) AND j.available_at<=now() AND (j.lease_until IS NULL OR j.lease_until<=now()) ORDER BY j.available_at,j.id LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(dest...)
	if errors.Is(err, pgx.ErrNoRows) {
		return lease, false, nil
	}
	if err != nil {
		return lease, false, err
	}
	lease.Owner, err = identifier.New()
	if err != nil {
		return lease, false, err
	}
	if _, err = tx.Exec(ctx, `UPDATE retrieval_jobs SET lease_owner=$2,lease_until=now()+interval '60 seconds' WHERE id=$1`, lease.ID, lease.Owner); err != nil {
		return lease, false, err
	}
	err = tx.Commit(ctx)
	return lease, err == nil, err
}

// Load validates original source digests and projects parts without trusting indexing-job payload text.
func (s *RetrievalStore) Load(ctx context.Context, lease application.Lease) ([]application.Part, error) {
	if lease.Source.Validate() != nil {
		return nil, domain.ErrInvalid
	}
	var raw []byte
	parts := []application.Part{}
	source := lease.Source
	switch source.Kind {
	case "capture":
		err := s.pool.QueryRow(ctx, `SELECT cr.canonical FROM capture_revisions cr JOIN repositories r ON r.id=cr.repository_id WHERE r.project_id=$1 AND cr.repository_id=$2 AND cr.session_id=$3 AND cr.revision=$4 AND cr.digest=$5`, source.ProjectID, source.RepositoryID, source.SessionID, source.Revision, source.Digest).Scan(&raw)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		var snapshot capture.Snapshot
		if json.Unmarshal(raw, &snapshot) != nil {
			return nil, domain.ErrInvalid
		}
		session, err := capture.NewSession(snapshot)
		if err != nil || session.Digest() != source.Digest {
			return nil, domain.ErrInvalid
		}
		for i, message := range snapshot.Messages {
			metadata := captureMetadata(snapshot, message)
			title := message.ToolName
			if title == "" {
				title = strings.SplitN(strings.TrimSpace(message.Text), "\n", 2)[0]
			}
			if title == "" {
				title = message.Role
			}
			if runes := []rune(title); len(runes) > 160 {
				title = string(runes[:160])
			}

			paths := []string{}
			for _, change := range message.Changes {
				paths = append(paths, change.Path)
			}
			parts = append(parts, application.Part{Kind: "session", Name: fmt.Sprintf("messages/%d", i), Title: title, Content: message.Text, Metadata: metadata, FilePaths: paths})
		}
		if snapshot.Origin != nil {
			origin, err := json.Marshal(snapshot.Origin)
			if err != nil {
				return nil, err
			}
			parts = append(parts, application.Part{Kind: "session", Name: "import_origin", Title: "Imported " + snapshot.Origin.System + " origin and source-reported provenance; not verified authorship", Content: string(origin)})
			for i, claim := range snapshot.Origin.Claims {
				raw, _ := json.Marshal(claim)
				m := domain.Metadata{Relation: "git_ai_reported", ReportedLineStart: claim.Start, ReportedLineEnd: claim.End, ModelBasis: "import_report", AttributionKind: claim.Kind, ReportedCommit: snapshot.Origin.TargetCommit}
				if claim.Agent != nil {
					m.Agent = claim.Agent.Tool
					m.Model = knownModel(claim.Agent.Model)
				}
				parts = append(parts, application.Part{Kind: "code_reference", Name: fmt.Sprintf("claims/%d", i), Title: claim.Path, Content: string(raw), Metadata: m, FilePaths: []string{claim.Path}})
			}
		}
		if len(snapshot.Files) > 0 {
			m := captureMetadata(snapshot, capture.Message{})
			m.Relation = "session_reported"
			parts = append(parts, application.Part{Kind: "code_reference", Name: "files", Title: snapshot.Files[0], Content: strings.Join(snapshot.Files, "\n"), Metadata: m, FilePaths: snapshot.Files})
		}
	case "code":
		err := s.pool.QueryRow(ctx, `SELECT f.model FROM forge_code_snapshots f JOIN repositories r ON r.id=f.repository_id WHERE r.project_id=$1 AND f.repository_id=$2 AND f.sha=$3 AND f.path=$4 AND f.digest=$5`, source.ProjectID, source.RepositoryID, source.CommitSHA, source.FilePath, source.Digest).Scan(&raw)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		if err != nil {
			return nil, err
		}
		var file forge.CodeFile
		if json.Unmarshal(raw, &file) != nil || file.ValidateRetained() != nil || file.ProjectionDigest() != source.Digest || file.Identity.BlobID != source.BlobID {
			return nil, domain.ErrInvalid
		}
		title := []rune(file.Identity.Path)
		for len(string(title)) > 2048 {
			title = title[:len(title)-1]
		}
		parts = append(parts, application.Part{Kind: "code", Name: "content", Title: string(title), Content: file.Content, Metadata: domain.Metadata{Relation: "retained_code"}, FilePaths: []string{file.Identity.Path}})

	}
	return parts, nil
}

// Finish atomically persists all chunks and their source/capture-outbox ACK under lease fencing.
func (s *RetrievalStore) Finish(ctx context.Context, lease application.Lease, chunks []domain.Chunk, code string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var attempts int
	var current domain.Source
	var outbox int64
	dest := append([]any{&attempts}, sourceDest(&current)...)
	dest = append(dest, &outbox)
	err = tx.QueryRow(ctx, `SELECT attempts,`+sourceColumns+`,COALESCE(outbox_id,0) FROM retrieval_jobs j WHERE id=$1 AND lease_owner=$2 AND lease_until>now() AND (completed_at IS NULL OR projection_version<2) FOR UPDATE`, lease.ID, lease.Owner).Scan(dest...)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrLease
	}
	if err != nil {
		return err
	}
	if current != lease.Source || outbox != lease.OutboxID {
		return domain.ErrLease
	}
	if code != "" {
		if code != "index_source_invalid" && code != "index_source_unavailable" {
			return domain.ErrInvalid
		}
		_, err = tx.Exec(ctx, `UPDATE retrieval_jobs SET attempts=attempts+1,last_error_code=$2,lease_owner=NULL,lease_until=NULL,available_at=now()+LEAST(900,power(2,LEAST(attempts,8))*5)*interval '1 second' WHERE id=$1`, lease.ID, code)
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	if len(chunks) > 8192 {
		return domain.ErrInvalid
	}
	records := make([][]any, 0, len(chunks))
	for _, chunk := range chunks {
		if chunk.Source != lease.Source || !domain.Digest(chunk.ID) || domain.Hash([]byte(chunk.Content)) != chunk.ContentDigest || chunk.End-chunk.Start != len([]rune(chunk.Content)) || chunk.Metadata.Validate() != nil || len(chunk.FilePaths) > 5000 {
			return domain.ErrInvalid
		}
		for _, p := range chunk.FilePaths {
			if !capture.ValidFilePath(p) {
				return domain.ErrInvalid
			}
		}
		metadata, _ := json.Marshal(chunk.Metadata)
		paths := chunk.FilePaths
		if paths == nil {
			paths = []string{}
		}
		records = append(records, []any{chunk.ID, lease.ID, chunk.Kind, chunk.Part, chunk.Start, chunk.End, chunk.Title, chunk.Content, chunk.ContentDigest, domain.Terms(chunk.Title + " " + chunk.Part + " " + chunk.Metadata.Agent + " " + chunk.Metadata.Model), domain.Terms(chunk.Content), chunk.LineStart, chunk.LineEnd, metadata, paths})
	}
	// A source either has its complete deterministic projection or no acknowledged projection.
	if _, err = tx.Exec(ctx, `DELETE FROM retrieval_chunks WHERE job_id=$1`, lease.ID); err != nil {
		return err
	}
	if len(records) > 0 {
		_, err = tx.CopyFrom(ctx, pgx.Identifier{"retrieval_chunks"}, []string{"id", "job_id", "kind", "part", "start_rune", "end_rune", "title", "content", "content_digest", "title_terms", "content_terms", "line_start", "line_end", "metadata", "file_paths"}, pgx.CopyFromRows(records))
		if err != nil {
			return err
		}
		// Aggregate lexemes, not stored text: words far apart in one message still match.
		// Restrict this additional vector to bounded messages, never an entire code file/transcript.
		_, err = tx.Exec(ctx, `WITH parts AS (
 SELECT job_id,part,min(start_rune) AS anchor,min(title_terms) AS title,string_agg(content_terms,' ' ORDER BY start_rune) AS content
 FROM retrieval_chunks WHERE job_id=$1 AND kind='session' AND part ~ '^messages/[0-9]{1,4}$' GROUP BY job_id,part
 ) UPDATE retrieval_chunks d SET part_lexical=
 setweight(to_tsvector('simple',p.title)||to_tsvector('english',p.title)||to_tsvector('russian',p.title),'A')||
 setweight(to_tsvector('simple',p.content)||to_tsvector('english',p.content)||to_tsvector('russian',p.content),'B')
 FROM parts p WHERE d.job_id=p.job_id AND d.part=p.part AND d.start_rune=p.anchor`, lease.ID)
		if err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE retrieval_jobs SET completed_at=now(),projection_version=2,attempts=0,last_error_code=NULL,lease_owner=NULL,lease_until=NULL WHERE id=$1`, lease.ID); err != nil {
		return err
	}
	if lease.OutboxID != 0 {
		if _, err = tx.Exec(ctx, `UPDATE outbox_events SET completed_at=now(),lease_owner=NULL,lease_until=NULL WHERE id=$1`, lease.OutboxID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// enqueueCodeIndex records a verified sanitized file projection in its persistence transaction.
func enqueueCodeIndex(ctx context.Context, tx pgx.Tx, repo, sha, path, digest string) error {
	id, err := identifier.New()
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO retrieval_jobs(id,source_kind,project_id,repository_id,code_sha,code_path,code_blob_id,source_digest,source_observed_at)
 SELECT $1,'code',r.project_id,f.repository_id,f.sha,f.path,f.blob_id,f.digest,f.observed_at FROM forge_code_snapshots f JOIN repositories r ON r.id=f.repository_id WHERE f.repository_id=$2 AND f.sha=$3 AND f.path=$4 AND f.digest=$5 ON CONFLICT DO NOTHING`, id, repo, sha, path, digest)
	return err
}
