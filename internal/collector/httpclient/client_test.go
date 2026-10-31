package httpclient_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	capture "workgraph.local/platform/internal/capture/domain"
	"workgraph.local/platform/internal/collector/application"
	"workgraph.local/platform/internal/collector/domain"
	"workgraph.local/platform/internal/collector/httpclient"
)

func session(t *testing.T) capture.Session {
	t.Helper()
	s, err := capture.Sanitize(capture.Snapshot{SchemaVersion: 1, RepositoryID: "11111111-1111-4111-8111-111111111111", SessionID: "22222222-2222-4222-8222-222222222222", Revision: 1, Agent: capture.Agent{Name: "local", Model: "local"}, StartedAt: time.Now(), Messages: []capture.Message{{Role: "user", Text: "DB_PASSWORD=not-sent"}}})
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func client(t *testing.T, base string) *httpclient.Client {
	t.Helper()
	c, err := httpclient.New(domain.Config{Version: 1, RepositoryID: "11111111-1111-4111-8111-111111111111", ServerURL: base, Remote: "origin", RemoteURL: "/test.git", TokenEnv: "WORKGRAPH_TOKEN"}, func(context.Context) (string, error) { return "wg_" + strings.Repeat("A", 43), nil })
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestUploaderRequiresExactImmutableAcknowledgement(t *testing.T) {
	for _, variant := range []string{"correct", "wrong_digest", "wrong_session", "missing_created", "trailing_json"} {
		t.Run(variant, func(t *testing.T) {
			snapshot := session(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/captures" || r.Header.Get("Authorization") == "" {
					t.Error("incorrect request contract")
				}
				var input capture.Snapshot
				if json.NewDecoder(r.Body).Decode(&input) != nil {
					t.Error("invalid request JSON")
				}
				if strings.Contains(input.Messages[0].Text, "not-sent") {
					t.Error("secret sent")
				}
				ack := map[string]any{"repository_id": input.RepositoryID, "session_id": input.SessionID, "revision": input.Revision, "digest": snapshot.Digest(), "created": true}
				switch variant {
				case "wrong_digest":
					ack["digest"] = strings.Repeat("0", 64)
				case "wrong_session":
					ack["session_id"] = input.RepositoryID
				case "missing_created":
					delete(ack, "created")
				}
				w.WriteHeader(201)
				_ = json.NewEncoder(w).Encode(ack)
				if variant == "trailing_json" {
					_, _ = w.Write([]byte(`{}`))
				}
			}))
			defer server.Close()
			err := client(t, server.URL).Send(context.Background(), snapshot)
			if (err == nil) != (variant == "correct") {
				t.Fatalf("invalid ACK policy: %v", err)
			}
		})
	}
}

func TestRedirectNeverForwardsTranscriptOrCredential(t *testing.T) {
	var received atomic.Int64
	trap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received.Add(1) }))
	defer trap.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, trap.URL, 307) }))
	defer server.Close()
	if err := client(t, server.URL).Send(context.Background(), session(t)); err == nil {
		t.Fatal("redirect acknowledged")
	}
	if received.Load() != 0 {
		t.Fatal("redirect leaked context")
	}
}

func TestStatusClassificationRetainsRetryableFailures(t *testing.T) {
	for _, status := range []int{401, 403, 409, 422, 500, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(status)
				_, _ = w.Write([]byte("private server diagnostic"))
			}))
			defer server.Close()
			err := client(t, server.URL).Send(context.Background(), session(t))
			delivery, ok := err.(*application.DeliveryError)
			if !ok || strings.Contains(err.Error(), "private") || delivery.Retryable != (status == 401 || status == 403 || status >= 500) {
				t.Fatalf("unsafe classification: %v", err)
			}
		})
	}
}
