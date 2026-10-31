package httpapi_test

import (
	"context"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"net/http"
	"net/http/httptest"
	"testing"

	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/contextpack/domain"
	"workgraph.local/platform/internal/transport/httpapi"
)

func TestContextBrowseMCPUsesTheSamePrincipalAndReauthenticates(t *testing.T) {
	b := &contextBrowser{}
	a := &mutableAuth{}
	h := httpapi.NewHandler(httpapi.Config{Captures: &capture{}, Authenticator: a, Ready: func(context.Context) error { return nil }, Retrieval: &retrieval{}, ContextBrowser: b})
	server := httptest.NewServer(h)
	defer server.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "browse-contract", Version: "1"}, nil)
	ctx := context.Background()
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", HTTPClient: &http.Client{Transport: contextBearer{http.DefaultTransport}}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "context.browse", Arguments: map[string]any{"project_id": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "kind": "task", "limit": 1}})
	if err != nil || result.IsError || b.calls != 1 || !b.p.Has(access.ReadIntegration) || b.p.Has(access.ReadCapture) {
		t.Fatal("browse widened MCP authority", result, err)
	}
	a.mu.Lock()
	a.revoked = true
	a.mu.Unlock()
	_, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "context.browse", Arguments: map[string]any{"project_id": "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "kind": "task"}})
	if err == nil || b.calls != 1 {
		t.Fatal("browse reused revoked MCP identity", err)
	}
}

type contextBrowser struct {
	calls int
	p     access.Principal
}

func (s *contextBrowser) Browse(_ context.Context, p access.Principal, q domain.BrowseQuery) (domain.BrowsePage, error) {
	s.calls++
	s.p = p
	return domain.BrowsePage{Query: q}, nil
}
func TestContextBrowseHTTPKeepsStrictJSONAndCallerAuthority(t *testing.T) {
	b := &contextBrowser{}
	c := httpapi.Config{Captures: &capture{}, Authenticator: auth{scopes: []access.Permission{access.ReadCapture}}, Ready: func(context.Context) error { return nil }, Retrieval: &retrieval{}, ContextBrowser: b}
	h := httpapi.NewHandler(c)
	for _, body := range []string{`{"kind":"task","kind":"session"}`, `{"unknown":true}`} {
		if w := request(h, "POST", "/v1/context/browse", body, "test-secret"); w.Code != 400 || b.calls != 0 {
			t.Fatal(w.Code)
		}
	}
	if w := request(h, "POST", "/v1/context/browse", `{"project_id":"p","kind":"session"}`, "test-secret"); w.Code != 200 || b.calls != 1 || b.p.Has(access.ReadIntegration) {
		t.Fatal(w.Code, b)
	}
	c.Authenticator = auth{scopes: []access.Permission{access.WriteCapture}}
	if w := request(httpapi.NewHandler(c), "POST", "/v1/context/browse", `{}`, "test-secret"); w.Code != 403 || b.calls != 1 {
		t.Fatal(w.Code, b)
	}
}
