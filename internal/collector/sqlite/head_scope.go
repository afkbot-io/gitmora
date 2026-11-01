package sqlite

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	capture "workgraph.local/platform/internal/capture/domain"
	"workgraph.local/platform/internal/collector/domain"
)

func (t *transaction) HeadScope(id string) ([]string, bool, error) {
	var raw []byte
	var digest string
	err := t.tx.QueryRowContext(t.ctx, `SELECT h.tips,h.digest FROM git_frontier_scopes f JOIN git_head_scopes h ON h.digest=f.digest WHERE f.frontier_id=?`, id).Scan(&raw, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var tips []string
	sum := sha256.Sum256(raw)
	if len(raw) > 280000 || hex.EncodeToString(sum[:]) != digest || json.Unmarshal(raw, &tips) != nil || len(tips) > 4096 {
		return nil, false, domain.ErrInvalid
	}
	for _, sha := range tips {
		if !capture.ValidCommit(sha) {
			return nil, false, domain.ErrInvalid
		}
	}
	return tips, true, nil
}

func (t *transaction) SaveHeadScope(id string, tips []string) error {
	if !domain.ValidFrontierID(id) || len(tips) > 4096 {
		return domain.ErrInvalid
	}
	tips = slices.Clone(tips)
	for _, sha := range tips {
		if !capture.ValidCommit(sha) {
			return domain.ErrInvalid
		}
	}
	slices.Sort(tips)
	tips = slices.Compact(tips)
	if tips == nil {
		tips = []string{}
	}
	raw, _ := json.Marshal(tips)
	sum := sha256.Sum256(raw)
	digest := hex.EncodeToString(sum[:])
	var existing string
	err := t.tx.QueryRowContext(t.ctx, `SELECT digest FROM git_frontier_scopes WHERE frontier_id=?`, id).Scan(&existing)
	if err == nil {
		if existing != digest {
			return domain.ErrConflict
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err = t.tx.ExecContext(t.ctx, `INSERT INTO git_head_scopes(digest,tips) VALUES(?,?) ON CONFLICT(digest) DO NOTHING`, digest, raw); err != nil {
		return err
	}
	_, err = t.tx.ExecContext(t.ctx, `INSERT INTO git_frontier_scopes(frontier_id,digest)VALUES(?,?)`, id, digest)
	return err
}
