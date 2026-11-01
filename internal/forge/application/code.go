package application

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"workgraph.local/platform/internal/forge/domain"
)

// CodeWork identifies one connection-fenced diff page, raw-file read or permission probe.
type CodeWork struct {
	Connection
	Owner, SHA, Path string
	Page             int
	Version          int64
	Probe            bool
}

// CodeOutcome contains exactly one bounded unit result and safe retry diagnostics.
type CodeOutcome struct {
	Diff       *domain.DiffPage
	File       *domain.FileObservation
	Code       string
	Blocked    bool
	RetryAfter time.Duration
}

// CodeStatus reports discovery/retention gaps without presenting diff enumeration as complete repository coverage.
type CodeStatus struct {
	RepositoryID        string         `json:"repository_id"`
	CommitSHA           string         `json:"commit_sha"`
	DiscoveryEnded      bool           `json:"discovery_ended"`
	LocalLimit          bool           `json:"local_path_limit_reached"`
	Enabled             bool           `json:"enabled"`
	ProviderAvailable   bool           `json:"provider_available"`
	Pending             int            `json:"pending"`
	Stored              int            `json:"stored"`
	Skipped             int            `json:"skipped"`
	Missing             int            `json:"missing"`
	SkipReasons         map[string]int `json:"skip_reasons"`
	LastError           string         `json:"last_error_code,omitempty"`
	Blocked             bool           `json:"blocked"`
	PermissionCheckedAt *time.Time     `json:"permission_checked_at,omitempty"`
	DiffLimitsApply     bool           `json:"provider_diff_limits_apply"`
}

// CodeFileView is a locally authorized immutable sanitized projection, independent of branch freshness.
type CodeFileView struct {
	ProjectID  string          `json:"project_id"`
	File       domain.CodeFile `json:"file"`
	Digest     string          `json:"digest"`
	ObservedAt time.Time       `json:"observed_at"`
}

// CodeQuote expands a file by bounded rune range without claiming current branch/head applicability.
type CodeQuote struct {
	ProjectID  string              `json:"project_id"`
	Identity   domain.FileIdentity `json:"identity"`
	Digest     string              `json:"digest"`
	RawSHA256  string              `json:"raw_sha256"`
	Policy     string              `json:"redaction_policy"`
	Redacted   bool                `json:"redacted"`
	ObservedAt time.Time           `json:"observed_at"`
	domain.CodeRange
}

// CodeStore owns durable code ingestion and checks current scope/credential generations on every completion/read.
type CodeStore interface {
	ClaimCode(context.Context) (CodeWork, bool, error)
	FinishCode(context.Context, CodeWork, CodeOutcome) error
	CodeStatus(context.Context, string, string, string) (CodeStatus, error)
	ReadCode(context.Context, string, string, string, string) (CodeFileView, error)
	RetryCode(context.Context, string, string, string) error
}

// CodeProvider reads provider-reported paths and exact raw blobs, never executing repository code.
type CodeProvider interface {
	CodeDiff(context.Context, domain.Repository, string, string, int) (domain.DiffPage, error)
	CodeFile(context.Context, domain.Repository, string, string, string) (domain.FileObservation, error)
}

// CodeService operates independently from MR synchronization while sharing the encrypted connection authority.
type CodeService struct {
	store    CodeStore
	vault    Vault
	provider CodeProvider
}

// NewCodeService injects the fenced queue/read store, scoped vault and provider client.
func NewCodeService(store CodeStore, vault Vault, provider CodeProvider) *CodeService {
	return &CodeService{store, vault, provider}
}

// Status returns scoped ingestion progress and permission health.
func (s *CodeService) Status(ctx context.Context, user, repo, sha string) (CodeStatus, error) {
	return s.store.CodeStatus(ctx, user, repo, sha)
}

// File retrieves retained sanitized bytes only after current local/provider scope checks.
func (s *CodeService) File(ctx context.Context, user, repo, sha, path string) (CodeFileView, error) {
	return s.store.ReadCode(ctx, user, repo, sha, path)
}

// Quote reauthorizes a pinned file and reveals only the requested compact source range.
func (s *CodeService) Quote(ctx context.Context, user, repo, sha, path string, offset, limit int) (CodeQuote, error) {
	if offset < 0 || offset > 1024*1024 || limit < 0 || limit > 2048 {
		return CodeQuote{}, domain.ErrInvalid
	}
	view, err := s.store.ReadCode(ctx, user, repo, sha, path)
	if err != nil {
		return CodeQuote{}, err
	}
	quoted, err := domain.SliceCode(view.File.Content, offset, limit)
	if err != nil {
		return CodeQuote{}, err
	}
	return CodeQuote{ProjectID: view.ProjectID, Identity: view.File.Identity, Digest: view.Digest, RawSHA256: view.File.RawSHA256, Policy: view.File.Policy, Redacted: view.File.Redacted, ObservedAt: view.ObservedAt, CodeRange: quoted}, nil
}

// Retry explicitly repairs a confirmed commit's enumeration without destroying immutable observations.
func (s *CodeService) Retry(ctx context.Context, user, repo, sha string) error {
	return s.store.RetryCode(ctx, user, repo, sha)
}

// ReconcileOnce makes provider I/O outside transactions and durably records one bounded unit.
func (s *CodeService) ReconcileOnce(parent context.Context) (bool, error) {
	work, found, err := s.store.ClaimCode(parent)
	if err != nil || !found {
		return found, err
	}
	ctx, cancel := context.WithTimeout(parent, 40*time.Second)
	defer cancel()
	out := CodeOutcome{}
	raw, e := s.vault.Open(work.Repository.CredentialScope(), work.Envelope)
	var secrets domain.Secrets
	if e != nil || json.Unmarshal(raw, &secrets) != nil || secrets.ValidateForProvider(work.Repository.Provider) != nil {
		out.Code = "credential_unavailable"
		out.Blocked = true
	} else {
		if work.Path == "" {
			var page domain.DiffPage
			page, err = s.provider.CodeDiff(ctx, work.Repository, secrets.APIToken, work.SHA, work.Page)
			if err == nil {
				out.Diff = &page
			}
		} else {
			var file domain.FileObservation
			file, err = s.provider.CodeFile(ctx, work.Repository, secrets.APIToken, work.SHA, work.Path)
			if err == nil {
				out.File = &file
			}
		}
		if err != nil {
			out.Code = "provider_unavailable"
			out.RetryAfter = time.Minute
			var failure *ProviderError
			if errors.As(err, &failure) {
				out.Code = failure.Code
				out.Blocked = !failure.Retryable
				out.RetryAfter = failure.RetryAfter
			}
		}
	}
	finish, cancelFinish := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
	defer cancelFinish()
	if err := s.store.FinishCode(finish, work, out); err != nil {
		return true, err
	}
	return true, err
}
