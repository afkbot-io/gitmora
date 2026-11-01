// Package application exposes local directory use cases, independently of HTTP or PostgreSQL.
package application

import (
	"context"
	"strings"
	"time"

	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/directory/domain"
	"workgraph.local/platform/internal/endpoint"
)

// Store reauthorizes every operation and commits a command receipt atomically with its state change.
type Store interface {
	AuthorizeRepositoryCreate(context.Context, access.Principal, string) error
	Apply(context.Context, access.Principal, domain.Command) (domain.Receipt, bool, error)
	Me(context.Context, access.Principal) (domain.Me, error)
	Projects(context.Context, access.Principal, domain.Cursor) (domain.Page[domain.Project], error)
	Project(context.Context, access.Principal, string) (domain.Project, error)
	Repositories(context.Context, access.Principal, string, domain.Cursor) (domain.Page[domain.Repository], error)
	Repository(context.Context, access.Principal, string) (domain.Repository, error)
	Members(context.Context, access.Principal, string, domain.Cursor) (domain.Page[domain.Member], error)
	ResolveMember(context.Context, access.Principal, string, string) (domain.MemberCandidate, error)
	Users(context.Context, access.Principal, domain.Cursor) (domain.Page[domain.User], error)
	User(context.Context, access.Principal, string) (domain.User, error)
	Events(context.Context, access.Principal, domain.EventQuery) (domain.EventPage, error)
	Tokens(context.Context, access.Principal, domain.Cursor) (domain.Page[domain.Token], error)
	IssueToken(context.Context, access.Principal, domain.TokenInput, access.CredentialRecord) (domain.Token, bool, error)
}

// Service normalizes writes and coordinates hash-only credential issuance. Reads retain storage reauthorization.
type Service struct {
	store    Store
	now      func() time.Time
	resolver RepositoryResolver
}

func NewService(store Store, now func() time.Time) *Service { return &Service{store: store, now: now} }

// RepositoryResolver reads public provider metadata without receiving user credentials.
type RepositoryResolver interface {
	LookupRepository(context.Context, string) (domain.ResolvedRepository, error)
}

// WithRepositoryResolver installs the optional public GitHub lookup adapter during construction.
func (s *Service) WithRepositoryResolver(resolver RepositoryResolver) *Service {
	s.resolver = resolver
	return s
}

// LookupRepository authorizes current catalog administration before external I/O.
// Its resolved ID is included in the later immutable command, so uncertain retries never re-resolve it.
func (s *Service) LookupRepository(ctx context.Context, p access.Principal, project, raw string) (domain.ResolvedRepository, error) {
	url, ok := endpoint.GitHubRepository(raw)
	if !ok || !strings.HasPrefix(url, "https://github.com/") {
		return domain.ResolvedRepository{}, domain.ErrInvalid
	}
	if err := s.store.AuthorizeRepositoryCreate(ctx, p, project); err != nil {
		return domain.ResolvedRepository{}, err
	}
	if s.resolver == nil {
		return domain.ResolvedRepository{}, domain.ErrProviderUnavailable
	}
	return s.resolver.LookupRepository(ctx, url)
}
func (s *Service) Apply(ctx context.Context, p access.Principal, c domain.Command) (domain.Receipt, bool, error) {
	c, e := domain.Normalize(c)
	if e != nil {
		return domain.Receipt{}, false, e
	}
	return s.store.Apply(ctx, p, c)
}
func (s *Service) Me(ctx context.Context, p access.Principal) (domain.Me, error) {
	return s.store.Me(ctx, p)
}
func (s *Service) Projects(ctx context.Context, p access.Principal, c domain.Cursor) (domain.Page[domain.Project], error) {
	return s.store.Projects(ctx, p, c)
}
func (s *Service) Project(ctx context.Context, p access.Principal, id string) (domain.Project, error) {
	return s.store.Project(ctx, p, id)
}
func (s *Service) Repositories(ctx context.Context, p access.Principal, id string, c domain.Cursor) (domain.Page[domain.Repository], error) {
	return s.store.Repositories(ctx, p, id, c)
}
func (s *Service) Repository(ctx context.Context, p access.Principal, id string) (domain.Repository, error) {
	return s.store.Repository(ctx, p, id)
}
func (s *Service) Members(ctx context.Context, p access.Principal, id string, c domain.Cursor) (domain.Page[domain.Member], error) {
	return s.store.Members(ctx, p, id, c)
}
func (s *Service) Users(ctx context.Context, p access.Principal, c domain.Cursor) (domain.Page[domain.User], error) {
	return s.store.Users(ctx, p, c)
}
func (s *Service) User(ctx context.Context, p access.Principal, id string) (domain.User, error) {
	return s.store.User(ctx, p, id)
}
func (s *Service) Events(ctx context.Context, p access.Principal, q domain.EventQuery) (domain.EventPage, error) {
	return s.store.Events(ctx, p, q)
}
func (s *Service) Tokens(ctx context.Context, p access.Principal, c domain.Cursor) (domain.Page[domain.Token], error) {
	return s.store.Tokens(ctx, p, c)
}

// IssueToken exposes a generated bearer only after its own record is durably accepted, and only on the first delivery.
func (s *Service) IssueToken(ctx context.Context, p access.Principal, input domain.TokenInput) (domain.IssuedToken, error) {
	input, e := input.Normalize()
	if e != nil {
		return domain.IssuedToken{}, e
	}
	issued, e := access.IssueCredential(p.UserID, input.Label, input.Scopes, s.now(), time.Duration(input.TTLSeconds)*time.Second)
	if e != nil {
		return domain.IssuedToken{}, domain.ErrInvalid
	}
	token, created, e := s.store.IssueToken(ctx, p, input, issued.Record)
	if e != nil {
		return domain.IssuedToken{}, e
	}
	out := domain.IssuedToken{Token: token, Created: created}
	if created {
		if token.ID != issued.Record.ID {
			return domain.IssuedToken{}, domain.ErrConflict
		}
		out.Secret = issued.Secret
	}
	return out, nil
}

func (s *Service) ResolveMember(ctx context.Context, p access.Principal, project, input string) (domain.MemberCandidate, error) {
	return s.store.ResolveMember(ctx, p, project, input)
}

// CredentialRepositoryResolver never persists lookup credentials; connection setup seals them separately.
type CredentialRepositoryResolver interface {
	LookupRepositoryWithToken(context.Context, string, string, string) (domain.ResolvedRepository, error)
}

func (s *Service) LookupRepositoryWithToken(ctx context.Context, p access.Principal, project, raw, provider, token string) (domain.ResolvedRepository, error) {
	if provider != "github" && provider != "gitlab" {
		return domain.ResolvedRepository{}, domain.ErrInvalid
	}
	if err := s.store.AuthorizeRepositoryCreate(ctx, p, project); err != nil {
		return domain.ResolvedRepository{}, err
	}
	resolver, ok := s.resolver.(CredentialRepositoryResolver)
	if !ok {
		return domain.ResolvedRepository{}, domain.ErrProviderUnavailable
	}
	return resolver.LookupRepositoryWithToken(ctx, raw, provider, token)
}
