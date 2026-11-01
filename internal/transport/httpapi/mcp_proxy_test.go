package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/transport/httpapi"
)

func TestMCPPublicAuthorityWorksBehindLoopbackProxyWithoutTrustingForwardedHeaders(t *testing.T) {
	config := httpapi.Config{Captures: &capture{}, Authenticator: auth{scopes: []access.Permission{access.ReadCapture}}, Ready: func(context.Context) error { return nil }, Retrieval: &retrieval{}, MCPPublicOrigin: "https://gitmora.example.test:8443"}
	handler := httpapi.NewHandler(config)
	server := httptest.NewServer(handler)
	defer server.Close()
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","capabilities":{},"clientInfo":{"name":"proxy-test","version":"1"}}}`
	send := func(host, origin, key string) *http.Response {
		req, _ := http.NewRequest("POST", server.URL+"/mcp", strings.NewReader(body))
		req.Host = host
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("X-Forwarded-Host", "gitmora.example.test:8443")
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		response, e := server.Client().Do(req)
		if e != nil {
			t.Fatal(e)
		}
		return response
	}
	for _, test := range []struct {
		host, origin, key string
		status            int
	}{
		{"gitmora.example.test:8443", "", "test-secret", 200},
		{"gitmora.example.test:9443", "", "test-secret", 403},
		{"attacker.example.test", "", "test-secret", 403},
		{"gitmora.example.test:8443", "https://attacker.example.test", "test-secret", 403},
		{"gitmora.example.test:8443", "", "wrong-key", 401},
	} {
		response := send(test.host, test.origin, test.key)
		response.Body.Close()
		if response.StatusCode != test.status {
			t.Fatalf("authority/origin gate: %s got %d expected %d", test.host, response.StatusCode, test.status)
		}
	}
	config.MCPPublicOrigin = "https://gitmora.example.test/path"
	invalid := httpapi.NewHandler(config)
	req := httptest.NewRequest("POST", "/mcp", strings.NewReader(body))
	req.Host = "gitmora.example.test"
	req.Header.Set("Authorization", "Bearer test-secret")
	w := httptest.NewRecorder()
	invalid.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatal("invalid public policy accepted")
	}
}
