package native_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"workgraph.local/platform/internal/contextpack/native"
	"workgraph.local/platform/internal/distribution/buildinfo"
)

func TestBrokerExposesOnlyCoreReadToolsAndRechecksRemoteAuthority(t *testing.T) {
	key := "wg_" + strings.Repeat("A", 43)
	var revoked atomic.Bool
	upstream := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "1"}, nil)
	for _, name := range []string{"context.search", "context.browse", "context.pack", "context.expand", "context.resolve", "context.file", "danger.execute"} {
		mcp.AddTool(upstream, &mcp.Tool{Name: name}, func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, map[string]string, error) {
			return nil, map[string]string{"text": "Exact permitted context"}, nil
		})
	}
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return upstream }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+key || revoked.Load() {
			w.WriteHeader(401)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	left, right := mcp.NewInMemoryTransports()
	done := make(chan error, 1)
	go func() { done <- native.Run(ctx, server.URL, key, right) }()
	client := mcp.NewClient(&mcp.Implementation{Name: "agent", Version: "1"}, nil)
	session, e := client.Connect(ctx, left, nil)
	if e != nil {
		t.Fatal(e)
	}
	if session.InitializeResult().ServerInfo.Version != buildinfo.Current().Version {
		t.Fatal("MCP handshake does not identify the running client release")
	}
	tools, e := session.ListTools(ctx, &mcp.ListToolsParams{})
	if e != nil || len(tools.Tools) != 6 {
		t.Fatal(tools, e)
	}
	result, e := session.CallTool(ctx, &mcp.CallToolParams{Name: "context.search", Arguments: map[string]any{}})
	if e != nil || result.IsError {
		t.Fatal(result, e)
	}
	revoked.Store(true)
	result, e = session.CallTool(ctx, &mcp.CallToolParams{Name: "context.search", Arguments: map[string]any{}})
	if e == nil && !result.IsError {
		t.Fatal("revoked authority reused")
	}
	session.Close()
	cancel()
	<-done
}
