package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/capture/application"
	"workgraph.local/platform/internal/capture/domain"
	"workgraph.local/platform/internal/transport/httpapi"
)

type auth struct {
	err    error
	scopes []access.Permission
}

func (a auth) Authenticate(_ context.Context, raw string) (access.Principal, error) {
	if a.err != nil {
		return access.Principal{}, a.err
	}
	if raw != "test-secret" {
		return access.Principal{}, access.ErrUnauthenticated
	}
	return access.Principal{UserID: "33333333-3333-4333-8333-333333333333", Scopes: a.scopes}, nil
}

type capture struct {
	calls   int
	err     error
	created bool
}

func (c *capture) Ingest(_ context.Context, _ string, s domain.Snapshot) (application.IngestResult, error) {
	c.calls++
	return application.IngestResult{RepositoryID: s.RepositoryID, SessionID: s.SessionID, Revision: s.Revision, Created: c.created}, c.err
}
func (c *capture) Get(_ context.Context, _, _, _ string, _ int64) (application.StoredRevision, error) {
	c.calls++
	session, err := domain.NewSession(input())
	if c.err != nil {
		return application.StoredRevision{}, c.err
	}
	return application.StoredRevision{Session: session, ConfirmedCommits: []string{}}, err
}
func input() domain.Snapshot {
	return domain.Snapshot{SchemaVersion: 1, RepositoryID: "11111111-1111-4111-8111-111111111111", SessionID: "22222222-2222-4222-8222-222222222222", Revision: 1, Agent: domain.Agent{Name: "local", Model: "local"}, StartedAt: time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC), Messages: []domain.Message{{Role: "user", Text: "Fix queue"}}}
}
func handler(c *capture, a auth) http.Handler {
	return httpapi.NewHandler(httpapi.Config{Captures: c, Authenticator: a, Ready: func(context.Context) error { return nil }})
}
func request(h http.Handler, method, path, body, secret string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	if secret != "" {
		r.Header.Set("Authorization", "Bearer "+secret)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestCaptureRoutesRequireCredentialAndExplicitScope(t *testing.T) {
	bytes, _ := json.Marshal(input())
	c := &capture{created: true}
	h := handler(c, auth{scopes: []access.Permission{access.ReadCapture, access.WriteCapture}})
	if w := request(h, "POST", "/v1/captures", string(bytes), ""); w.Code != 401 || c.calls != 0 || w.Header().Get("WWW-Authenticate") == "" {
		t.Fatal("unauthenticated request reached capture")
	}
	if w := request(h, "POST", "/v1/captures", string(bytes), "test-secret"); w.Code != 201 || c.calls != 1 {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}
	c.created = false
	if w := request(h, "POST", "/v1/captures", string(bytes), "test-secret"); w.Code != 200 {
		t.Fatal("retry must return 200")
	}
	readOnly := handler(c, auth{scopes: []access.Permission{access.ReadCapture}})
	if w := request(readOnly, "POST", "/v1/captures", string(bytes), "test-secret"); w.Code != 403 {
		t.Fatal("read-only token wrote capture")
	}
	path := "/v1/repositories/" + input().RepositoryID + "/sessions/" + input().SessionID
	if w := request(readOnly, "GET", path, "", "test-secret"); w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("read: %d %s", w.Code, w.Body)
	}
}

func TestStrictJSONBoundaryRejectsAmbiguityBeforeApplication(t *testing.T) {
	for _, body := range []string{`{"revision":1,"revision":2}`, `{"revision":1,"Revision":2}`, `{"messages":[{"role":"user","role":"system"}]}`, `{"unknown":true}`, `{} {}`, strings.Repeat("[", 40) + strings.Repeat("]", 40), string([]byte{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'})} {
		c := &capture{}
		w := request(handler(c, auth{scopes: []access.Permission{access.WriteCapture}}), "POST", "/v1/captures", body, "test-secret")
		if w.Code != 400 || c.calls != 0 {
			t.Errorf("invalid body reached application: %d", w.Code)
		}
	}
	c := &capture{}
	w := request(handler(c, auth{scopes: []access.Permission{access.WriteCapture}}), "POST", "/v1/captures", strings.Repeat(" ", 3*1024*1024+1), "test-secret")
	if w.Code != 413 || c.calls != 0 {
		t.Fatal("oversized request accepted")
	}
}

func TestErrorMappingNeverLeaksInfrastructureDetails(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{{access.ErrDenied, 403}, {application.ErrNotFound, 404}, {application.ErrConflict, 409}, {domain.ErrInvalid, 422}, {errors.New("postgres://private-secret"), 500}} {
		c := &capture{err: tc.err}
		bytes, _ := json.Marshal(input())
		w := request(handler(c, auth{scopes: []access.Permission{access.WriteCapture}}), "POST", "/v1/captures", string(bytes), "test-secret")
		if w.Code != tc.status || strings.Contains(w.Body.String(), "private-secret") {
			t.Errorf("unsafe mapping: %d %s", w.Code, w.Body)
		}
	}
}

func TestHealthAndReadinessAreDistinct(t *testing.T) {
	h := httpapi.NewHandler(httpapi.Config{Captures: &capture{}, Authenticator: auth{}, Ready: func(context.Context) error { return errors.New("database password is private") }})
	if w := request(h, "GET", "/health/live", "", ""); w.Code != 200 {
		t.Fatal("liveness depends on database")
	}
	if w := request(h, "GET", "/health/ready", "", ""); w.Code != 503 || strings.Contains(w.Body.String(), "password") {
		t.Fatal("readiness failure is not safely reported")
	}
}
