package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"workgraph.local/platform/internal/access"
	identityapp "workgraph.local/platform/internal/identity/application"
	"workgraph.local/platform/internal/identity/domain"
	"workgraph.local/platform/internal/transport/httpapi"
)

type cliFixture struct {
	calls int
	err   error
	owner string
}

func (s *cliFixture) Start(context.Context, domain.CLIRequest) (identityapp.CLIStarted, error) {
	s.calls++
	return identityapp.CLIStarted{ID: "11111111-1111-4111-8111-111111111111", VerificationURI: "https://workgraph.internal/v1/auth/cli/login", IntervalSeconds: 2}, s.err
}
func (s *cliFixture) Inspect(_ context.Context, p domain.Session, _ string) (domain.CLIView, error) {
	s.calls++
	s.owner = p.Principal.UserID
	return domain.CLIView{Status: "pending"}, s.err
}
func (s *cliFixture) Decide(_ context.Context, p domain.Session, _, _ string, _ bool) (domain.CLIView, error) {
	s.calls++
	s.owner = p.Principal.UserID
	return domain.CLIView{Status: "approved"}, s.err
}
func (s *cliFixture) Poll(context.Context, string, string, string) (domain.CLIView, error) {
	s.calls++
	return domain.CLIView{Status: "pending"}, s.err
}
func (s *cliFixture) Cancel(context.Context, string, string, string) (domain.CLIView, error) {
	s.calls++
	return domain.CLIView{Status: "cancelled"}, s.err
}

func TestCLIHTTPProofEndpointsAndBrowserOnlyConsent(t *testing.T) {
	cookie, _ := domain.NewSecret(domain.SessionPrefix)
	csrf, _ := domain.NewSecret(domain.CSRFPrefix)
	i := &identityFixture{cookie: cookie, csrf: csrf, session: domain.Session{ID: "11111111-1111-4111-8111-111111111111", BindingID: "22222222-2222-4222-8222-222222222222", Principal: access.Principal{UserID: "33333333-3333-4333-8333-333333333333", TokenID: "44444444-4444-4444-8444-444444444444", Scopes: domain.InteractiveScopes()}, CSRFHash: domain.SecretHash(domain.CSRFPrefix, csrf), ExpiresAt: time.Now().Add(time.Hour)}}
	s := &cliFixture{}
	h := httpapi.NewHandler(httpapi.Config{Captures: &capture{}, Authenticator: auth{}, Ready: func(context.Context) error { return nil }, Identity: i, CLIEnrollment: s})
	sessionName, csrfName, _ := i.Config().CookieNames()
	call := func(path, body string, browser, bearer, proof bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "https://workgraph.internal"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if browser {
			r.AddCookie(&http.Cookie{Name: sessionName, Value: cookie})
			r.AddCookie(&http.Cookie{Name: csrfName, Value: csrf})
		}
		if proof {
			r.Header.Set("Origin", "https://workgraph.internal")
			r.Header.Set("X-Workgraph-CSRF", csrf)
		}
		if bearer {
			r.Header.Set("Authorization", "Bearer test-secret")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, p := range []string{"inspect", "decide"} {
		body := `{"user_code":"ABCD-EFGH"}`
		if p == "decide" {
			body = `{"user_code":"ABCD-EFGH","expected_fingerprint":"` + strings.Repeat("a", 64) + `","approve":true}`
		}
		for _, tc := range []struct {
			browser, bearer, proof bool
			status                 int
		}{{false, false, false, 401}, {true, true, true, 401}, {true, false, false, 403}} {
			before := s.calls
			if w := call("/v1/auth/cli/"+p, body, tc.browser, tc.bearer, tc.proof); w.Code != tc.status || s.calls != before {
				t.Fatal(p, w.Code, w.Body.String())
			}
		}
		if w := call("/v1/auth/cli/"+p, body, true, false, true); w.Code != 200 || s.owner != i.session.Principal.UserID {
			t.Fatal(p, w.Code, w.Body.String())
		}
	}
	before := s.calls
	for _, bad := range []struct{ path, body string }{
		{"inspect", `{"user_code":"ABCD-EFGH","user_id":"someone else"}`},
		{"decide", `{"user_code":"ABCD-EFGH","expected_fingerprint":"x"}`},
		{"decide", `{"user_code":"ABCD-EFGH","approve":true,"approve":false}`},
		{"prepare", `{"secret":"must-never-be-uploaded"}`},
		{"poll", `{"id":"x","device_secret":"x","proof":"x","extra":1}`},
		{"cancel", `{"id":"x","device_secret":"x","proof":"x","extra":1}`},
		{"inspect?unknown=1", `{}`},
	} {
		w := call("/v1/auth/cli/"+bad.path, bad.body, true, false, true)
		if w.Code < 400 || s.calls != before {
			t.Fatal("invalid body dispatched", bad.path, w.Code)
		}
	}
	// Public proof routes do not consume browser authority or require its CSRF cookie.
	w := call("/v1/auth/cli/prepare", `{"id":"11111111-1111-4111-8111-111111111111","device_hash":"a","proof_hash":"b","credential_hash":"c","code_hash":"d","label":"Laptop","scopes":["credentials:manage"],"ttl_seconds":86400}`, true, false, false)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("proof response cached")
	}
	for _, private := range []string{"secret_hash", "credential_hash", "device_hash", "proof_hash"} {
		if strings.Contains(w.Body.String(), private) {
			t.Fatal("hash commitment echoed")
		}
	}
	if w = call("/v1/auth/cli/poll", `{"id":"11111111-1111-4111-8111-111111111111","device_secret":"proof","proof":"proof"}`, false, false, false); w.Code != 200 || w.Header().Get("Retry-After") != "2" {
		t.Fatal(w.Code, w.Header())
	}
	if w = call("/v1/auth/cli/cancel", `{"id":"11111111-1111-4111-8111-111111111111","device_secret":"proof","proof":"proof"}`, true, false, false); w.Code != 200 || !strings.Contains(w.Body.String(), "cancelled") {
		t.Fatal("proof-owned cancel requires browser authority", w.Code)
	}
	s.err = domain.ErrFreshLogin
	if w = call("/v1/auth/cli/decide", `{"user_code":"ABCD-EFGH","expected_fingerprint":"`+strings.Repeat("a", 64)+`","approve":true}`, true, false, true); w.Code != 403 || !strings.Contains(w.Body.String(), "cli_fresh_login_required") {
		t.Fatal(w.Code, w.Body.String())
	}
	s.err = domain.ErrConflict
	if w = call("/v1/auth/cli/decide", `{"user_code":"ABCD-EFGH","expected_fingerprint":"`+strings.Repeat("a", 64)+`","approve":true}`, true, false, true); w.Code != 409 || !strings.Contains(w.Body.String(), "cli_enrollment_conflict") {
		t.Fatal("CLI conflict confused with binding administration", w.Code)
	}
}

func TestCLIContinuationIsFixedOriginPublicCodeAndOneUseCookie(t *testing.T) {
	cookie, _ := domain.NewSecret(domain.SessionPrefix)
	csrf, _ := domain.NewSecret(domain.CSRFPrefix)
	flow, _ := domain.NewSecret(domain.FlowPrefix)
	i := &identityFixture{cookie: cookie, csrf: csrf, session: domain.Session{ExpiresAt: time.Now().Add(time.Hour)}}
	h := httpapi.NewHandler(httpapi.Config{Captures: &capture{}, Authenticator: auth{}, Ready: func(context.Context) error { return nil }, Identity: i, CLIEnrollment: &cliFixture{}})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "https://attacker.invalid/v1/auth/cli/login?code=ABCD-EFGH", nil)
	h.ServeHTTP(w, r)
	if w.Code != 303 || w.Header().Get("Location") != "https://workgraph.internal/v1/auth/login" {
		t.Fatal(w.Code, w.Header())
	}
	var continuation *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Value == "ABCD-EFGH" {
			continuation = c
		}
	}
	if continuation == nil || !continuation.HttpOnly || !continuation.Secure || continuation.Domain != "" || continuation.Path != "/" || continuation.MaxAge != 600 {
		t.Fatal("unsafe continuation cookie")
	}
	r = httptest.NewRequest("GET", "https://workgraph.internal/v1/auth/oidc/callback?state=public-state&code=provider-proof", nil)
	_, _, flowName := i.Config().CookieNames()
	r.AddCookie(&http.Cookie{Name: flowName, Value: flow})
	r.AddCookie(continuation)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 303 || w.Header().Get("Location") != "https://workgraph.internal/#cli=ABCD-EFGH" {
		t.Fatal(w.Code, w.Header())
	}
	cleared := false
	for _, c := range w.Result().Cookies() {
		if c.Name == continuation.Name && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("continuation survived callback")
	}
	for _, query := range []string{"code=ABCD-EFGH&code=JKLM-NPQR", "code=ABCD-EFGH&return_to=https://attacker.invalid", "code=%00", "code=../../../"} {
		r = httptest.NewRequest("GET", "https://workgraph.internal/v1/auth/cli/login?"+query, nil)
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code < 400 || w.Header().Get("Location") != "" {
			t.Fatal("unsafe continuation accepted", w.Code)
		}
	}
	raw, _ := json.Marshal(domain.CLISecrets{Device: "private", Credential: "private"})
	if string(raw) != "{}" {
		t.Fatal("device entropy serialized")
	}
}

func TestCLIContinuationKeepsLanguageWithoutCallerRedirects(t *testing.T) {
	identity := &identityFixture{}
	h := httpapi.NewHandler(httpapi.Config{Captures: &capture{}, Authenticator: auth{}, Ready: func(context.Context) error { return nil }, Identity: identity, CLIEnrollment: &cliFixture{}})
	for _, tc := range []struct {
		query, target string
		valid         bool
	}{
		{"code=ABCD-EFGH", "https://workgraph.internal/v1/auth/login", true},
		{"code=ABCD-EFGH&locale=en", "https://workgraph.internal/v1/auth/login?locale=en", true},
		{"code=ABCD-EFGH&locale=ru", "https://workgraph.internal/v1/auth/login?locale=ru", true},
		{"code=ABCD-EFGH&locale=de", "", false},
		{"code=ABCD-EFGH&locale=en&redirect_uri=https://attacker.invalid", "", false},
		{"code=ABCD-EFGH&code=XXXX-YYYY", "", false},
		{"locale=en", "", false},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "https://workgraph.internal/v1/auth/cli/login?"+tc.query, nil))
		if !tc.valid {
			if w.Code < 400 || len(w.Result().Cookies()) > 0 {
				t.Fatal("invalid continuation dispatched")
			}
			continue
		}
		if w.Code != 303 || w.Header().Get("Location") != tc.target || len(w.Result().Cookies()) != 1 || w.Result().Cookies()[0].Value != "ABCD-EFGH" {
			t.Fatal("continuation changed its destination or proof")
		}
	}
}
