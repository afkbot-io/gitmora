package application

import (
	"context"
	"workgraph.local/platform/internal/collector/domain"
)

func associateCheckpoint(tx Transaction, worktree, id string, commits []string, messageEnd *int) error {
	if len(commits) == 0 {
		return nil
	}
	chain := []RecordingEntry{}
	seen := map[string]bool{}
	for len(chain) < 256 {
		if seen[id] {
			return domain.ErrConflict
		}
		seen[id] = true
		entry, found, err := tx.FindSession(id)
		if err != nil {
			return err
		}
		if !found || entry.Recording.State().Worktree != worktree {
			return domain.ErrConflict
		}
		chain = append(chain, entry)
		state := entry.Recording.State()
		if state.Snapshot.Continuation == nil {
			break
		}
		id = state.Snapshot.Continuation.PreviousSessionID
	}
	if chain[len(chain)-1].Recording.State().Snapshot.Continuation != nil {
		return domain.ErrConflict
	}
	for i := len(chain) - 1; i >= 0; i-- {
		entry := chain[i]
		end := len(entry.Recording.State().Snapshot.Messages)
		if i == 0 && messageEnd != nil {
			end = *messageEnd
		}
		changed, err := entry.Recording.LinkCommitCheckpointAt(commits, end)
		if err != nil {
			return err
		}
		if changed {
			if err := seal(tx, entry); err != nil {
				return err
			}
		}
	}
	return nil
}

// AssociateCheckpoint is an observed Git transition, distinct from broad manual linking.
func (s *Service) AssociateCheckpoint(ctx context.Context, worktree, id string, commits []string) error {
	return s.ledger.Within(ctx, func(tx Transaction) error {
		if err := associateCheckpoint(tx, worktree, id, commits, nil); err != nil {
			return err
		}
		return flushPublished(tx)
	})
}
