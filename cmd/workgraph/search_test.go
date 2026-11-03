package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"workgraph.local/platform/internal/retrieval/domain"
)

func TestSearchCommandPreservesExactSelectorsAndUsesSavedAuthority(t *testing.T) {
	key := "wg_" + strings.Repeat("A", 43)
	t.Setenv("WORKGRAPH_TOKEN", key)
	var received domain.Query
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != "POST" || r.URL.Path != "/v1/context/search" || r.Header.Get("Authorization") != "Bearer "+key {
			t.Error("wrong endpoint or authority")
		}
		if json.NewDecoder(r.Body).Decode(&received) != nil {
			t.Error("invalid search body")
		}
		json.NewEncoder(w).Encode(domain.Result{Items: []domain.Hit{}, SearchMode: "lexical"})
	}))
	defer server.Close()
	project := "11111111-1111-4111-8111-111111111111"
	for _, text := range []string{"refreshToken", strings.Repeat("a", 40), "22222222-2222-4222-8222-222222222222"} {
		var out bytes.Buffer
		err := searchCommand(context.Background(), []string{text, "--server", server.URL, "--project", project, "--mode", "lexical"}, &out)
		if err != nil || received.Text != text || received.ProjectID != project || received.Mode != "lexical" || strings.Contains(out.String(), key) {
			t.Fatal("search selector/authority contract failed", err)
		}
	}
}

func TestSearchCommandRejectsInvalidModeBeforeNetwork(t *testing.T) {
	var out bytes.Buffer
	if err := searchCommand(context.Background(), []string{"hello", "--server", "https://gitmora.example.com", "--project", "11111111-1111-4111-8111-111111111111", "--mode", "magic"}, &out); err == nil {
		t.Fatal("unsupported search engine accepted")
	}
}
