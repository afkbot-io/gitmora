package application

import (
	"context"

	directory "workgraph.local/platform/internal/directory/domain"
	"workgraph.local/platform/internal/forge/domain"
	"workgraph.local/platform/internal/identifier"
)

// CredentialRef selects an exact saved connection generation, never secret bytes.
type CredentialRef struct {
	RepositoryID string `json:"repository_id"`
	Generation   int64  `json:"generation"`
}

// Valid rejects incomplete selections before database or provider work.
func (r CredentialRef) Valid() bool { return identifier.Valid(r.RepositoryID) && r.Generation > 0 }

// CredentialOption contains only project-authorized labels for a reusable API token.
type CredentialOption struct {
	CredentialRef
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Endpoint string `json:"endpoint"`
}

// CredentialPage bounds metadata discovery independently of the repository catalog.
type CredentialPage struct {
	Items  []CredentialOption `json:"items"`
	NextID string             `json:"next_id,omitempty"`
}

// CredentialResolver selects the configured endpoint before receiving saved secret material.
type CredentialResolver interface {
	CredentialEndpoint(string, string) (string, error)
	LookupRepositoryWithToken(context.Context, string, string, string) (directory.ResolvedRepository, error)
}

// WithCredentialResolver installs immutable endpoint routing during service construction.
func (s *Service) WithCredentialResolver(resolver CredentialResolver) *Service {
	s.credentialResolver = resolver
	return s
}

// Credentials exposes selectable labels only to current project integration administrators.
func (s *Service) Credentials(ctx context.Context, subject, project, after string) (CredentialPage, error) {
	if !identifier.Valid(project) || after != "" && !identifier.Valid(after) {
		return CredentialPage{}, domain.ErrInvalid
	}
	return s.store.Credentials(ctx, subject, project, after)
}

// LookupWithCredential never releases a token to the browser or to another configured endpoint.
func (s *Service) LookupWithCredential(ctx context.Context, subject, project, raw, provider string, ref CredentialRef) (directory.ResolvedRepository, error) {
	if !ref.Valid() || (provider != "github" && provider != "gitlab") {
		return directory.ResolvedRepository{}, domain.ErrInvalid
	}
	connection, err := s.store.Credential(ctx, subject, project, ref)
	if err != nil {
		return directory.ResolvedRepository{}, err
	}
	if s.credentialResolver == nil {
		return directory.ResolvedRepository{}, domain.ErrInvalid
	}
	endpoint, err := s.credentialResolver.CredentialEndpoint(raw, provider)
	if err != nil {
		return directory.ResolvedRepository{}, err
	}
	expected := ""
	if provider == "github" {
		expected = "github"
	}
	if connection.Repository.Provider != expected || endpoint != connection.Repository.CredentialEndpoint() {
		return directory.ResolvedRepository{}, domain.ErrCredentialEndpoint
	}
	secrets, err := s.secrets(connection)
	if err != nil {
		return directory.ResolvedRepository{}, err
	}
	result, err := s.credentialResolver.LookupRepositoryWithToken(ctx, raw, provider, secrets.APIToken)
	if err != nil {
		return directory.ResolvedRepository{}, err
	}
	// Rotation, disconnect and membership revocation invalidate a lookup already in flight.
	if _, err = s.store.Credential(ctx, subject, project, ref); err != nil {
		return directory.ResolvedRepository{}, err
	}
	return result, nil
}

// ConnectUsingCredential copies only the API token; webhook secrets remain repository-specific.
// The target is verified independently and storage atomically fences the selected source generation.
func (s *Service) ConnectUsingCredential(ctx context.Context, subject, repository string, ref CredentialRef, secrets domain.Secrets) (Status, error) {
	if !ref.Valid() || secrets.APIToken != "" || secrets.Anonymous {
		return Status{}, domain.ErrInvalid
	}
	repo, err := s.store.RepositoryForAdmin(ctx, subject, repository)
	if err != nil {
		return Status{}, err
	}
	connection, err := s.store.CredentialForTarget(ctx, subject, repository, ref)
	if err != nil {
		return Status{}, err
	}
	if repo.Provider != connection.Repository.Provider || repo.CredentialEndpoint() != connection.Repository.CredentialEndpoint() {
		return Status{}, domain.ErrCredentialEndpoint
	}
	saved, err := s.secrets(connection)
	if err != nil {
		return Status{}, err
	}
	secrets.APIToken = saved.APIToken
	return s.connect(ctx, subject, repo, secrets, &connection)
}
