// Package application coordinates one-use OIDC login and hash-only local sessions without provider role assignment.
package application

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"workgraph.local/platform/internal/access"
	directory "workgraph.local/platform/internal/directory/domain"
	"workgraph.local/platform/internal/identifier"
	"workgraph.local/platform/internal/identity/domain"
)

type Store interface {
	CreateFlow(context.Context, domain.Flow) error
	ConsumeFlow(context.Context, domain.Flow) error
	Resolve(context.Context, domain.Identity) (domain.Binding, error)
	Register(context.Context, domain.Identity, string) (domain.Binding, error)
	OpenSession(context.Context, domain.SessionSeed) (domain.Session, error)
	ReauthenticateSession(context.Context, string, string, domain.Binding) (domain.Session, error)
	Session(context.Context, string, string) (domain.Session, error)
	Logout(context.Context, string, string) error
	Bind(context.Context, access.Principal, string, domain.BindInput) (directory.Receipt, bool, error)
	Bindings(context.Context, access.Principal, string, directory.Cursor) (directory.Page[domain.Binding], error)
	ChangeBinding(context.Context, access.Principal, string, string, domain.BindingChange) (directory.Receipt, bool, error)
}

// Provider verifies the code, signature/issuer/audience/time/nonce and returns only an external identity.
type Provider interface {
	AuthorizationURL(context.Context, string, string, string) (string, error)
	Exchange(context.Context, string, string, string) (domain.Identity, error)
}
type Service struct {
	store    Store
	provider Provider
	config   domain.Config
	now      func() time.Time
}

func NewService(store Store, provider Provider, config domain.Config, now func() time.Time) *Service {
	return &Service{store, provider, config, now}
}
func (s *Service) Config() domain.Config { return s.config }

// Start creates a browser-specific flow after provider metadata is available; only hashes enter storage.
func (s *Service) Start(ctx context.Context) (authorization, browser string, err error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	state, e := domain.NewSecret(domain.StatePrefix)
	if e != nil {
		return "", "", e
	}
	browser, e = domain.NewSecret(domain.FlowPrefix)
	if e != nil {
		return "", "", e
	}
	verifier, nonce, e := domain.Proof(state, browser)
	if e != nil {
		return "", "", e
	}
	authorization, e = s.provider.AuthorizationURL(ctx, state, verifier, nonce)
	if e != nil {
		return "", "", e
	}
	if e = s.store.CreateFlow(ctx, domain.Flow{StateHash: domain.SecretHash(domain.StatePrefix, state), BrowserHash: domain.SecretHash(domain.FlowPrefix, browser), Configuration: s.config.Fingerprint()}); e != nil {
		return "", "", e
	}
	return authorization, browser, nil
}

// StartRegistration uses the same one-use PKCE/nonce flow with the hosted provider's registration entry.
func (s *Service) StartRegistration(ctx context.Context) (string, string, error) {
	if !s.config.RegistrationEnabled {
		return "", "", domain.ErrLogin
	}
	authorization, browser, e := s.Start(ctx)
	if e != nil {
		return "", "", e
	}
	u, e := url.Parse(authorization)
	if e != nil || !strings.HasSuffix(u.Path, "/protocol/openid-connect/auth") {
		return "", "", domain.ErrProvider
	}
	u.Path = strings.TrimSuffix(u.Path, "/auth") + "/registrations"
	return u.String(), browser, nil
}

func (s *Service) consume(ctx context.Context, state, browser string) error {
	if _, _, e := domain.Proof(state, browser); e != nil {
		return e
	}
	return s.store.ConsumeFlow(ctx, domain.Flow{StateHash: domain.SecretHash(domain.StatePrefix, state), BrowserHash: domain.SecretHash(domain.FlowPrefix, browser), Configuration: s.config.Fingerprint()})
}
func (s *Service) Cancel(ctx context.Context, state, browser string) error {
	return s.consume(ctx, state, browser)
}

// Finish consumes the browser-bound flow before exchange, then rechecks the exact local mapping before publication.
func (s *Service) Finish(ctx context.Context, state, browser, code, oldCookie string) (domain.Login, error) {
	return s.finish(ctx, state, browser, code, oldCookie, false)
}

// FinishCLI verifies a fresh OIDC flow while preserving a same-account browser grant and its original deadline.
func (s *Service) FinishCLI(ctx context.Context, state, browser, code, oldCookie string) (domain.Login, error) {
	return s.finish(ctx, state, browser, code, oldCookie, true)
}

func (s *Service) finish(ctx context.Context, state, browser, code, oldCookie string, cli bool) (domain.Login, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	verifier, nonce, e := domain.Proof(state, browser)
	if e != nil {
		return domain.Login{}, e
	}
	if e = s.consume(ctx, state, browser); e != nil {
		return domain.Login{}, e
	}
	identity, e := s.provider.Exchange(ctx, code, verifier, nonce)
	if e != nil {
		return domain.Login{}, e
	}
	if identity.Issuer != s.config.Issuer || !domain.Subject(identity.Subject) {
		return domain.Login{}, domain.ErrLogin
	}
	binding, e := s.store.Resolve(ctx, identity)
	if errors.Is(e, domain.ErrLogin) && s.config.RegistrationEnabled {
		binding, e = s.store.Register(ctx, identity, s.config.Fingerprint())
	}
	if e != nil {
		return domain.Login{}, e
	}
	if cli {
		if oldHash := domain.SecretHash(domain.SessionPrefix, oldCookie); oldHash != "" {
			existing, e := s.store.ReauthenticateSession(ctx, oldHash, s.config.Fingerprint(), binding)
			if e == nil {
				if existing.Principal.UserID != binding.UserID || existing.BindingID != binding.ID || !identifier.Valid(existing.ID) || !identifier.Valid(existing.Principal.TokenID) {
					return domain.Login{}, domain.ErrLogin
				}
				return domain.Login{Session: existing, ReusedSession: true}, nil
			}
			if !errors.Is(e, access.ErrUnauthenticated) {
				return domain.Login{}, e
			}
		}
	}
	cookie, e := domain.NewSecret(domain.SessionPrefix)
	if e != nil {
		return domain.Login{}, e
	}
	csrf, e := domain.NewSecret(domain.CSRFPrefix)
	if e != nil {
		return domain.Login{}, e
	}
	id, e := identifier.New()
	if e != nil {
		return domain.Login{}, e
	}
	issued, e := access.IssueCredential(binding.UserID, "Browser session", domain.InteractiveScopes(), s.now(), time.Duration(s.config.SessionSeconds)*time.Second)
	if e != nil {
		return domain.Login{}, e
	}
	session, e := s.store.OpenSession(ctx, domain.SessionSeed{Username: identity.Username, ID: id, CookieHash: domain.SecretHash(domain.SessionPrefix, cookie), CSRFHash: domain.SecretHash(domain.CSRFPrefix, csrf), Configuration: s.config.Fingerprint(), Binding: binding, Credential: issued.Record, OldCookieHash: domain.SecretHash(domain.SessionPrefix, oldCookie)})
	if e != nil {
		return domain.Login{}, e
	}
	if session.ID != id || session.Principal.TokenID != issued.Record.ID || session.Principal.UserID != binding.UserID {
		return domain.Login{}, domain.ErrLogin
	}
	return domain.Login{Session: session, Cookie: cookie, CSRF: csrf}, nil
}
func (s *Service) Authenticate(ctx context.Context, cookie string) (domain.Session, error) {
	hash := domain.SecretHash(domain.SessionPrefix, cookie)
	if hash == "" {
		return domain.Session{}, access.ErrUnauthenticated
	}
	return s.store.Session(ctx, hash, s.config.Fingerprint())
}
func (s *Service) Logout(ctx context.Context, cookie string) error {
	hash := domain.SecretHash(domain.SessionPrefix, cookie)
	if hash == "" {
		return access.ErrUnauthenticated
	}
	return s.store.Logout(ctx, hash, s.config.Fingerprint())
}
func (s *Service) Bind(ctx context.Context, p access.Principal, i domain.BindInput) (directory.Receipt, bool, error) {
	if e := i.Validate(); e != nil {
		return directory.Receipt{}, false, e
	}
	return s.store.Bind(ctx, p, s.config.Issuer, i)
}
func (s *Service) Bindings(ctx context.Context, p access.Principal, c directory.Cursor) (directory.Page[domain.Binding], error) {
	return s.store.Bindings(ctx, p, s.config.Issuer, c)
}
func (s *Service) ChangeBinding(ctx context.Context, p access.Principal, id string, c domain.BindingChange) (directory.Receipt, bool, error) {
	return s.store.ChangeBinding(ctx, p, s.config.Issuer, id, c)
}
