package application

import (
	"context"
	"errors"

	"workgraph.local/platform/internal/collector/domain"
	"workgraph.local/platform/internal/identifier"
)

// NativeStream pins initial task scope and the latest accepted source-observation branch.
type NativeStream struct{ RootKey, ActiveKey string }

func (s *Service) recordNativePage(ctx context.Context, worktree string, input domain.Observation, frontiers []string) (string, error) {
	o, err := domain.Normalize(input)
	if err != nil || worktree == "" {
		return "", domain.ErrInvalid
	}
	input = o.Value()
	var result string
	err = s.ledger.Within(ctx, func(tx Transaction) error {
		config, err := tx.Config()
		if err != nil {
			return err
		}
		if !config.AllowsAgent(input.Agent.Name) {
			return domain.ErrDisabled
		}
		native := domain.SessionKey(config.RepositoryID, worktree, nil, o)
		id, digest, found, err := tx.NativeEvent(native, o.EventKey())
		if err != nil {
			return err
		}
		if found {
			if digest != o.Digest() {
				return domain.ErrConflict
			}
			result = id
			return bindFrontiers(tx, native, result, frontiers)
		}
		stream, exists, err := tx.Stream(native)
		if err != nil {
			return err
		}
		hadStream := exists
		rootKey := stream.RootKey
		if !exists {
			rootKey, exists, err = tx.NativePin(native)
			if err != nil {
				return err
			}
			stream = NativeStream{RootKey: rootKey, ActiveKey: rootKey}
		}
		var root *domain.Recording
		if exists {
			root, found, err = tx.Find(rootKey)
			if err != nil {
				return err
			}
			if !found {
				return domain.ErrConflict
			}
			if input.StartedAt != nil && !input.StartedAt.Equal(root.State().Snapshot.StartedAt) {
				return domain.ErrConflict
			}
		}
		var rootID string
		if root != nil {
			rootID = root.State().Snapshot.SessionID
		} else {
			rootID, err = identifier.New()
			if err != nil {
				return err
			}
		}
		key, known, err := tx.NativePage(native, input.History.PageID)
		if err != nil {
			return err
		}
		var recording *domain.Recording
		if known {
			recording, found, err = tx.Find(key)
			if err != nil {
				return err
			}
			if !found {
				return domain.ErrConflict
			}
		}
		value := o.Value()
		fileKeys := []string{}
		for _, event := range value.FileEvents {
			_, seen, err := tx.EventDigest(rootID, event.Key())
			if err != nil {
				return err
			}
			if !seen {
				value.ChangedFiles = append(value.ChangedFiles, event.Path)
				fileKeys = append(fileKeys, event.Key())
			}
		}
		value.FileEvents = nil
		effective, err := domain.Normalize(value)
		if err != nil {
			return err
		}
		reason := input.History.Reason
		if known {
			if err = recording.ObserveNative(effective); err != nil {
				if !errors.Is(err, domain.ErrConflict) {
					return err
				}
				known = false
				reason = "history_replacement"
			}
		}
		advance := false
		if !known {
			advance = true
			if root == nil {
				task, err := tx.Binding(worktree)
				if err != nil {
					return err
				}
				pinned, exists, err := tx.FrontierSource(native)
				if err != nil {
					return err
				}
				if exists {
					task = pinned.Task
				}
				key = domain.SessionKey(config.RepositoryID, worktree, task, o)
				recording, err = domain.NewRecording(config.RepositoryID, rootID, worktree, task, effective, s.now())
				if err != nil {
					return err
				}
				rootKey = key
			} else {
				parent, ok, err := tx.Find(stream.ActiveKey)
				if err != nil {
					return err
				}
				if !ok {
					return domain.ErrConflict
				}
				if err = seal(tx, RecordingEntry{Key: stream.ActiveKey, Recording: parent}); err != nil {
					return err
				}
				id, err := identifier.New()
				if err != nil {
					return err
				}
				key = "native:" + id
				if !hadStream {
					reason = "protocol_upgrade"
				}
				recording, err = domain.ContinueRecording(parent, rootID, id, effective, reason, s.now())
				if err != nil {
					return err
				}
			}
		}
		if err = tx.Save(key, recording); err != nil {
			return err
		}
		if err = tx.PinNative(native, rootKey); err != nil {
			return err
		}
		if err = tx.SaveNative(native, input.History.PageID, rootKey, key, advance); err != nil {
			return err
		}
		result = recording.State().Snapshot.SessionID
		published, err := tx.PublicationAllowed(result)
		if err != nil {
			return err
		}
		if published && recording.State().Dirty {
			if err = seal(tx, RecordingEntry{Key: key, Recording: recording}); err != nil {
				return err
			}
		}
		seen := map[string]bool{}
		for _, key := range fileKeys {
			if !seen[key] {
				if err = tx.RememberEvent(rootID, key, key); err != nil {
					return err
				}
				seen[key] = true
			}
		}
		if err := tx.RememberNativeEvent(native, o.EventKey(), o.Digest(), result); err != nil {
			return err
		}
		return bindFrontiers(tx, native, result, frontiers)
	})
	return result, err
}

// AssociateSession links actual Git-resolved context to the exact successfully recorded local segment.
func (s *Service) AssociateSession(ctx context.Context, worktree, id string, commits []string) error {
	return s.ledger.Within(ctx, func(tx Transaction) error {
		entry, found, err := tx.FindSession(id)
		if err != nil {
			return err
		}
		if !found || entry.Recording.State().Worktree != worktree {
			return domain.ErrConflict
		}
		changed, err := entry.Recording.LinkObservedCommits(commits)
		if err != nil || !changed {
			return err
		}
		if err := seal(tx, entry); err != nil {
			return err
		}
		return flushPublished(tx)
	})
}
