package sqlite

import (
	"encoding/json"
	capture "workgraph.local/platform/internal/capture/domain"
)

// Test-only writer recreates data produced by retired task-binding clients.
func (t *transaction) SeedLegacyBinding(worktree string, task *capture.TaskRef) error {
	if _, err := t.Config(); err != nil {
		return err
	}
	if task == nil {
		_, err := t.tx.ExecContext(t.ctx, "DELETE FROM bindings WHERE worktree=?", worktree)
		return err
	}
	raw, err := json.Marshal(task)
	if err != nil {
		return err
	}
	_, err = t.tx.ExecContext(t.ctx, "INSERT INTO bindings(worktree,task) VALUES(?,?) ON CONFLICT(worktree) DO UPDATE SET task=excluded.task", worktree, string(raw))
	return err
}
