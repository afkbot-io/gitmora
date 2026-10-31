package application_test

import (
	"context"
	"strings"
	"testing"
	"time"
	"workgraph.local/platform/internal/access"

	"workgraph.local/platform/internal/identity/application"
	"workgraph.local/platform/internal/identity/domain"
)

type failedStore struct {
	application.Store
	consume bool
	resolve bool
}

type refreshedStore struct {
	application.Store
	binding   domain.Binding
	session   domain.Session
	refreshes int
	opens     int
}

func (s *refreshedStore) ConsumeFlow(context.Context, domain.Flow) error { return nil }
func (s *refreshedStore) Resolve(context.Context, domain.Identity) (domain.Binding, error) {
	return s.binding, nil
}
func (s *refreshedStore) ReauthenticateSession(_ context.Context, cookie, configuration string, b domain.Binding) (domain.Session, error) {
	s.refreshes++
	if cookie == "" || configuration == "" || b != s.binding {
		panic("invalid refresh")
	}
	return s.session, nil
}
func (s *refreshedStore) OpenSession(context.Context, domain.SessionSeed) (domain.Session, error) {
	s.opens++
	return domain.Session{}, domain.ErrLogin
}

func TestCLIFreshOIDCDoesNotReplaceOrExtendAnExistingSameAccountBrowserSession(t *testing.T) {
	s := &refreshedStore{binding: domain.Binding{ID: "11111111-1111-4111-8111-111111111111", UserID: "22222222-2222-4222-8222-222222222222", Issuer: "https://idp.internal", Subject: "unknown", Enabled: true, Version: 1}}
	s.session = domain.Session{ID: "33333333-3333-4333-8333-333333333333", BindingID: s.binding.ID, Principal: access.Principal{UserID: s.binding.UserID, TokenID: "44444444-4444-4444-8444-444444444444"}, ExpiresAt: time.Now().Add(time.Hour)}
	p := &provider{}
	service := application.NewService(s, p, domain.Config{Issuer: s.binding.Issuer, Policy: domain.Policy, PublicURL: "https://workgraph.internal"}, time.Now)
	state, _ := domain.NewSecret(domain.StatePrefix)
	flow, _ := domain.NewSecret(domain.FlowPrefix)
	cookie, _ := domain.NewSecret(domain.SessionPrefix)
	result, e := service.FinishCLI(context.Background(), state, flow, "provider-code", cookie)
	if e != nil || !result.ReusedSession || result.Session.ID != s.session.ID || result.Session.ExpiresAt != s.session.ExpiresAt || s.opens != 0 || s.refreshes != 1 || p.calls != 1 {
		t.Fatal("CLI login replaced browser grant", e, s.opens, s.refreshes)
	}
	if result.Cookie != "" || result.CSRF != "" || strings.Contains(result.Session.ID, cookie) {
		t.Fatal("reused session exposed cookie material")
	}
}

func (s *failedStore) ConsumeFlow(context.Context, domain.Flow) error {
	if !s.consume {
		return domain.ErrLogin
	}
	return nil
}
func (s *failedStore) Resolve(context.Context, domain.Identity) (domain.Binding, error) {
	s.resolve = true
	return domain.Binding{}, domain.ErrLogin
}

type provider struct{ calls int }

func (p *provider) AuthorizationURL(context.Context, string, string, string) (string, error) {
	return "", nil
}
func (p *provider) Exchange(context.Context, string, string, string) (domain.Identity, error) {
	p.calls++
	return domain.Identity{Issuer: "https://idp.internal", Subject: "unknown"}, nil
}
func TestNoExchangeBeforeFlowConsumptionOrSessionForUnknownIdentity(t *testing.T) {
	store := &failedStore{}
	idp := &provider{}
	service := application.NewService(store, idp, domain.Config{Issuer: "https://idp.internal"}, time.Now)
	state, _ := domain.NewSecret(domain.StatePrefix)
	browser, _ := domain.NewSecret(domain.FlowPrefix)
	login, e := service.Finish(context.Background(), state, browser, "code", "")
	if e == nil || idp.calls != 0 || login.Cookie != "" {
		t.Fatal("unconsumed flow exchanged", idp.calls, e)
	}
	store.consume = true
	login, e = service.Finish(context.Background(), state, browser, "code", "")
	if e == nil || idp.calls != 1 || !store.resolve || login.Cookie != "" {
		t.Fatal("unknown identity gained session", e)
	}
}

type signupStore struct {
	failedStore
	registered int
}

func (s *signupStore) Register(context.Context, domain.Identity, string) (domain.Binding, error) {
	s.registered++
	return domain.Binding{}, domain.ErrLogin
}
func TestSignupPolicyGatesUnknownVerifiedIdentity(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		s := &signupStore{failedStore: failedStore{consume: true}}
		svc := application.NewService(s, &provider{}, domain.Config{Issuer: "https://idp.internal", RegistrationEnabled: enabled}, time.Now)
		state, _ := domain.NewSecret(domain.StatePrefix)
		browser, _ := domain.NewSecret(domain.FlowPrefix)
		if _, e := svc.Finish(context.Background(), state, browser, "provider-code", ""); e == nil {
			t.Fatal("unprovisioned identity gained session")
		}
		expected := 0
		if enabled {
			expected = 1
		}
		if s.registered != expected {
			t.Fatal("signup policy bypass", enabled, s.registered)
		}
		if !enabled {
			if _, _, e := svc.StartRegistration(context.Background()); e == nil {
				t.Fatal("disabled registration started")
			}
		}
	}
}
