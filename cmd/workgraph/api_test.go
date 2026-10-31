package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAPICommandWorksOutsideRepositoryWithExplicitIntentAndNoKeyInOutput(t *testing.T) {
	key := "wg_" + strings.Repeat("A", 43)
	t.Setenv("WORKGRAPH_TOKEN", key)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		if r.Method != "POST" || r.URL.Path != "/v1/projects" || r.Header.Get("Authorization") != "Bearer "+key || string(raw) != `{"name":"Core","request_id":"same-intent"}` {
			t.Error("request changed")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		w.Write([]byte(`{"id":"created"}`))
	}))
	defer server.Close()
	t.Chdir(t.TempDir())
	var output, diagnostic bytes.Buffer
	e := run(context.Background(), []string{"api", "post", "/v1/projects", "--server", server.URL, "--input", "-"}, strings.NewReader(`{"name":"Core","request_id":"same-intent"}`), &output, &diagnostic)
	if e != nil || calls != 1 || strings.Contains(output.String()+diagnostic.String(), key) {
		t.Fatal("native API failed or leaked authority", e)
	}
	var response struct {
		Status int `json:"status"`
	}
	if json.Unmarshal(output.Bytes(), &response) != nil || response.Status != 201 {
		t.Fatal("invalid JSON response")
	}
}
func TestAPIMCPHelpAndInvalidFlagsNeedNoCredentials(t *testing.T) {
	t.Setenv("WORKGRAPH_TOKEN", "")
	for _, args := range [][]string{{"api", "--help"}, {"mcp", "--help"}} {
		var out bytes.Buffer
		if e := run(context.Background(), args, nil, &out, &out); e != nil || out.Len() == 0 {
			t.Fatal(e)
		}
	}
	for _, args := range [][]string{{"api", "get", "https://evil.invalid/v1/projects", "--server", "https://gitmora.internal"}, {"api", "get", "/v1/projects", "--server", "https://gitmora.internal", "--token", "private"}, {"mcp", "--server", "https://gitmora.internal?token=private"}} {
		var out bytes.Buffer
		if e := run(context.Background(), args, nil, &out, &out); e == nil || out.Len() != 0 || strings.Contains(e.Error(), "private") {
			t.Fatal("invalid input opened native credential operation or leaked", e)
		}
	}
}
