// Package application coordinates authorized provider setup, durable webhook receipt and reconciliation.
package application

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/forge/domain"
	"workgraph.local/platform/internal/identifier"
)

// Status is credential-free connection state suitable for authorized API responses.
type Status struct {
	Anonymous     bool           `json:"anonymous,omitempty"`
	Provider      string         `json:"provider,omitempty"`
	RepositoryID  string         `json:"repository_id"`
	Enabled       bool           `json:"enabled"`
	Generation    int64          `json:"generation"`
	WebhookMode   string         `json:"webhook_mode"`
	WebhookPath   string         `json:"webhook_path"`
	LastErrorCode string         `json:"last_error_code,omitempty"`
	LastSyncedAt  *time.Time     `json:"last_synced_at,omitempty"`
	NextSyncAt    time.Time      `json:"next_sync_at"`
	Blocked       bool           `json:"blocked"`
	Streams       []StreamStatus `json:"streams"`
}

// Connection contains an encrypted envelope only inside the trusted worker/authentication boundary.
type Connection struct {
	Repository domain.Repository
	Generation int64
	Envelope   []byte
}

// Lease fences workers against expired ownership, disabled connections and credential rotation.
type Lease struct {
	Connection
	Owner     string
	Requested int64
}

// LeaseDuration exceeds the bounded provider batch plus its durable completion allowance.
const LeaseDuration = 60 * time.Second

const batchSize = 16

// Outcome contains bounded diagnostics and scheduling decisions, never raw provider errors.
type Outcome struct {
	More       bool
	Code       string
	Blocked    bool
	RetryAfter time.Duration
}

// Store must recheck membership in each public operation and fence every worker write transaction.
type Store interface {
	MetadataStore
	RepositoryForAdmin(context.Context, string, string) (domain.Repository, error)
	Connect(context.Context, string, domain.Repository, []byte, string, bool, *Connection) (Status, error)
	Credentials(context.Context, string, string, string) (CredentialPage, error)
	Credential(context.Context, string, string, CredentialRef) (Connection, error)
	CredentialForTarget(context.Context, string, string, CredentialRef) (Connection, error)
	Status(context.Context, string, string) (Status, error)
	Disconnect(context.Context, string, string) error
	RequestSync(context.Context, string, string) error
	WebhookConnection(context.Context, string) (Connection, error)
	Accept(context.Context, Connection, domain.Event) (bool, error)
	Claim(context.Context) (Lease, bool, error)
	Pending(context.Context, Lease, int) ([]string, error)
	MarkCommit(context.Context, Lease, string, bool) error
	Finish(context.Context, Lease, Outcome) error
}

// Vault binds recoverable credentials to immutable repository scope.
type Vault interface {
	Seal(string, []byte) ([]byte, error)
	Open(string, []byte) ([]byte, error)
}

// Provider verifies real provider identity; webhook/client claims never implement this port.
type Provider interface {
	MetadataProvider
	VerifyProject(context.Context, domain.Repository, string) error
	ConfirmCommit(context.Context, domain.Repository, string, string) (bool, error)
}

// ProviderError classifies a bounded integration health state without exposing response content.
type ProviderError struct {
	Code       string
	Retryable  bool
	RetryAfter time.Duration
}

func (e *ProviderError) Error() string { return "Forge request failed: " + e.Code }

// Service keeps provider I/O outside local database transactions.
type Service struct {
	store              Store
	vault              Vault
	provider           Provider
	now                func() time.Time
	credentialResolver CredentialResolver
}

// NewService supplies the durable store, encryption boundary, provider adapter and clock.
func NewService(store Store, vault Vault, provider Provider, now func() time.Time) *Service {
	return &Service{store: store, vault: vault, provider: provider, now: now}
}

// Connect verifies a token against the configured immutable project before encrypted persistence.
func (s *Service) Connect(ctx context.Context, subject, repository string, secrets domain.Secrets) (Status, error) {
	if !identifier.Valid(subject) || !identifier.Valid(repository) {
		return Status{}, access.ErrDenied
	}
	if err := secrets.Validate(); err != nil {
		return Status{}, err
	}
	repo, err := s.store.RepositoryForAdmin(ctx, subject, repository)
	if err != nil {
		return Status{}, err
	}
	return s.connect(ctx, subject, repo, secrets, nil)
}

func (s *Service) connect(ctx context.Context, subject string, repo domain.Repository, secrets domain.Secrets, source *Connection) (Status, error) {
	if err := secrets.ValidateForProvider(repo.Provider); err != nil {
		return Status{}, err
	}
	if err := s.provider.VerifyProject(ctx, repo, secrets.APIToken); err != nil {
		return Status{}, err
	}
	raw, err := json.Marshal(secrets)
	if err != nil {
		return Status{}, domain.ErrInvalid
	}
	envelope, err := s.vault.Seal(repo.CredentialScope(), raw)
	if err != nil {
		return Status{}, err
	}
	return s.store.Connect(ctx, subject, repo, envelope, secrets.Mode(), secrets.Anonymous, source)
}

// Status returns only authorized metadata and integration health.
func (s *Service) Status(ctx context.Context, subject, repository string) (Status, error) {
	return s.store.Status(ctx, subject, repository)
}

// Disconnect removes stored recoverable credentials and invalidates active worker leases.
func (s *Service) Disconnect(ctx context.Context, subject, repository string) error {
	return s.store.Disconnect(ctx, subject, repository)
}

// RequestSync explicitly retries a connection, including a previously blocked credential failure.
func (s *Service) RequestSync(ctx context.Context, subject, repository string) error {
	return s.store.RequestSync(ctx, subject, repository)
}

func (s *Service) secrets(connection Connection) (domain.Secrets, error) {
	raw, err := s.vault.Open(connection.Repository.CredentialScope(), connection.Envelope)
	if err != nil {
		return domain.Secrets{}, err
	}
	var secrets domain.Secrets
	if json.Unmarshal(raw, &secrets) != nil || secrets.ValidateForProvider(connection.Repository.Provider) != nil {
		return domain.Secrets{}, domain.ErrInvalid
	}
	return secrets, nil
}

// Receive authenticates the exact body before persisting a minimal, idempotent inbox hint.
func (s *Service) Receive(ctx context.Context, repository string, delivery domain.Delivery) (bool, error) {
	if !identifier.Valid(repository) {
		return false, domain.ErrUnauthenticated
	}
	connection, err := s.store.WebhookConnection(ctx, repository)
	if errors.Is(err, domain.ErrNotFound) {
		return false, domain.ErrUnauthenticated
	}
	if err != nil {
		return false, err
	}
	secrets, err := s.secrets(connection)
	if err != nil {
		return false, err
	}
	if err := domain.AuthenticateDelivery(secrets, delivery, s.now()); err != nil {
		return false, err
	}
	event, err := domain.ParseEvent(delivery, connection.Repository.ProjectID)
	if err != nil {
		return false, err
	}
	return s.store.Accept(ctx, connection, event)
}

// ReconcileOnce claims one durable connection batch. Periodic due work also runs without a webhook.
func (s *Service) ReconcileOnce(parent context.Context) (bool, error) {
	lease, found, err := s.store.Claim(parent)
	if err != nil || !found {
		return found, err
	}
	ctx, cancel := context.WithTimeout(parent, LeaseDuration-15*time.Second)
	defer cancel()
	outcome := Outcome{}
	err = s.reconcile(ctx, lease, &outcome)
	if err != nil {
		outcome.Code = "provider_unavailable"
		var provider *ProviderError
		if errors.As(err, &provider) {
			outcome.Code = provider.Code
			outcome.Blocked = !provider.Retryable
			outcome.RetryAfter = provider.RetryAfter
		}
	}
	cleanup, cancelCleanup := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer cancelCleanup()
	if finishErr := s.store.Finish(cleanup, lease, outcome); finishErr != nil {
		return true, finishErr
	}
	return true, err
}
func (s *Service) reconcile(ctx context.Context, lease Lease, outcome *Outcome) error {
	secrets, err := s.secrets(lease.Connection)
	if err != nil {
		outcome.Code = "credential_unavailable"
		outcome.Blocked = true
		return &ProviderError{Code: outcome.Code}
	}
	if err := s.provider.VerifyProject(ctx, lease.Repository, secrets.APIToken); err != nil {
		return err
	}
	more, err := s.metadata(ctx, lease, secrets.APIToken)
	if err != nil {
		return err
	}
	pending, err := s.store.Pending(ctx, lease, batchSize)
	if err != nil {
		return err
	}
	outcome.More = more || len(pending) == batchSize
	for _, sha := range pending {
		if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < 12*time.Second {
			outcome.More = true
			return nil
		}
		confirmed, err := s.provider.ConfirmCommit(ctx, lease.Repository, secrets.APIToken, sha)
		if err != nil {
			return err
		}
		if err := s.store.MarkCommit(ctx, lease, sha, confirmed); err != nil {
			return err
		}
	}
	return nil
}
