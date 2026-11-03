package ollama_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"workgraph.local/platform/internal/retrieval/ollama"
)

func TestLocalEmbeddingContractAndInvalidResponses(t *testing.T) {
	mode := "ok"
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			digest := strings.Repeat("a", 64)
			if mode == "changed-digest" {
				digest = strings.Repeat("b", 64)
			}
			json.NewEncoder(w).Encode(map[string]any{"models": []map[string]string{{"name": "test", "digest": digest}}})
			return
		}
		calls.Add(1)
		if mode == "redirect" {
			http.Redirect(w, r, "http://127.0.0.1:1", 307)
			return
		}
		if r.URL.Path != "/api/embed" || r.Header.Get("Authorization") != "" {
			t.Error("wrong route or unexpected credentials")
		}
		var input struct {
			Input    []string
			Truncate bool
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil || len(input.Input) != 1 || input.Truncate {
			t.Error("unbounded or truncated input")
		}
		switch mode {
		case "bad-dim":
			json.NewEncoder(w).Encode(map[string]any{"model": "test", "embeddings": [][]float32{{1}}})
		case "zero":
			json.NewEncoder(w).Encode(map[string]any{"model": "test", "embeddings": [][]float32{{0, 0}}})
		case "wrong-model":
			json.NewEncoder(w).Encode(map[string]any{"model": "other", "embeddings": [][]float32{{3, 4}}})
		default:
			json.NewEncoder(w).Encode(map[string]any{"model": "test", "embeddings": [][]float32{{3, 4}}})
		}
	}))
	defer server.Close()
	config := ollama.Config{URL: server.URL, Model: "test", Revision: strings.Repeat("a", 64), Dimensions: 2}
	client, err := ollama.New(config)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.Embed(context.Background(), []string{"hello"}, true)
	if err != nil || result[0][0] != 0.6 || result[0][1] != 0.8 {
		t.Fatal(result, err)
	}
	result[0][0] = 99
	cached, err := client.Embed(context.Background(), []string{"hello"}, true)
	if err != nil || cached[0][0] != 0.6 || calls.Load() != 1 {
		t.Fatal("query cache changed bytes or repeated inference", err)
	}
	for _, value := range []string{"redirect", "bad-dim", "zero", "wrong-model", "changed-digest"} {
		mode = value
		text := value
		if value == "changed-digest" {
			text = "hello"
		}
		if _, err := client.Embed(context.Background(), []string{text}, true); err == nil {
			t.Fatal("invalid response accepted", mode)
		}
	}
	config.URL = "http://user:secret@localhost"
	if _, err := ollama.New(config); err == nil {
		t.Fatal("credential URL accepted")
	}
}
