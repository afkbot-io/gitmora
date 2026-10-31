package application

import (
	"context"
	"errors"
	"fmt"

	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/capture/domain"
)

var (
	// ErrConflict means a revision identity was reused for different content or cannot extend its session.
	ErrConflict = errors.New("capture conflict")
	// ErrNotFound means no matching revision exists within an authorized repository.
	ErrNotFound = errors.New("capture not found")
)

// CaptureEvent is durable indexing work recorded in the same transaction as its revision.
type CaptureEvent struct {
	RepositoryID string `json:"repository_id"`
	SessionID    string `json:"session_id"`
	Revision     int64  `json:"revision"`
	Digest       string `json:"digest"`
}

// StoredRevision includes independently confirmed commits alongside client observations.
type StoredRevision struct {
	Session                 domain.Session
	ConfirmedCommits        []string
	ParentReferenceVerified bool
}

// Transaction is scoped to a repository and a locked, authorized membership.
// PutRevision returns false only for an identical retry, and conflicts on changed content.
type Transaction interface {
	PutRevision(context.Context, domain.Session) (bool, error)
	AddEvent(context.Context, CaptureEvent) error
	GetRevision(context.Context, string, int64) (StoredRevision, error)
}

// Store provides an atomic authorization/persistence boundary. Implementations must recheck
// membership inside the transaction and roll back both evidence and events on any error.
type Store interface {
	WithinAuthorized(context.Context, string, string, access.Permission, func(Transaction) error) error
}

// IngestResult distinguishes a newly committed revision from an identical retry.
type IngestResult struct {
	RepositoryID string `json:"repository_id"`
	SessionID    string `json:"session_id"`
	Revision     int64  `json:"revision"`
	Digest       string `json:"digest"`
	Created      bool   `json:"created"`
}

// Service coordinates capture validation, redaction and transactional delivery.
type Service struct{ store Store }

// NewService constructs an application service with its required transactional port.
func NewService(store Store) *Service { return &Service{store: store} }

// Ingest appends a bounded sanitized revision or acknowledges an identical retry.
func (s *Service) Ingest(ctx context.Context, subject string, input domain.Snapshot) (IngestResult, error) {
	if !domain.ValidID(subject) {
		return IngestResult{}, access.ErrDenied
	}
	session, err := domain.Sanitize(input)
	if err != nil {
		return IngestResult{}, err
	}
	sanitized := session.Snapshot()
	result := IngestResult{RepositoryID: sanitized.RepositoryID, SessionID: sanitized.SessionID, Revision: sanitized.Revision, Digest: session.Digest()}
	err = s.store.WithinAuthorized(ctx, subject, sanitized.RepositoryID, access.WriteCapture, func(tx Transaction) error {
		created, err := tx.PutRevision(ctx, session)
		if err != nil {
			return err
		}
		result.Created = created
		if !created {
			return nil
		}
		return tx.AddEvent(ctx, CaptureEvent{RepositoryID: result.RepositoryID, SessionID: result.SessionID, Revision: result.Revision, Digest: result.Digest})
	})
	if err != nil {
		return IngestResult{}, fmt.Errorf("ingest capture: %w", err)
	}
	return result, nil
}

// Get retrieves an exact revision or the latest revision when revision is zero.
func (s *Service) Get(ctx context.Context, subject, repository, sessionID string, revision int64) (StoredRevision, error) {
	if !domain.ValidID(subject) {
		return StoredRevision{}, access.ErrDenied
	}
	if !domain.ValidID(repository) || !domain.ValidID(sessionID) || revision < 0 || revision > 1_000_000_000 {
		return StoredRevision{}, domain.ErrInvalid
	}
	var stored StoredRevision
	err := s.store.WithinAuthorized(ctx, subject, repository, access.ReadCapture, func(tx Transaction) error {
		var err error
		stored, err = tx.GetRevision(ctx, sessionID, revision)
		return err
	})
	if err != nil {
		return StoredRevision{}, fmt.Errorf("get capture: %w", err)
	}
	return stored, nil
}
