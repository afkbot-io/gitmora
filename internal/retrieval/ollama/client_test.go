package ollama_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

func TestColdModelsWarmAfterDeadlineWithoutRetainingSourceText(t *testing.T) {
	for _, query := range []bool{true, false} {
		name := "indexing"
		if query {
			name = "query"
		}
		t.Run(name, func(t *testing.T) {
			var loaded atomic.Bool
			var warmCalls atomic.Int32
			started, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
			canceled := make(chan struct{}, 1)
			queryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/tags" {
					json.NewEncoder(w).Encode(map[string]any{"models": []map[string]string{{"name": "test", "digest": strings.Repeat("a", 64)}}})
					return
				}
				var input struct{ Input []string }
				if r.URL.Path != "/api/embed" || json.NewDecoder(r.Body).Decode(&input) != nil || len(input.Input) != 1 {
					t.Error("unexpected model request")
					w.WriteHeader(400)
					return
				}
				if input.Input[0] == "query: Gitmora model warmup" {
					if warmCalls.Add(1) == 1 {
						close(started)
					} else {
						t.Error("concurrent timeouts created multiple warmups")
					}
					select {
					case <-release:
						loaded.Store(true)
					case <-r.Context().Done():
						canceled <- struct{}{}
						return
					}
					defer close(finished)
				} else if !loaded.Load() {
					<-r.Context().Done()
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"model": "test", "embeddings": [][]float32{{3, 4}}})
			}))
			defer queryServer.Close()
			defer close(release)
			background := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				t.Error("warmup contacted the wrong endpoint")
				w.WriteHeader(500)
			}))
			defer background.Close()
			config := ollama.Config{URL: background.URL, QueryURL: queryServer.URL, Model: "test", Revision: strings.Repeat("a", 64), Dimensions: 2, QueryPrefix: "query: "}
			if !query {
				config.URL, config.QueryURL = queryServer.URL, background.URL
			}
			client, err := ollama.New(config)
			if err != nil {
				t.Fatal(err)
			}
			parent, cancelParent := context.WithCancel(context.Background())
			defer cancelParent()
			ctx, cancel := context.WithTimeout(parent, 50*time.Millisecond)
			defer cancel()
			result := make(chan error, 1)
			go func() {
				_, err := client.Embed(ctx, []string{"private first question"}, query)
				result <- err
			}()
			select {
			case err := <-result:
				if err == nil {
					t.Fatal("cold inference unexpectedly completed")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("warmup blocked the search deadline")
			}
			select {
			case <-started:
			case <-time.After(2 * time.Second):
				t.Fatal("timeout did not initiate a bounded warmup")
			}
			cancelParent()
			var concurrent sync.WaitGroup
			for i := 0; i < 8; i++ {
				concurrent.Go(func() {
					short, done := context.WithTimeout(context.Background(), 50*time.Millisecond)
					defer done()
					if _, err := client.Embed(short, []string{"another private question"}, query); err == nil {
						t.Error("blocked cold request unexpectedly completed")
					}
				})
			}
			concurrent.Wait()
			if warmCalls.Load() != 1 {
				t.Fatal("warmup was not coalesced")
			}
			select {
			case <-canceled:
				t.Fatal("request cancellation canceled model warmup")
			default:
			}
			// Release exactly once, including cleanup paths above.
			release <- struct{}{}
			select {
			case <-finished:
			case <-time.After(2 * time.Second):
				t.Fatal("warm model did not finish")
			}
			fresh, done := context.WithTimeout(context.Background(), time.Second)
			defer done()
			vectors, err := client.Embed(fresh, []string{"an uncached fresh question"}, query)
			if err != nil || len(vectors) != 1 || vectors[0][0] != 0.6 {
				t.Fatal("fresh search did not recover after cold start", err)
			}

		})
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
