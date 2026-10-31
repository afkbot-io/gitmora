package application

import (
	"context"
	"reflect"

	"workgraph.local/platform/internal/collector/domain"
)

// BeginFrontier durably pins the initial task and HEAD before native source reads.
func (s *Service) BeginFrontier(ctx context.Context, source domain.NativeSource, call, before string) (domain.Frontier, error) {
	var result domain.Frontier
	if source.Validate() != nil {
		return result, domain.ErrInvalid
	}
	err := s.ledger.Within(ctx, func(tx Transaction) error {
		config, err := tx.Config()
		if err != nil {
			return err
		}
		if !config.AllowsAgent(source.Agent) {
			return domain.ErrDisabled
		}
		key := source.Key(config.RepositoryID)
		pinned, found, err := tx.FrontierSource(key)
		if err != nil {
			return err
		}
		if found {
			source = pinned
		} else {
			// Previously captured streams retain their original task, including an absent binding.
			local, exists, err := tx.NativePin(key)
			if err != nil {
				return err
			}
			if exists {
				recording, exists, err := tx.Find(local)
				if err != nil {
					return err
				}
				if !exists {
					return domain.ErrConflict
				}
				source.Task = recording.State().Snapshot.Task
			} else {
				source.Task, err = tx.Binding(source.Worktree)
				if err != nil {
					return err
				}
			}
			if err := tx.SaveFrontierSource(key, source); err != nil {
				return err
			}
		}
		result, err = domain.BeginFrontier(config.RepositoryID, source, call, before)
		if err != nil {
			return err
		}
		old, exists, err := tx.Frontier(result.ID)
		if err != nil {
			return err
		}
		if exists {
			result = old
			return nil
		}
		pending, err := tx.PendingFrontiers(source.Worktree)
		if err != nil {
			return err
		}
		if len(pending) >= domain.MaxPendingFrontiers {
			return domain.ErrConflict
		}
		return tx.SaveFrontier(result)
	})
	return result, err
}

// EndFrontier persists the first observed end even if the next source read or process fails.
func (s *Service) EndFrontier(ctx context.Context, source domain.NativeSource, call, after string) (domain.Frontier, error) {
	var result domain.Frontier
	err := s.ledger.Within(ctx, func(tx Transaction) error {
		config, err := tx.Config()
		if err != nil {
			return err
		}
		identity, err := domain.BeginFrontier(config.RepositoryID, source, call, "")
		if err != nil {
			return err
		}
		result, err = loadFrontier(tx, source.Worktree, identity.ID)
		if err != nil {
			return err
		}
		if err := result.End(after); err != nil {
			return err
		}
		return tx.SaveFrontier(result)
	})
	return result, err
}

// Frontier reads an exact worktree-scoped entry, including terminal replay identities.
func (s *Service) Frontier(ctx context.Context, worktree, id string) (domain.Frontier, error) {
	var result domain.Frontier
	err := s.ledger.Within(ctx, func(tx Transaction) error {
		var err error
		result, err = loadFrontier(tx, worktree, id)
		return err
	})
	return result, err
}

// PendingFrontiers returns the complete bounded unfinished set for one worktree.
func (s *Service) PendingFrontiers(ctx context.Context, worktree string) ([]domain.Frontier, error) {
	result := []domain.Frontier{}
	if worktree == "" {
		return nil, domain.ErrInvalid
	}
	err := s.ledger.Within(ctx, func(tx Transaction) error {
		if _, err := tx.Config(); err != nil {
			return err
		}
		var err error
		result, err = tx.PendingFrontiers(worktree)
		return err
	})
	return result, err
}

// ResolveFrontier supplies an explicit missing end; callers verify the actual Git object first.
func (s *Service) ResolveFrontier(ctx context.Context, worktree, id, end, reason string) (domain.Frontier, error) {
	var result domain.Frontier
	err := s.ledger.Within(ctx, func(tx Transaction) error {
		var err error
		result, err = loadFrontier(tx, worktree, id)
		if err != nil {
			return err
		}
		if err := result.Resolve(end, reason); err != nil {
			return err
		}
		return tx.SaveFrontier(result)
	})
	return result, err
}

// AbandonFrontier closes one association intent, retaining evidence and replay identity.
func (s *Service) AbandonFrontier(ctx context.Context, worktree, id, reason string) error {
	return s.ledger.Within(ctx, func(tx Transaction) error {
		f, err := loadFrontier(tx, worktree, id)
		if err != nil {
			return err
		}
		if err := f.Abandon(reason); err != nil {
			return err
		}
		return tx.SaveFrontier(f)
	})
}

// RecordNativeFrontiers binds completed tools to the exact accepted window in its transaction.
func (s *Service) RecordNativeFrontiers(ctx context.Context, worktree string, input domain.Observation, ids []string) (string, error) {
	if input.History == nil || len(ids) > domain.MaxPendingFrontiers {
		return "", domain.ErrInvalid
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if !domain.ValidFrontierID(id) || seen[id] {
			return "", domain.ErrInvalid
		}
		seen[id] = true
	}
	return s.recordNativePage(ctx, worktree, input, ids)
}

func bindFrontiers(tx Transaction, native, session string, ids []string) error {
	for _, id := range ids {
		f, found, err := tx.Frontier(id)
		if err != nil {
			return err
		}
		if !found || f.NativeKey != native {
			return domain.ErrConflict
		}
		if err := f.Bind(session); err != nil {
			return err
		}
		if err := tx.SaveFrontier(f); err != nil {
			return err
		}
	}
	return nil
}

// ApplyFrontier commits Git-resolved associations and completion under an unchanged-state fence.
// Resolve Git outside the ledger transaction using only the stored Before/After.
func (s *Service) ApplyFrontier(ctx context.Context, worktree string, expected domain.Frontier, commits []string) error {
	return s.ledger.Within(ctx, func(tx Transaction) error {
		actual, err := loadFrontier(tx, worktree, expected.ID)
		if err != nil {
			return err
		}
		expected.Applied = actual.Applied // Exact acknowledgement retries are harmless.
		if !reflect.DeepEqual(actual, expected) || actual.Abandoned {
			return domain.ErrConflict
		}
		if actual.Applied {
			return nil
		}
		if err := actual.Apply(); err != nil {
			return err
		}
		entry, found, err := tx.FindSession(actual.TargetSessionID)
		if err != nil {
			return err
		}
		if !found || entry.Recording.State().Worktree != worktree {
			return domain.ErrConflict
		}
		changed, err := entry.Recording.LinkObservedCommits(commits)
		if err != nil {
			return err
		}
		if changed {
			if err := seal(tx, entry); err != nil {
				return err
			}
		}
		if err := tx.SaveFrontier(actual); err != nil {
			return err
		}
		return flushPublished(tx)
	})
}

func loadFrontier(tx Transaction, worktree, id string) (domain.Frontier, error) {
	if !domain.ValidFrontierID(id) || worktree == "" {
		return domain.Frontier{}, domain.ErrInvalid
	}
	config, err := tx.Config()
	if err != nil {
		return domain.Frontier{}, err
	}
	f, found, err := tx.Frontier(id)
	if err != nil {
		return f, err
	}
	if !found || f.Source.Worktree != worktree || f.NativeKey != f.Source.Key(config.RepositoryID) {
		return domain.Frontier{}, domain.ErrConflict
	}
	return f, nil
}
