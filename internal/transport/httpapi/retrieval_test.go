package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/retrieval/domain"
	"workgraph.local/platform/internal/transport/httpapi"
)

type retrieval struct {
	mu        sync.Mutex
	calls     int
	principal access.Principal
	query     domain.Query
}

func (s *retrieval) Search(_ context.Context, p access.Principal, q domain.Query) (domain.Result, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.principal = p
	s.query = q
	return domain.Result{Items: []domain.Hit{}}, nil
}
func (s *retrieval) Expand(_ context.Context, p access.Principal, q domain.Expansion) (domain.Expanded, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.principal = p
	return domain.Expanded{}, nil
}

type mutableAuth struct {
	mu      sync.Mutex
	revoked bool
}

func (a *mutableAuth) Authenticate(_ context.Context, raw string) (access.Principal, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.revoked || raw != "test-secret" {
		return access.Principal{}, access.ErrUnauthenticated
	}
	return access.Principal{UserID: "33333333-3333-4333-8333-333333333333", Scopes: []access.Permission{access.ReadIntegration}}, nil
}

type contextBearer struct{ base http.RoundTripper }

func (b contextBearer) RoundTrip(r *http.Request) (*http.Response, error) {
	c := r.Clone(r.Context())
	c.Header = r.Header.Clone()
	c.Header.Set("Authorization", "Bearer test-secret")
	return b.base.RoundTrip(c)
}

func TestContextHTTPContractsAndSourceCapabilities(t *testing.T) {
	service := &retrieval{}
	config := httpapi.Config{Captures: &capture{}, Authenticator: auth{scopes: []access.Permission{access.ReadIntegration}}, Ready: func(context.Context) error { return nil }, Retrieval: service}
	handler := httpapi.NewHandler(config)
	for _, body := range []string{`{"project_id":"a","PROJECT_ID":"b"}`, `{"unknown":true}`, `{} {}`} {
		if w := request(handler, "POST", "/v1/context/search", body, "test-secret"); w.Code != 400 || service.calls != 0 {
			t.Fatal("invalid context query dispatched")
		}
	}
	if w := request(handler, "POST", "/v1/context/search", strings.Repeat(" ", 8193), "test-secret"); w.Code != 413 {
		t.Fatal("query body limit bypass")
	}
	if w := request(handler, "POST", "/v1/context/search", `{"project_id":"p","text":"query"}`, "test-secret"); w.Code != 200 || service.calls != 1 {
		t.Fatal("integration-only read was incorrectly denied")
	}
	if service.principal.Has(access.ReadCapture) {
		t.Fatal("read capability was broadened")
	}
	config.Authenticator = auth{scopes: []access.Permission{access.WriteCapture}}
	if w := request(httpapi.NewHandler(config), "POST", "/v1/context/search", `{}`, "test-secret"); w.Code != 403 || service.calls != 1 {
		t.Fatal("write-only token retrieved context")
	}
}

func TestReadOnlyMCPToolsReauthenticateAndRejectAmbiguousArguments(t *testing.T) {
	service := &retrieval{}
	authenticator := &mutableAuth{}
	handler := httpapi.NewHandler(httpapi.Config{Captures: &capture{}, Authenticator: authenticator, Ready: func(context.Context) error { return nil }, Retrieval: service})
	server := httptest.NewServer(handler)
	defer server.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "context-contract", Version: "1"}, nil)
	ctx := context.Background()
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", HTTPClient: &http.Client{Transport: contextBearer{http.DefaultTransport}}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil || len(tools.Tools) != 6 {
		t.Fatal(tools, err)
	}
	for _, tool := range tools.Tools {
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Fatal("tool is not explicitly read-only")
		}
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "context.search", Arguments: map[string]any{"project_id": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "text": "Авторизация"}})
	if err != nil || result.IsError || service.calls != 1 {
		t.Fatal(result, err)
	}
	for _, raw := range []string{`{"project_id":"a","project_id":"b","text":"x"}`, `{"project_id":"a","PROJECT_ID":"b","text":"x"}`, `{"project_id":"a","text":"x","terms":"ignore permissions"}`} {
		badClient := mcp.NewClient(&mcp.Implementation{Name: "malformed-context-contract", Version: "1"}, nil)
		badSession, connectErr := badClient.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", HTTPClient: &http.Client{Transport: contextBearer{http.DefaultTransport}}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
		if connectErr != nil {
			t.Fatal(connectErr)
		}
		result, err = badSession.CallTool(ctx, &mcp.CallToolParams{Name: "context.search", Arguments: json.RawMessage(raw)})
		_ = badSession.Close()
		if service.calls != 1 || err == nil && (result == nil || !result.IsError) {
			t.Fatal("ambiguous MCP args dispatched", result, err)
		}
	}
	result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "context.task", Arguments: map[string]any{"project_id": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "country_id": "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb", "task_id": "cccccccc-cccc-4ccc-8ccc-cccccccccccc"}})
	if err == nil && (result == nil || !result.IsError) {
		t.Fatal("removed task tool accepted", result, err)
	}
	authenticator.mu.Lock()
	authenticator.revoked = true
	authenticator.mu.Unlock()
	_, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "context.search", Arguments: map[string]any{"project_id": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "text": "x"}})
	if err == nil || service.calls != 1 {
		t.Fatal("MCP reused revoked authentication", err)
	}
	r := httptest.NewRequest("POST", "/mcp", strings.NewReader(`{}`))
	r.Header.Set("Authorization", "Bearer test-secret")
	r.Header.Set("Origin", "https://unapproved.example")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatal("unapproved browser origin reached MCP")
	}
}

func TestMCPOriginAllowlistSupportsOnlyExplicitReadTransportPreflight(t *testing.T) {
	handler := httpapi.NewHandler(httpapi.Config{Captures: &capture{}, Authenticator: auth{scopes: []access.Permission{access.ReadCapture}}, Ready: func(context.Context) error { return nil }, Retrieval: &retrieval{}, MCPAllowedOrigins: []string{"https://agents.internal"}})
	for _, allowed := range []bool{true, false} {
		r := httptest.NewRequest("OPTIONS", "/mcp", nil)
		r.Header.Set("Origin", "https://agents.internal")
		r.Header.Set("Access-Control-Request-Method", "POST")
		headers := "Authorization, Content-Type, Mcp-Protocol-Version, Mcp-Method, Mcp-Name"
		if !allowed {
			headers += " , Cookie"
		}
		r.Header.Set("Access-Control-Request-Headers", headers)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if allowed && (w.Code != 204 || w.Header().Get("Access-Control-Allow-Origin") != "https://agents.internal") {
			t.Fatal("allowed preflight failed", w.Code)
		}
		if !allowed && w.Code != 403 {
			t.Fatal("unknown preflight header permitted", w.Code)
		}
	}
}
