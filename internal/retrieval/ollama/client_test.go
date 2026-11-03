package ollama_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"workgraph.local/platform/internal/retrieval/domain"
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

func TestInteractiveReplicaBypassesBusyBackfillAndVerifiesCachedModel(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var changed atomic.Bool
	digest := strings.Repeat("a", 64)
	respond := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			current := digest
			if changed.Load() {
				current = strings.Repeat("b", 64)
			}
			json.NewEncoder(w).Encode(map[string]any{"models": []map[string]string{{"name": "test", "digest": current}}})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"model": "test", "embeddings": [][]float32{{3, 4}}})
	}
	background := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/embed" {
			close(started)
			<-release
		}
		respond(w, r)
	}))
	defer background.Close()
	defer close(release)
	interactive := httptest.NewServer(http.HandlerFunc(respond))
	defer interactive.Close()
	config := ollama.Config{URL: background.URL, Model: "test", Revision: digest, Dimensions: 2}
	worker, err := ollama.New(config)
	if err != nil {
		t.Fatal(err)
	}
	config.QueryURL = interactive.URL
	reader, err := ollama.New(config)
	if err != nil || reader.Profile() != worker.Profile() {
		t.Fatal("query routing split the persisted vector profile", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		_, err := worker.Embed(ctx, []string{"background passage"}, false)
		finished <- err
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("backfill did not start")
	}
	query, done := context.WithTimeout(ctx, 500*time.Millisecond)
	defer done()
	vectors, err := reader.Embed(query, []string{"interactive question"}, true)
	if err != nil || len(vectors) != 1 {
		t.Fatal("query waited for the blocked backfill", err)
	}
	select {
	case err := <-finished:
		t.Fatal("backfill finished before its release", err)
	default:
	}
	changed.Store(true)
	if _, err := reader.Embed(ctx, []string{"interactive question"}, true); err == nil {
		t.Fatal("cached query ignored a changed replica digest")
	}
}

func TestQueryReplicaOriginAndLegacyProfile(t *testing.T) {
	config := ollama.Config{URL: "http://embeddings:11434", Model: "test", Revision: strings.Repeat("a", 64), Dimensions: 2}
	client, err := ollama.New(config)
	if err != nil {
		t.Fatal(err)
	}
	// A routing-only upgrade must continue reading vectors already persisted by 0.9.0.
	legacy := `{"Version":1,"Config":{"URL":"http://embeddings:11434","Model":"test","Revision":"` + strings.Repeat("a", 64) + `","Dimensions":2,"QueryPrefix":"","PassagePrefix":""}}`
	if client.Profile() != domain.Hash([]byte(legacy)) {
		t.Fatal("version-1 embedding identity changed")
	}
	for _, endpoint := range []string{"http://user:secret@replica", "http://replica/path", "http://replica?key=value", "http://replica#fragment", "file:///model"} {
		config.QueryURL = endpoint
		if _, err := ollama.New(config); err == nil {
			t.Fatal("unsafe query replica origin accepted")
		}
	}
}
