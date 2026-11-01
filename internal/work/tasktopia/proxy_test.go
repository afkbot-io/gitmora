package tasktopia

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"workgraph.local/platform/internal/work/domain"
)

func TestTasktopiaCustomTLSMCPUsesOnlyItsSelectedProxy(t *testing.T) {
	const country = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	const token = "synthetic-tasktopia-token"
	server := mcp.NewServer(&mcp.Implementation{Name: "custom-tasktopia", Version: "test"}, nil)
	server.AddTool(&mcp.Tool{Name: "country.list", InputSchema: json.RawMessage(`{"type":"object"}`)}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{StructuredContent: map[string]any{"result": []any{map[string]string{"id": country}}}}, nil
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true})
	var received, tunnels atomic.Int32
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/workspace/mcp" || r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("credential/endpoint changed")
			w.WriteHeader(403)
			return
		}
		received.Add(1)
		handler.ServeHTTP(w, r)
	}))
	defer target.Close()
	address, _ := url.Parse(target.URL)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "CONNECT" || r.Host != address.Host || r.Header.Get("Authorization") != "" || r.Header.Get("Proxy-Authorization") != "" {
			t.Error("unsafe CONNECT")
			w.WriteHeader(403)
			return
		}
		upstream, e := net.DialTimeout("tcp", address.Host, time.Second)
		if e != nil {
			t.Error(e)
			w.WriteHeader(502)
			return
		}
		client, b, e := w.(http.Hijacker).Hijack()
		if e != nil {
			upstream.Close()
			t.Error(e)
			return
		}
		tunnels.Add(1)
		b.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		b.Flush()
		go func() { defer client.Close(); defer upstream.Close(); io.Copy(upstream, client) }()
		io.Copy(client, upstream)
		client.Close()
		upstream.Close()
	}))
	defer proxy.Close()
	endpoint := target.URL + "/workspace/mcp"
	c, e := NewWithProxy([]string{endpoint}, proxy.URL)
	if e != nil {
		t.Fatal(e)
	}
	c.transport.TLSClientConfig = &tls.Config{RootCAs: target.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs, MinVersion: tls.VersionTLS12}
	if e = c.Verify(context.Background(), domain.Config{Endpoint: endpoint, CountryID: country, Token: token}); e != nil {
		t.Fatal(e)
	}
	if tunnels.Load() == 0 || received.Load() == 0 {
		t.Fatal("custom route was not exercised")
	}
	before := tunnels.Load()
	if e = c.Verify(context.Background(), domain.Config{Endpoint: target.URL + "/other/mcp", CountryID: country, Token: token}); e == nil || tunnels.Load() != before {
		t.Fatal("unapproved endpoint received I/O", e)
	}
	untrusted, e := NewWithProxy([]string{endpoint}, proxy.URL)
	if e != nil {
		t.Fatal(e)
	}
	if e = untrusted.Verify(context.Background(), domain.Config{Endpoint: endpoint, CountryID: country, Token: token}); e == nil {
		t.Fatal("untrusted certificate accepted")
	}
}

func TestTasktopiaProxyPolicyRejectsAmbiguousDestinations(t *testing.T) {
	for _, proxy := range []string{"https://proxy.internal", "http://user:secret@proxy.internal", "http://proxy.internal/path", "http://proxy.internal?target=other", "http://proxy.internal#fragment"} {
		if _, err := NewWithProxy([]string{"https://tasks.example.com/mcp"}, proxy); err == nil {
			t.Fatal("unsafe proxy accepted")
		}
	}
	if _, err := NewWithProxy([]string{"http://localhost:1234/mcp"}, "http://localhost:1235"); err == nil {
		t.Fatal("credential-bearing HTTP selected through proxy")
	}
}
