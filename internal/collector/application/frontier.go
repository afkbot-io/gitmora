package application

import (
	"context"
	"reflect"

	"workgraph.local/platform/internal/collector/domain"
)

// BeginFrontier durably pins the initial source scope and HEAD before native source reads.
func (s *Service) BeginFrontier(ctx context.Context, source domain.NativeSource, call, before string) (domain.Frontier, error) {
	return s.beginFrontier(ctx, source, call, before, nil)
}

// BeginFrontierWithHeads atomically retains pre-tool reachable tips for checkout-safe Git attribution.
// The first scope wins on replay; these private Git IDs never enter capture snapshots.
func (s *Service) BeginFrontierWithHeads(ctx context.Context, source domain.NativeSource, call, before string, heads []string) (domain.Frontier, error) {
	if heads == nil {
		heads = []string{}
	}
	return s.beginFrontier(ctx, source, call, before, heads)
}

func (s *Service) beginFrontier(ctx context.Context, source domain.NativeSource, call, before string, heads []string) (domain.Frontier, error) {
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
				source.Task = nil
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
		if err := tx.SaveFrontier(result); err != nil {
			return err
		}
		if heads != nil {
			return tx.SaveHeadScope(result.ID, heads)
		}
		return nil
	})
	return result, err
}

// FrontierHeads reads the immutable pre-tool Git scope without returning it in journal listings.
func (s *Service) FrontierHeads(ctx context.Context, worktree, id string) (heads []string, found bool, err error) {
	err = s.ledger.Within(ctx, func(tx Transaction) error {
		if _, err := loadFrontier(tx, worktree, id); err != nil {
			return err
		}
		var err error
		heads, found, err = tx.HeadScope(id)
		return err
	})
	return
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
		if err != nil {
			return err
		}
		return orderFrontiers(tx, result)
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
	bindings := make([]domain.CheckpointBinding, 0, len(ids))
	for _, id := range ids {
		if !domain.ValidFrontierID(id) || seen[id] {
			return "", domain.ErrInvalid
		}
		seen[id] = true
		bindings = append(bindings, domain.CheckpointBinding{ID: id})
	}
	return s.recordNativePage(ctx, worktree, input, bindings)
}

// RecordNativeCheckpoints accepts source content, tool targets and exact endpoints atomically.
// A process crash cannot leave a new checkpoint bound without its message position.
func (s *Service) RecordNativeCheckpoints(ctx context.Context, worktree string, input domain.Observation, bindings []domain.CheckpointBinding) (string, error) {
	if input.History == nil || len(bindings) > domain.MaxPendingFrontiers {
		return "", domain.ErrInvalid
	}
	seen := map[string]bool{}
	for _, b := range bindings {
		if !domain.ValidFrontierID(b.ID) || seen[b.ID] || b.MessageEnd < 1 || b.MessageEnd > len(input.Messages) {
			return "", domain.ErrInvalid
		}
		seen[b.ID] = true
	}
	return s.recordNativePage(ctx, worktree, input, bindings)
}

func bindFrontiers(tx Transaction, native, session string, bindings []domain.CheckpointBinding) error {
	for _, binding := range bindings {
		f, found, err := tx.Frontier(binding.ID)
		if err != nil {
			return err
		}
		if !found || f.NativeKey != native {
			return domain.ErrConflict
		}
		if err := f.Bind(session); err != nil {
			return err
		}
		if binding.MessageEnd > 0 {
			entry, found, err := tx.FindSession(session)
			if err != nil {
				return err
			}
			if !found || binding.MessageEnd > len(entry.Recording.State().Snapshot.Messages) {
				return domain.ErrInvalid
			}
			if f.MessageEnd != nil && *f.MessageEnd != binding.MessageEnd || f.Applied && f.MessageEnd == nil {
				return domain.ErrConflict
			}
			end := binding.MessageEnd
			f.MessageEnd = &end
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
		config, err := tx.Config()
		if err != nil {
			return err
		}
		if !config.AllowsAgent(actual.Source.Agent) {
			return domain.ErrDisabled
		}
		expected.Applied = actual.Applied // Exact acknowledgement retries are harmless.
		if !reflect.DeepEqual(actual, expected) || actual.Abandoned {
			return domain.ErrConflict
		}
		if actual.Applied {
			return nil
		}
		if actual.MessageEnd != nil {
			pending, err := tx.PendingFrontiers(worktree)
			if err != nil {
				return err
			}
			if err = orderFrontiers(tx, pending); err != nil {
				return err
			}
			// An unresolved tool has no trustworthy source position. It might precede
			// this endpoint, so do not silently consume its context during recovery.
			for _, other := range pending {
				if other.ID != actual.ID && other.NativeKey == actual.NativeKey && (other.TargetSessionID == "" || other.MessageEnd == nil) {
					return domain.ErrConflict
				}
			}
			for _, prior := range pending {
				if prior.ID == actual.ID {
					break
				}
				if prior.NativeKey == actual.NativeKey && prior.TargetSessionID != "" && prior.MessageEnd != nil {
					return domain.ErrConflict
				}
			}
		}
		if err := actual.Apply(); err != nil {
			return err
		}
		if actual.MessageEnd == nil {
			entry, found, e := tx.FindSession(actual.TargetSessionID)
			if e != nil {
				return e
			}
			if !found {
				return domain.ErrConflict
			}
			changed, e := entry.Recording.LinkObservedCommits(commits)
			if e != nil {
				return e
			}
			if changed {
				if e := seal(tx, entry); e != nil {
					return e
				}
			}
		} else if err := associateCheckpoint(tx, worktree, actual.TargetSessionID, commits, actual.MessageEnd); err != nil {
			return err
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
