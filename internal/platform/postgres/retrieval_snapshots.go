package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/identifier"
	"workgraph.local/platform/internal/retrieval/domain"
)

func saveSearchSnapshot(ctx context.Context, tx pgx.Tx, p access.Principal, q domain.Query, items []domain.Hit) (string, error) {
	id, err := identifier.New()
	if err != nil {
		return "", err
	}
	positions := make([]domain.SearchPosition, len(items))
	for i, h := range items {
		positions[i] = domain.SearchPosition{ID: h.ID, Score: h.Score}
	}
	raw, _ := json.Marshal(positions)
	// Serialize only this user's tiny cache edit, never inference or ranking.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('retrieval-search-cache/'||$1,0))`, p.UserID); err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM retrieval_search_snapshots WHERE id IN(SELECT id FROM retrieval_search_snapshots WHERE expires_at<=clock_timestamp() LIMIT 100 FOR UPDATE SKIP LOCKED)`); err != nil {
		return "", err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM retrieval_search_snapshots WHERE id IN(SELECT id FROM retrieval_search_snapshots WHERE user_id=$1 ORDER BY created_at DESC,id OFFSET 15)`, p.UserID); err != nil {
		return "", err
	}
	_, err = tx.Exec(ctx, `INSERT INTO retrieval_search_snapshots(id,user_id,project_id,query_digest,profile,vector_digest,engine,indexed_before,items) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, id, p.UserID, q.ProjectID, q.Fingerprint(), q.SemanticProfile, q.VectorDigest, q.Engine, q.ReadAt, raw)
	return id, err
}

func loadSearchSnapshot(ctx context.Context, tx pgx.Tx, p access.Principal, q domain.Query) ([]domain.SearchPosition, error) {
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT items FROM retrieval_search_snapshots WHERE id=$1 AND user_id=$2 AND project_id=$3 AND query_digest=$4 AND profile=$5 AND vector_digest=$6 AND engine=$7 AND indexed_before=$8 AND expires_at>clock_timestamp()`, q.After.SnapshotID, p.UserID, q.ProjectID, q.Fingerprint(), q.SemanticProfile, q.VectorDigest, q.Engine, q.ReadAt).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrSearchExpired
	}
	if err != nil {
		return nil, err
	}
	var result []domain.SearchPosition
	if json.Unmarshal(raw, &result) != nil || len(result) > 400 {
		return nil, domain.ErrInvalid
	}
	for _, v := range result {
		if !domain.Digest(v.ID) || v.Score <= 0 {
			return nil, domain.ErrInvalid
		}
	}
	return result, nil
}
