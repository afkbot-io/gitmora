package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/identity/domain"
	"workgraph.local/platform/internal/transport/httpapi"
)

type identityFixture struct {
	httpapi.IdentityService
	cookie, csrf string
	session      domain.Session
	logout       bool
}

func (i *identityFixture) Config() domain.Config {
	return domain.Config{Issuer: "https://idp.internal", ClientID: "fixture", PublicURL: "https://workgraph.internal", AuthStyle: "basic", SessionSeconds: 28800, Policy: domain.Policy}
}
func (i *identityFixture) Authenticate(_ context.Context, cookie string) (domain.Session, error) {
	if cookie != i.cookie {
		return domain.Session{}, access.ErrUnauthenticated
	}
	return i.session, nil
}
func (i *identityFixture) Logout(context.Context, string) error { i.logout = true; return nil }
func (i *identityFixture) Start(context.Context) (string, string, error) {
	v, _ := domain.NewSecret(domain.FlowPrefix)
	return "https://idp.internal/authorize?state=fixture", v, nil
}
func (i *identityFixture) Finish(context.Context, string, string, string, string) (domain.Login, error) {
	return domain.Login{Session: i.session, Cookie: i.cookie, CSRF: i.csrf}, nil
}

func (i *identityFixture) FinishCLI(ctx context.Context, state, browser, code, old string) (domain.Login, error) {
	return i.Finish(ctx, state, browser, code, old)
}

func TestCookieAuthenticationRequiresCSRFAndCannotReachMCPAsBearer(t *testing.T) {
	cookie, _ := domain.NewSecret(domain.SessionPrefix)
	csrf, _ := domain.NewSecret(domain.CSRFPrefix)
	id := &identityFixture{cookie: cookie, csrf: csrf, session: domain.Session{ID: "11111111-1111-4111-8111-111111111111", Principal: access.Principal{UserID: "33333333-3333-4333-8333-333333333333", Scopes: []access.Permission{access.ReadCapture, access.WriteCapture}}, CSRFHash: domain.SecretHash(domain.CSRFPrefix, csrf), ExpiresAt: time.Now().Add(time.Hour)}}
	captures := &capture{created: true}
	h := httpapi.NewHandler(httpapi.Config{Captures: captures, Authenticator: auth{scopes: []access.Permission{access.ReadCapture}}, Ready: func(context.Context) error { return nil }, Identity: id})
	sessionName, csrfName, _ := id.Config().CookieNames()
	body, _ := json.Marshal(input())
	call := func(method, path, origin, proof, bearer string, duplicate bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://workgraph.internal"+path, strings.NewReader(string(body)))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: sessionName, Value: cookie})
		r.AddCookie(&http.Cookie{Name: csrfName, Value: csrf})
		if duplicate {
			r.AddCookie(&http.Cookie{Name: sessionName, Value: cookie})
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if proof != "" {
			r.Header.Set("X-Workgraph-CSRF", proof)
		}
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, bad := range []struct{ origin, proof string }{{"", ""}, {"https://attacker.internal", csrf}, {"https://workgraph.internal", "wrong"}} {
		if w := call("POST", "/v1/captures", bad.origin, bad.proof, "", false); w.Code != 403 || captures.calls != 0 {
			t.Fatal(w.Code, w.Body.String(), captures.calls)
		}
	}
	if w := call("POST", "/v1/captures", "https://workgraph.internal", csrf, "", false); w.Code != 201 || captures.calls != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := call("POST", "/v1/captures", "", "", "test-secret", false); w.Code != 403 || captures.calls != 1 {
		t.Fatal("cookie overrode explicit bearer", w.Code)
	}
	if w := call("GET", "/mcp", "", "", "", false); w.Code != 401 {
		t.Fatal("cookie reached MCP", w.Code)
	}
	if w := call("GET", "/v1/auth/session", "", "", cookie, false); w.Code != 401 {
		t.Fatal("cookie accepted as bearer", w.Code)
	}
	if w := call("GET", "/v1/auth/session", "", "", "", true); w.Code != 401 {
		t.Fatal("ambiguous cookie accepted", w.Code)
	}
	if w := call("POST", "/v1/auth/logout", "https://workgraph.internal", csrf, "", false); w.Code != 200 || !id.logout {
		t.Fatal(w.Code)
	}
}

func TestOIDCCookiesAreHostOnlyAndNeverExposeHttpOnlySession(t *testing.T) {
	cookie, _ := domain.NewSecret(domain.SessionPrefix)
	csrf, _ := domain.NewSecret(domain.CSRFPrefix)
	flow, _ := domain.NewSecret(domain.FlowPrefix)
	fixture := &identityFixture{cookie: cookie, csrf: csrf, session: domain.Session{ExpiresAt: time.Now().Add(time.Hour)}}
	h := httpapi.NewHandler(httpapi.Config{Captures: &capture{}, Authenticator: auth{}, Ready: func(context.Context) error { return nil }, Identity: fixture})
	r := httptest.NewRequest("GET", "https://workgraph.internal/v1/auth/oidc/callback?state=fixture&code=fixture", nil)
	_, _, flowName := fixture.Config().CookieNames()
	r.AddCookie(&http.Cookie{Name: flowName, Value: flow})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 303 || w.Header().Get("Location") != "https://workgraph.internal/" || strings.Contains(w.Body.String(), cookie) {
		t.Fatal(w.Code, w.Header())
	}
	sessionName, csrfName, _ := fixture.Config().CookieNames()
	seenSession, seenCSRF := false, false
	for _, c := range w.Result().Cookies() {
		if !c.Secure || c.Domain != "" || c.Path != "/" || c.SameSite != http.SameSiteLaxMode {
			t.Fatal("unsafe cookie attributes")
		}
		if c.Name == sessionName {
			seenSession = c.HttpOnly && c.Value == cookie
		}
		if c.Name == csrfName {
			seenCSRF = !c.HttpOnly && c.Value == csrf
		}
	}
	if !seenSession || !seenCSRF {
		t.Fatal("cookie purposes conflated")
	}
}

type registrationFixture struct {
	identityFixture
	enabled, started bool
}

func (i *registrationFixture) Config() domain.Config {
	c := i.identityFixture.Config()
	c.RegistrationEnabled = i.enabled
	return c
}
func (i *registrationFixture) StartRegistration(context.Context) (string, string, error) {
	i.started = true
	return "https://idp.internal/registration", "private-browser-proof", nil
}
func TestPublicRegistrationIsServerGated(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		identity := &registrationFixture{enabled: enabled}
		h := httpapi.NewHandler(httpapi.Config{Captures: &capture{}, Authenticator: auth{}, Ready: func(context.Context) error { return nil }, Identity: identity})
		r := httptest.NewRequest("GET", "https://workgraph.internal/v1/auth/register", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		expected := 403
		if enabled {
			expected = 303
		}
		if w.Code != expected || identity.started != enabled {
			t.Fatal("registration gate", enabled, w.Code)
		}
		if !enabled && len(w.Result().Cookies()) != 0 {
			t.Fatal("disabled signup created flow")
		}
	}
}

func TestBrowserLoginLocalePreservesServerFlowAndRejectsOtherParameters(t *testing.T) {
	for _, endpoint := range []string{"login", "register"} {
		for _, test := range []struct {
			query, locale string
			valid         bool
		}{
			{"", "", true}, {"?locale=ru", "ru", true}, {"?locale=en", "en", true},
			{"?locale=de", "", false}, {"?locale=", "", false}, {"?locale=en&locale=ru", "", false},
			{"?redirect_uri=https://attacker.invalid", "", false}, {"?locale=en&next=/admin", "", false},
		} {
			t.Run(endpoint+test.query, func(t *testing.T) {
				identity := &registrationFixture{enabled: true}
				h := httpapi.NewHandler(httpapi.Config{Captures: &capture{}, Authenticator: auth{}, Ready: func(context.Context) error { return nil }, Identity: identity})
				w := httptest.NewRecorder()
				h.ServeHTTP(w, httptest.NewRequest("GET", "https://workgraph.internal/v1/auth/"+endpoint+test.query, nil))
				if !test.valid {
					if w.Code < 400 || len(w.Result().Cookies()) != 0 {
						t.Fatalf("invalid language dispatched: %d", w.Code)
					}
					return
				}
				if w.Code != 303 {
					t.Fatalf("language rejected: %d", w.Code)
				}
				destination, e := url.Parse(w.Header().Get("Location"))
				if e != nil || destination.Host != "idp.internal" || destination.Query().Get("ui_locales") != test.locale {
					t.Fatal("language or provider destination changed")
				}
				if endpoint == "login" && destination.Query().Get("state") != "fixture" {
					t.Fatal("server flow state changed")
				}
				if len(w.Result().Cookies()) != 1 {
					t.Fatal("missing browser flow proof")
				}
			})
		}
	}
}
