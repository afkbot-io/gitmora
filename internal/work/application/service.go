// Package application coordinates authorized task reads and durable evidence delivery.
package application

import (
	"context"
	"errors"
	"time"

	"workgraph.local/platform/internal/work/domain"
)

// ProviderError is safe integration health, without remote bodies, credentials or endpoint strings.
type ProviderError struct {
	Code       string
	Retryable  bool
	RetryAfter time.Duration
}

func (e *ProviderError) Error() string { return "Tasktopia request failed: " + e.Code }

// Provider uses Tasktopia's country-scoped MCP contract.
type Provider interface {
	Verify(context.Context, domain.Config) error
	Task(context.Context, domain.Config, string) (domain.Task, error)
	Publish(context.Context, domain.Config, string, string, domain.EvidenceDocument) error
	AddLink(context.Context, domain.Config, string, string, domain.MRLink) error
}

// Vault binds recoverable credentials to the immutable project identity.
type Vault interface {
	Seal(string, []byte) ([]byte, error)
	Open(string, []byte) ([]byte, error)
}

// Status exposes connection health without the bearer credential.
type Status struct {
	ProjectID   string `json:"project_id"`
	Endpoint    string `json:"endpoint"`
	CountryID   string `json:"country_id"`
	Enabled     bool   `json:"enabled"`
	Generation  int64  `json:"generation"`
	Blocked     bool   `json:"blocked"`
	ErrorCode   string `json:"error_code,omitempty"`
	AutoLinkMRs bool   `json:"auto_link_merge_requests"`
}

// TaskView distinguishes an available cached observation from missing or revoked sources.
type TaskView struct {
	Task       domain.Task `json:"task"`
	Digest     string      `json:"digest"`
	Available  bool        `json:"available"`
	ObservedAt time.Time   `json:"observed_at"`
}

// Publication tracks one immutable external write, including ambiguous retries.
type Publication struct {
	ID          string                  `json:"id"`
	ProjectID   string                  `json:"project_id"`
	TaskID      string                  `json:"task_id"`
	State       string                  `json:"state"`
	ErrorCode   string                  `json:"error_code,omitempty"`
	Document    domain.EvidenceDocument `json:"document"`
	CompletedAt *time.Time              `json:"completed_at,omitempty"`
}

// PublishInput identifies stored evidence. Clients cannot supply report text or confirmed SHAs.
type PublishInput struct {
	RepositoryID string `json:"repository_id"`
	SessionID    string `json:"session_id"`
	Revision     int64  `json:"revision"`
}

// Lease fences a single task refresh or publication against credential rotation and expired workers.
type Lease struct {
	ID, ProjectID, CountryID, TaskID, Endpoint, Owner, Kind string
	Generation                                              int64
	Envelope                                                []byte
	Document                                                domain.EvidenceDocument
	Link                                                    domain.MRLink
	ReadStartedAt                                           time.Time
}

// Outcome contains bounded scheduling and source-availability decisions.
type Outcome struct {
	Code        string
	Blocked     bool
	RetryAfter  time.Duration
	Task        *domain.Task
	Unavailable bool
}

// Store performs current membership checks for public calls and fences every worker write.
type Store interface {
	AuthorizeAdmin(context.Context, string, string) error
	Connect(context.Context, string, string, domain.Config, []byte) (Status, error)
	Status(context.Context, string, string) (Status, error)
	Disconnect(context.Context, string, string) error
	Refresh(context.Context, string, string, string) error
	Task(context.Context, string, string, string) (TaskView, error)
	QueuePublication(context.Context, string, string, PublishInput) (Publication, error)
	Publication(context.Context, string, string, string) (Publication, error)
	Links(context.Context, string, string, string) (LinkPage, error)
	Claim(context.Context) (Lease, bool, error)
	Finish(context.Context, Lease, Outcome) error
}

// Service keeps all external I/O outside local transactions.
type Service struct {
	store    Store
	vault    Vault
	provider Provider
}

// NewService injects the durable store, project-scoped encryption and MCP boundary.
func NewService(s Store, v Vault, p Provider) *Service { return &Service{s, v, p} }

// Connect verifies external country access before persisting an authorized encrypted credential.
func (s *Service) Connect(ctx context.Context, user, project string, c domain.Config) (Status, error) {
	if err := c.Validate(); err != nil {
		return Status{}, err
	}
	if err := s.store.AuthorizeAdmin(ctx, user, project); err != nil {
		return Status{}, err
	}
	if err := s.provider.Verify(ctx, c); err != nil {
		return Status{}, err
	}
	envelope, err := s.vault.Seal("tasktopia:"+project, []byte(c.Token))
	if err != nil {
		return Status{}, err
	}
	return s.store.Connect(ctx, user, project, c, envelope)
}

// Status returns credential-free health under current membership.
func (s *Service) Status(ctx context.Context, u, p string) (Status, error) {
	return s.store.Status(ctx, u, p)
}

// Disconnect destroys recoverable credentials and fences unfinished local work.
func (s *Service) Disconnect(ctx context.Context, u, p string) error {
	return s.store.Disconnect(ctx, u, p)
}

// Refresh schedules a durable read for an explicit task UUID.
func (s *Service) Refresh(ctx context.Context, u, p, t string) error {
	return s.store.Refresh(ctx, u, p, t)
}

// Task returns the available sanitized observation with exact digest and freshness time.
func (s *Service) Task(ctx context.Context, u, p, t string) (TaskView, error) {
	return s.store.Task(ctx, u, p, t)
}

// QueuePublication records an immutable report derived from authorized stored evidence.
func (s *Service) QueuePublication(ctx context.Context, u, p string, i PublishInput) (Publication, error) {
	return s.store.QueuePublication(ctx, u, p, i)
}

// Publication returns an authorized immutable report and its delivery state.
func (s *Service) Publication(ctx context.Context, u, p, id string) (Publication, error) {
	return s.store.Publication(ctx, u, p, id)
}

// ReconcileOnce processes one leased operation. Remote success can precede a lost local ACK;
// retries therefore retain the same external idempotency key and immutable report bytes.
func (s *Service) ReconcileOnce(parent context.Context) (bool, error) {
	lease, found, err := s.store.Claim(parent)
	if err != nil || !found {
		return found, err
	}
	ctx, cancel := context.WithTimeout(parent, 40*time.Second)
	defer cancel()
	out := Outcome{}
	token, e := s.vault.Open("tasktopia:"+lease.ProjectID, lease.Envelope)
	if e != nil {
		out.Code = "credential_unavailable"
		out.Blocked = true
	} else {
		config := domain.Config{Endpoint: lease.Endpoint, CountryID: lease.CountryID, Token: string(token)}
		task, requestErr := s.provider.Task(ctx, config, lease.TaskID)
		if requestErr == nil && lease.Kind == "link" {
			task, requestErr = s.ensureLink(ctx, config, lease, task)
		}
		if requestErr == nil && lease.Kind == "publication" {
			alreadyPresent := false
			for _, d := range task.Documents {
				if d.FileName == lease.Document.FileName {
					alreadyPresent = d.Content == lease.Document.Content
					if !alreadyPresent {
						requestErr = &ProviderError{Code: "publication_conflict"}
					}
				}
			}
			if requestErr == nil && !alreadyPresent {
				requestErr = s.provider.Publish(ctx, config, lease.TaskID, lease.ID, lease.Document)
			}
		}
		if requestErr == nil {
			out.Task = &task
		} else {
			out.Code = "provider_unavailable"
			out.RetryAfter = time.Minute
			var provider *ProviderError
			if errors.As(requestErr, &provider) {
				out.Code = provider.Code
				out.Blocked = !provider.Retryable
				out.RetryAfter = provider.RetryAfter
				if out.RetryAfter <= 0 {
					out.RetryAfter = time.Minute
				}
				out.Unavailable = provider.Code == "task_not_found" || provider.Code == "provider_unauthorized" || provider.Code == "country_access_denied"
			}
		}
	}
	finish, cancelFinish := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer cancelFinish()
	return true, s.store.Finish(finish, lease, out)
}
