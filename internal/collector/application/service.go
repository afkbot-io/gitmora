// Package application coordinates local capture through durable ledger and upload ports.
package application

import (
	"context"
	"errors"
	"time"

	capture "workgraph.local/platform/internal/capture/domain"
	"workgraph.local/platform/internal/collector/domain"
	"workgraph.local/platform/internal/identifier"
)

// RecordingEntry includes its stable local mapping key without exposing native agent IDs.
type RecordingEntry struct {
	Key       string
	Recording *domain.Recording
}

// Transaction is the local atomic recording/binding/outbox boundary.
type Transaction interface {
	Config() (domain.Config, error)
	Binding(string) (*capture.TaskRef, error)
	Find(string) (*domain.Recording, bool, error)
	NativePin(string) (string, bool, error)
	PinNative(string, string) error
	PublicationAllowed(string) (bool, error)
	ListDirty(string) ([]RecordingEntry, error)
	Observed(string, []string) ([]RecordingEntry, error)
	Linked([]string, bool) ([]RecordingEntry, error)
	Save(string, *domain.Recording) error
	EventDigest(string, string) (string, bool, error)
	RememberEvent(string, string, string) error
	Enqueue(capture.Session) error
	Eligible([]string, string) error
	Stream(string) (NativeStream, bool, error)
	NativePage(string, string) (string, bool, error)
	SaveNative(string, string, string, string, bool) error
	NativeEvent(string, string) (string, string, bool, error)
	RememberNativeEvent(string, string, string, string) error
	FindSession(string) (RecordingEntry, bool, error)
	PublishedDirty() ([]RecordingEntry, error)
	FrontierSource(string) (domain.NativeSource, bool, error)
	SaveFrontierSource(string, domain.NativeSource) error
	Frontier(string) (domain.Frontier, bool, error)
	SaveFrontier(domain.Frontier) error
	PendingFrontiers(string) ([]domain.Frontier, error)
	HeadScope(string) ([]string, bool, error)
	SaveHeadScope(string, []string) error
}

// QueuedRevision is immutable content read without holding a lock during HTTP I/O.
type QueuedRevision struct {
	Session  capture.Session
	Attempts int
}

// Stats reports local metadata only; it never includes prompts or credentials.
type Stats struct {
	Recordings int `json:"recordings"`
	Pending    int `json:"pending"`
	Eligible   int `json:"eligible"`
	Blocked    int `json:"blocked"`
}

// Ledger implements atomic local changes, exact ACKs and durable retry state.
type Ledger interface {
	Configure(context.Context, domain.Config) error
	Config(context.Context) (domain.Config, error)
	Within(context.Context, func(Transaction) error) error
	Pending(context.Context, time.Time, int) ([]QueuedRevision, error)
	Ack(context.Context, capture.Session) (bool, error)
	Fail(context.Context, capture.Session, string, bool, time.Time) error
	Retry(context.Context) error
	Stats(context.Context) (Stats, error)
}

// DeliveryError classifies a safe, bounded diagnostic without retaining server responses or URLs.
type DeliveryError struct {
	Code      string
	Retryable bool
}

func (e *DeliveryError) Error() string { return "context delivery failed: " + e.Code }

// Uploader returns success only after validating the exact immutable acknowledgement.
type Uploader interface {
	Send(context.Context, capture.Session) error
}

// Service runs local capture independently of server availability.
type Service struct {
	ledger   Ledger
	uploader Uploader
	now      func() time.Time
}

// NewService constructs a collector; uploader may be nil for capture-only operations.
func NewService(ledger Ledger, uploader Uploader, now func() time.Time) *Service {
	return &Service{ledger: ledger, uploader: uploader, now: now}
}

// Record stores a complete sanitized adapter observation and deduplicates its stable event ID.
func (s *Service) Record(ctx context.Context, worktree string, input domain.Observation) (string, error) {
	if input.History != nil {
		return "", domain.ErrInvalid
	}
	return s.record(ctx, worktree, input, false)
}

// RecordNative pins a native conversation's immutable source scope across restarts.
func (s *Service) RecordNative(ctx context.Context, worktree string, input domain.Observation) (string, error) {
	if input.History != nil {
		return s.recordNativePage(ctx, worktree, input, nil)
	}
	return s.record(ctx, worktree, input, true)
}

func (s *Service) record(ctx context.Context, worktree string, input domain.Observation, pinNative bool) (string, error) {
	observation, err := domain.Normalize(input)
	if err != nil || worktree == "" {
		return "", domain.ErrInvalid
	}
	var sessionID string
	err = s.ledger.Within(ctx, func(tx Transaction) error {
		config, err := tx.Config()
		if err != nil {
			return err
		}
		if !config.AllowsAgent(observation.Value().Agent.Name) {
			return domain.ErrDisabled
		}
		key := domain.SessionKey(config.RepositoryID, worktree, nil, observation)
		nativeKey := ""
		if pinNative {
			nativeKey = domain.SessionKey(config.RepositoryID, worktree, nil, observation)
			pinned, exists, err := tx.NativePin(nativeKey)
			if err != nil {
				return err
			}
			if exists {
				key = pinned
			}
		}
		recording, found, err := tx.Find(key)
		if err != nil {
			return err
		}
		// Retired bindings may locate an existing immutable recording, never scope a new one.
		if !found {
			legacy, e := tx.Binding(worktree)
			if e != nil {
				return e
			}
			if legacy != nil {
				legacyKey := domain.SessionKey(config.RepositoryID, worktree, legacy, observation)
				old, exists, e := tx.Find(legacyKey)
				if e != nil {
					return e
				}
				if exists {
					recording, found, key = old, true, legacyKey
				}
			}
		}
		duplicate := false
		if found {
			sessionID = recording.State().Snapshot.SessionID
			digest, exists, err := tx.EventDigest(sessionID, observation.EventKey())
			if err != nil {
				return err
			}
			if exists {
				if digest != observation.Digest() {
					return domain.ErrConflict
				}
				duplicate = true
			}
		} else {
			sessionID, err = identifier.New()
			if err != nil {
				return err
			}
		}
		fileKeys := []string{}
		if !duplicate {
			value := observation.Value()
			for _, event := range value.FileEvents {
				_, exists, err := tx.EventDigest(sessionID, event.Key())
				if err != nil {
					return err
				}
				if !exists {
					value.ChangedFiles = append(value.ChangedFiles, event.Path)
					fileKeys = append(fileKeys, event.Key())
				}
			}
			value.FileEvents = nil
			effective, err := domain.Normalize(value)
			if err != nil {
				return err
			}
			if found {
				if err := recording.Observe(effective); err != nil {
					return err
				}
			} else {
				recording, err = domain.NewRecording(config.RepositoryID, sessionID, worktree, nil, effective, s.now())
				if err != nil {
					return err
				}
			}
		}
		published := false
		if found {
			published, err = tx.PublicationAllowed(sessionID)
			if err != nil {
				return err
			}
		}
		if published && recording.State().Dirty {
			if err := seal(tx, RecordingEntry{Key: key, Recording: recording}); err != nil {
				return err
			}
		} else if !duplicate {
			if err := tx.Save(key, recording); err != nil {
				return err
			}
		}
		if pinNative {
			if err := tx.PinNative(nativeKey, key); err != nil {
				return err
			}
		}
		if duplicate {
			return nil
		}
		seen := map[string]bool{}
		for _, key := range fileKeys {
			if !seen[key] {
				if err := tx.RememberEvent(sessionID, key, key); err != nil {
					return err
				}
				seen[key] = true
			}
		}
		return tx.RememberEvent(sessionID, observation.EventKey(), observation.Digest())
	})
	if err != nil {
		return "", err
	}
	return sessionID, nil
}

// Commit associates only this worktree's observed paths and atomically seals relevant sessions.
func (s *Service) Commit(ctx context.Context, worktree, sha string, changed, remaining []string) error {
	if worktree == "" || !capture.ValidCommit(sha) {
		return domain.ErrInvalid
	}
	return s.ledger.Within(ctx, func(tx Transaction) error {
		recordings, err := tx.Observed(worktree, changed)
		if err != nil {
			return err
		}
		config, e := tx.Config()
		if e != nil {
			return e
		}

		for _, entry := range recordings {
			if !config.AllowsAgent(entry.Recording.State().Snapshot.Agent.Name) {
				continue
			}
			associated, err := entry.Recording.Associate(sha, changed, remaining)
			if err != nil {
				return err
			}
			if associated {
				if err := seal(tx, entry); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// Rewrite preserves old associations and adds the actual Git rewrite mapping across shared history.
func (s *Service) Rewrite(ctx context.Context, mapping map[string]string) error {
	oldCommits := make([]string, 0, len(mapping))
	for old, newSHA := range mapping {
		if !capture.ValidCommit(old) || !capture.ValidCommit(newSHA) {
			return domain.ErrInvalid
		}
		oldCommits = append(oldCommits, old)
	}
	return s.ledger.Within(ctx, func(tx Transaction) error {
		entries, err := tx.Linked(oldCommits, false)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			changed, err := entry.Recording.Rewrite(mapping)
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
	})
}

// PreparePush seals updates for sessions linked to reachable pushed history and schedules matching revisions.
func (s *Service) PreparePush(ctx context.Context, commits []string) error {
	if len(commits) == 0 {
		return nil
	}
	for _, sha := range commits {
		if !capture.ValidCommit(sha) {
			return domain.ErrInvalid
		}
	}
	set := make(map[string]bool, len(commits))
	for _, sha := range commits {
		set[sha] = true
	}
	return s.ledger.Within(ctx, func(tx Transaction) error {
		entries, err := tx.Linked(commits, true)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			linked := false
			for _, sha := range entry.Recording.State().Snapshot.Commits {
				if set[sha] {
					linked = true
					break
				}
			}
			if linked {
				if err := seal(tx, entry); err != nil {
					return err
				}
			}
		}
		if err := tx.Eligible(commits, ""); err != nil {
			return err
		}
		return flushPublished(tx)
	})
}

// Checkpoint explicitly seals this worktree's current recordings; all also permits uncommitted delivery.
func (s *Service) Checkpoint(ctx context.Context, worktree string, all bool) error {
	if worktree == "" {
		return domain.ErrInvalid
	}
	return s.ledger.Within(ctx, func(tx Transaction) error {
		config, e := tx.Config()
		if e != nil {
			return e
		}
		if config.Disabled {
			return domain.ErrDisabled
		}

		entries, err := tx.ListDirty(worktree)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if !config.AllowsAgent(entry.Recording.State().Snapshot.Agent.Name) {
				continue
			}
			if err := seal(tx, entry); err != nil {
				return err
			}
		}
		if all {
			return tx.Eligible(nil, worktree)
		}
		return nil
	})
}

func seal(tx Transaction, entry RecordingEntry) error {
	session, changed, err := entry.Recording.Seal()
	if err != nil {
		return err
	}
	if changed {
		if err := tx.Enqueue(session); err != nil {
			return err
		}
	}
	return tx.Save(entry.Key, entry.Recording)
}

func flushPublished(tx Transaction) error {
	entries, err := tx.PublishedDirty()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := seal(tx, entry); err != nil {
			return err
		}
	}
	return nil
}

// SyncResult reports one bounded delivery batch.
type SyncResult struct {
	Attempted int   `json:"attempted"`
	Sent      int   `json:"sent"`
	Failed    int   `json:"failed"`
	Stats     Stats `json:"queue"`
}

// Sync sends due eligible records without a local transaction spanning the network request.
func (s *Service) Sync(ctx context.Context) (SyncResult, error) {
	config, err := s.ledger.Config(ctx)
	if err != nil {
		return SyncResult{}, err
	}
	if config.Disabled {
		return SyncResult{}, domain.ErrDisabled
	}

	if s.uploader == nil {
		return SyncResult{}, errors.New("collector uploader is required")
	}
	queued, err := s.ledger.Pending(ctx, s.now(), 16)
	if err != nil {
		return SyncResult{}, err
	}
	result := SyncResult{}
	for _, entry := range queued {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		current, e := s.ledger.Config(ctx)
		if e != nil {
			return result, e
		}
		if current.Disabled {
			return result, domain.ErrDisabled
		}
		if !current.AllowsAgent(entry.Session.Snapshot().Agent.Name) {
			continue
		}

		result.Attempted++
		if err := s.uploader.Send(ctx, entry.Session); err != nil {
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			result.Failed++
			code := "unavailable"
			retry := true
			var delivery *DeliveryError
			if errors.As(err, &delivery) {
				code = delivery.Code
				retry = delivery.Retryable
			}
			delay := time.Second * time.Duration(1<<min(entry.Attempts, 8))
			if err := s.ledger.Fail(ctx, entry.Session, code, !retry, s.now().Add(delay)); err != nil {
				return result, err
			}
			continue
		}
		acknowledged, err := s.ledger.Ack(ctx, entry.Session)
		if err != nil {
			return result, err
		}
		if acknowledged {
			result.Sent++
		}
	}
	result.Stats, err = s.ledger.Stats(ctx)
	return result, err
}
