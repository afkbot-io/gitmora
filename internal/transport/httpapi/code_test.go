package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"workgraph.local/platform/internal/access"
	forgeapp "workgraph.local/platform/internal/forge/application"
	"workgraph.local/platform/internal/transport/httpapi"
)

type codeQuotes struct{ calls int }

func (c *codeQuotes) Status(context.Context, string, string, string) (forgeapp.CodeStatus, error) {
	c.calls++
	return forgeapp.CodeStatus{}, nil
}
func (c *codeQuotes) Quote(context.Context, string, string, string, string, int, int) (forgeapp.CodeQuote, error) {
	c.calls++
	return forgeapp.CodeQuote{}, nil
}
func (c *codeQuotes) Retry(context.Context, string, string, string) error {
	c.calls++
	return nil
}

func TestCodeQuotesKeepIntegrationAuthorityInRESTAndMCP(t *testing.T) {
	service := &codeQuotes{}
	configuration := httpapi.Config{Captures: &capture{}, Authenticator: auth{scopes: []access.Permission{access.ReadCapture}}, Ready: func(context.Context) error { return nil }, Retrieval: &retrieval{}, Code: service}
	root := "/v1/repositories/11111111-1111-4111-8111-111111111111/code"
	handler := httpapi.NewHandler(configuration)
	if w := request(handler, "POST", root+"/file", `{}`, "test-secret"); w.Code != 403 || service.calls != 0 {
		t.Fatal("capture credential reached code quotes")
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "code-scope-contract", Version: "1"}, nil)
	ctx := context.Background()
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", HTTPClient: &http.Client{Transport: contextBearer{http.DefaultTransport}}, DisableStandaloneSSE: true, MaxRetries: -1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "context.file", Arguments: map[string]any{"repository_id": "11111111-1111-4111-8111-111111111111", "commit_sha": strings.Repeat("a", 40), "file_path": "src/auth.go"}})
	if err != nil || result == nil || !result.IsError || service.calls != 0 {
		t.Fatal("capture credential reached code MCP", result, err)
	}
	configuration.Authenticator = auth{scopes: []access.Permission{access.ReadIntegration}}
	handler = httpapi.NewHandler(configuration)
	for _, body := range []string{`{"commit_sha":"a","COMMIT_SHA":"b"}`, `{"unknown":true}`, `{} {}`} {
		if w := request(handler, "POST", root+"/file", body, "test-secret"); w.Code != 400 || service.calls != 0 {
			t.Fatal("ambiguous quote dispatched")
		}
	}
	if w := request(handler, "POST", root+"/file", strings.Repeat(" ", 8193), "test-secret"); w.Code != 413 || service.calls != 0 {
		t.Fatal("unbounded quote dispatched")
	}
	if w := request(handler, "POST", root+"/commits/"+strings.Repeat("a", 40)+"/sync", "", "test-secret"); w.Code != 403 || service.calls != 0 {
		t.Fatal("read credential rescheduled code ingestion")
	}
}
