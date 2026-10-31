package postgres_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"workgraph.local/platform/internal/access"
	captureapp "workgraph.local/platform/internal/capture/application"
	"workgraph.local/platform/internal/forge/domain"
	"workgraph.local/platform/internal/platform/postgres"
	"workgraph.local/platform/internal/transport/httpapi"
)

func TestForgeAPIOnlyHTTPKeepsPollingAndFencesOldWebhookAndWorker(t *testing.T) {
	service, store, pool, available := forgeFixture(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, "UPDATE memberships SET role='admin' WHERE user_id=$1", subject); err != nil {
		t.Fatal(err)
	}
	before, err := service.Connect(ctx, subject, repository, providerSecrets())
	if err != nil {
		t.Fatal(err)
	}
	oldConnection, err := store.WebhookConnection(ctx, repository)
	if err != nil {
		t.Fatal(err)
	}
	oldLease, found, err := store.Claim(ctx)
	if err != nil || !found {
		t.Fatal("missing old worker lease", err)
	}
	tokens := postgres.NewCredentialStore(pool)
	issued, err := access.IssueCredential(subject, "API-only administration", []access.Permission{access.ReadIntegration, access.ManageIntegration}, time.Now(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := tokens.Save(ctx, issued.Record); err != nil {
		t.Fatal(err)
	}
	captures := captureapp.NewService(postgres.NewCaptureStore(pool))
	handler := httpapi.NewHandler(httpapi.Config{Captures: captures, Authenticator: tokens, Ready: pool.Ping, Forge: service})
	route := "/v1/repositories/" + repository + "/gitlab"
	perform := func(method, path string, raw []byte, delivery *domain.Delivery) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, bytes.NewReader(raw))
		r.Header.Set("Content-Type", "application/json")
		if delivery == nil {
			r.Header.Set("Authorization", "Bearer "+issued.Secret)
		} else {
			r.Header.Set("Webhook-Id", delivery.ID)
			r.Header.Set("Webhook-Timestamp", delivery.Timestamp)
			r.Header.Set("Webhook-Signature", delivery.Signature)
			r.Header.Set("X-Gitlab-Event", delivery.Event)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	config := []byte(`{"api_token":"synthetic-provider-token","webhook_mode":"disabled"}`)
	w := perform("PUT", route, config, nil)
	if w.Code != 200 {
		t.Fatalf("API-only setup: %d", w.Code)
	}
	var status struct {
		Generation  int64  `json:"generation"`
		WebhookMode string `json:"webhook_mode"`
		WebhookPath string `json:"webhook_path"`
	}
	if json.Unmarshal(w.Body.Bytes(), &status) != nil || status.Generation != before.Generation+1 || status.WebhookMode != "disabled" || status.WebhookPath != "" {
		t.Fatal("API-only status advertised a webhook or lost generation")
	}
	if bytes.Contains(w.Body.Bytes(), []byte("synthetic-provider-token")) {
		t.Fatal("setup disclosed provider secret")
	}
	var encrypted []byte
	if err := pool.QueryRow(ctx, "SELECT secret_envelope FROM forge_connections WHERE repository_id=$1", repository).Scan(&encrypted); err != nil || bytes.Contains(encrypted, []byte("synthetic-provider-token")) {
		t.Fatal("API-only secret was not encrypted")
	}
	if _, err := store.WebhookConnection(ctx, repository); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("disabled webhook retrieved a credential envelope", err)
	}
	body := `{"object_kind":"push","project_id":42,"after":"` + strings.Repeat("a", 40) + `"}`
	delivery := signedDelivery(body, "before-api-only")
	event, err := domain.ParseEvent(delivery, 42)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Accept(ctx, oldConnection, event); !errors.Is(err, domain.ErrUnauthenticated) {
		t.Fatal("pre-switch authenticated webhook wrote after API-only replacement", err)
	}
	if err := store.MarkCommit(ctx, oldLease, strings.Repeat("a", 40), true); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("pre-switch worker wrote after API-only replacement", err)
	}
	if w := perform("POST", before.WebhookPath, delivery.Body, &delivery); w.Code != 401 {
		t.Fatal("old signed webhook accepted by API-only connection", w.Code)
	}
	if count(t, pool, "forge_webhook_inbox") != 0 {
		t.Fatal("disabled webhook left durable input")
	}
	input := snapshot()
	input.Commits = append(input.Commits, strings.Repeat("b", 40))
	if _, err := captures.Ingest(ctx, subject, input); err != nil {
		t.Fatal(err)
	}
	if worked, err := service.ReconcileOnce(ctx); err != nil || !worked {
		t.Fatal("API-only worker required a webhook", err)
	}
	stored, err := captures.Get(ctx, subject, repository, input.SessionID, 0)
	if err != nil || len(stored.ConfirmedCommits) != 1 {
		t.Fatal("API-only provider verification lost exact confirmation", err)
	}
	available.Store(true)
	// Advance persisted due times instead of waiting for a real five-minute period.
	if _, err := pool.Exec(ctx, "UPDATE forge_connections SET next_sync_at=now(); UPDATE commits SET verify_after=now()"); err != nil {
		t.Fatal(err)
	}
	if worked, err := service.ReconcileOnce(ctx); err != nil || !worked {
		t.Fatal("periodic API-only retry required webhook/manual sync", err)
	}
	stored, err = captures.Get(ctx, subject, repository, input.SessionID, 0)
	if err != nil || len(stored.ConfirmedCommits) != 2 {
		t.Fatal("periodic API-only retry failed to confirm newly visible object", err)
	}
	raw, _ := json.Marshal(providerSecrets())
	if w := perform("PUT", route, raw, nil); w.Code != 200 {
		t.Fatal("signature mode could not be restored", w.Code)
	}
	if w := perform("POST", before.WebhookPath, delivery.Body, &delivery); w.Code != 202 {
		t.Fatal("explicitly restored webhook failed", w.Code)
	}
}
