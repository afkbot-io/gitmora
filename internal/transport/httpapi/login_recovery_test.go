package httpapi_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"workgraph.local/platform/internal/identity/domain"
	"workgraph.local/platform/internal/transport/httpapi"
)

type failedLogin struct {
	identityFixture
	err error
}

func TestDisabledIdentityBrowserRecoveryDoesNotUseTheRequestHost(t *testing.T) {
	c := httpapi.Config{Captures: &capture{}, Authenticator: auth{}, Ready: func(context.Context) error { return nil }, WebUI: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}
	for _, path := range []string{"/v1/auth/login", "/v1/auth/oidc/callback?state=private-proof"} {
		r := httptest.NewRequest("GET", "https://untrusted-host.invalid"+path, nil)
		r.Header.Set("Accept", "text/html")
		w := httptest.NewRecorder()
		httpapi.NewHandler(c).ServeHTTP(w, r)
		if w.Code != 303 || w.Header().Get("Location") != "/#login=unavailable" {
			t.Fatal("disabled login trusted request authority", w.Code, w.Header())
		}
	}
}

func (f *failedLogin) Start(context.Context) (string, string, error) { return "", "", f.err }

func TestBrowserLoginRecoveryNeverReflectsProofOrProviderContent(t *testing.T) {
	for _, tc := range []struct {
		path, accept string
		err          error
		status       int
		fragment     string
		ui           bool
	}{
		{"/v1/auth/login", "text/html,application/xhtml+xml,*/*;q=0.8", domain.ErrProvider, 303, "unavailable", true},
		{"/v1/auth/login", "text/html", domain.ErrCapacity, 303, "busy", true},
		{"/v1/auth/login", "text/html", errors.New("private-provider-body"), 303, "unavailable", true},
		{"/v1/auth/oidc/callback?state=private-proof&code=private-code&unexpected=private-text", "text/html", domain.ErrProvider, 303, "failed", true},
		{"/v1/auth/login", "application/json", domain.ErrProvider, 503, "", true},
		{"/v1/auth/login", "text/html;q=0,application/json", domain.ErrProvider, 503, "", true},
		{"/v1/auth/login", "text/html;q=0.2,application/json;q=0.8", domain.ErrProvider, 503, "", true},
		{"/v1/auth/login", "*/*", domain.ErrProvider, 503, "", true},
		{"/v1/auth/login", "text/html", domain.ErrProvider, 503, "", false},
	} {
		t.Run(tc.path+tc.accept, func(t *testing.T) {
			c := httpapi.Config{Captures: &capture{}, Authenticator: auth{}, Ready: func(context.Context) error { return nil }, Identity: &failedLogin{err: tc.err}}
			if tc.ui {
				c.WebUI = http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
			}
			r := httptest.NewRequest("GET", "https://workgraph.internal"+tc.path, nil)
			r.Header.Set("Accept", tc.accept)
			w := httptest.NewRecorder()
			httpapi.NewHandler(c).ServeHTTP(w, r)
			if w.Code != tc.status {
				t.Fatalf("unexpected login representation: %d %s", w.Code, w.Body.String())
			}
			if tc.fragment != "" && w.Header().Get("Location") != "https://workgraph.internal/#login="+tc.fragment {
				t.Fatal("unsafe recovery destination", w.Header())
			}
			if tc.fragment == "" && w.Header().Get("Location") != "" {
				t.Fatal("JSON client was redirected")
			}
			if strings.Contains(w.Body.String()+w.Header().Get("Location"), "private-") || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("login proof leaked or cached")
			}
		})
	}
}
