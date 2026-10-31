package sqlite

import (
	"database/sql"
	"encoding/json"
	"errors"

	"workgraph.local/platform/internal/collector/domain"
)

func (t *transaction) FrontierSource(key string) (domain.NativeSource, bool, error) {
	var source domain.NativeSource
	var raw []byte
	err := t.tx.QueryRowContext(t.ctx, `SELECT data FROM native_frontier_sources WHERE native_key=?`, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return source, false, nil
	}
	if err != nil {
		return source, false, err
	}
	if json.Unmarshal(raw, &source) != nil || source.Validate() != nil {
		return source, false, domain.ErrInvalid
	}
	return source, true, nil
}
func (t *transaction) SaveFrontierSource(key string, source domain.NativeSource) error {
	if source.Validate() != nil {
		return domain.ErrInvalid
	}
	raw, err := json.Marshal(source)
	if err != nil {
		return err
	}
	_, err = t.tx.ExecContext(t.ctx, `INSERT INTO native_frontier_sources(native_key,data) VALUES(?,?)`, key, raw)
	return err
}
func (t *transaction) Frontier(id string) (domain.Frontier, bool, error) {
	var raw []byte
	err := t.tx.QueryRowContext(t.ctx, `SELECT data FROM native_frontiers WHERE id=?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Frontier{}, false, nil
	}
	if err != nil {
		return domain.Frontier{}, false, err
	}
	f, err := decodeFrontier(raw)
	if err == nil && f.ID != id {
		return domain.Frontier{}, false, domain.ErrConflict
	}
	return f, err == nil, err
}
func (t *transaction) SaveFrontier(f domain.Frontier) error {
	f, err := domain.RestoreFrontier(f)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(f)
	if err != nil {
		return err
	}
	_, err = t.tx.ExecContext(t.ctx, `INSERT INTO native_frontiers(id,native_key,worktree,status,data) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET status=excluded.status,data=excluded.data`, f.ID, f.NativeKey, f.Source.Worktree, f.Status(), raw)
	return err
}
func (t *transaction) PendingFrontiers(worktree string) ([]domain.Frontier, error) {
	rows, err := t.tx.QueryContext(t.ctx, `SELECT data FROM native_frontiers WHERE worktree=? AND status IN('started','ended','bound') ORDER BY id LIMIT ?`, worktree, domain.MaxPendingFrontiers+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.Frontier{}
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		f, err := decodeFrontier(raw)
		if err != nil {
			return nil, err
		}
		if f.Source.Worktree != worktree || f.Applied || f.Abandoned {
			return nil, domain.ErrConflict
		}
		result = append(result, f)
	}
	if len(result) > domain.MaxPendingFrontiers {
		return nil, domain.ErrConflict
	}
	return result, rows.Err()
}
func decodeFrontier(raw []byte) (domain.Frontier, error) {
	var f domain.Frontier
	if json.Unmarshal(raw, &f) != nil {
		return f, domain.ErrInvalid
	}
	return domain.RestoreFrontier(f)
}
