package postgres_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"workgraph.local/platform/internal/access"
	captureapp "workgraph.local/platform/internal/capture/application"
	forgeapp "workgraph.local/platform/internal/forge/application"
	"workgraph.local/platform/internal/forge/domain"
	"workgraph.local/platform/internal/forge/gitlab"
	"workgraph.local/platform/internal/platform/postgres"
	"workgraph.local/platform/internal/platform/secretbox"
	"workgraph.local/platform/internal/transport/httpapi"
)

func forgeFixture(t *testing.T) (*forgeapp.Service, *postgres.ForgeStore, *pgxpool.Pool, *atomic.Bool) {
	t.Helper()
	pool := database(t)
	seed(t, pool)
	var available atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != "synthetic-provider-token" {
			w.WriteHeader(401)
			return
		}
		if r.URL.Path == "/api/v4/projects/42" {
			_, _ = w.Write([]byte(`{"id":42}`))
			return
		}
		if r.URL.Path == "/api/v4/projects/42/merge_requests" || r.URL.Path == "/api/v4/projects/42/pipelines" || r.URL.Path == "/api/v4/projects/42/deployments" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		sha := strings.TrimPrefix(r.URL.Path, "/api/v4/projects/42/repository/commits/")
		if (sha == strings.Repeat("a", 40) || available.Load()) && len(sha) == 40 {
			_ = json.NewEncoder(w).Encode(map[string]string{"id": sha})
			return
		}
		w.WriteHeader(404)
	}))
	t.Cleanup(server.Close)
	if _, err := pool.Exec(context.Background(), "UPDATE repositories SET gitlab_url=$1 WHERE id=$2", server.URL, repository); err != nil {
		t.Fatal(err)
	}
	vault, err := secretbox.New("test", map[string][]byte{"test": bytes.Repeat([]byte{1}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	provider, err := gitlab.NewWithProxy([]string{server.URL}, "")
	if err != nil {
		t.Fatal(err)
	}
	store := postgres.NewForgeStore(pool)
	service := forgeapp.NewService(store, vault, provider, time.Now)
	return service, store, pool, &available
}
func providerSecrets() domain.Secrets {
	return domain.Secrets{APIToken: "synthetic-provider-token", SigningToken: "whsec_" + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32))}
}
func signedDelivery(body, id string) domain.Delivery {
	stamp := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, bytes.Repeat([]byte{3}, 32))
	_, _ = mac.Write([]byte(id + "." + stamp + "." + body))
	return domain.Delivery{ID: id, Timestamp: stamp, Signature: "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil)), Event: "Push Hook", Body: []byte(body)}
}

func TestForgeHTTPEncryptedSetupDurableWebhookAndProviderConfirmation(t *testing.T) {
	service, _, pool, available := forgeFixture(t)
	ctx := context.Background()
	if _, err := service.Connect(ctx, subject, repository, providerSecrets()); !errors.Is(err, access.ErrDenied) {
		t.Fatal("writer configured provider")
	}
	if _, err := pool.Exec(ctx, "UPDATE memberships SET role='admin' WHERE user_id=$1", subject); err != nil {
		t.Fatal(err)
	}
	tokens := postgres.NewCredentialStore(pool)
	issued, err := access.IssueCredential(subject, "Integration", []access.Permission{access.ReadCapture, access.WriteCapture, access.ReadIntegration, access.ManageIntegration}, time.Now(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := tokens.Save(ctx, issued.Record); err != nil {
		t.Fatal(err)
	}
	captures := captureapp.NewService(postgres.NewCaptureStore(pool))
	if _, err := captures.Ingest(ctx, subject, snapshot()); err != nil {
		t.Fatal(err)
	}
	handler := httpapi.NewHandler(httpapi.Config{Captures: captures, Authenticator: tokens, Ready: pool.Ping, Forge: service})
	perform := func(method, path string, body []byte, delivery *domain.Delivery) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, bytes.NewReader(body))
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
	raw, _ := json.Marshal(providerSecrets())
	route := "/v1/repositories/" + repository + "/gitlab"
	if w := perform("PUT", route, raw, nil); w.Code != 200 || strings.Contains(w.Body.String(), "synthetic-provider-token") {
		t.Fatalf("setup: %d", w.Code)
	}
	var encrypted []byte
	if err := pool.QueryRow(ctx, "SELECT secret_envelope FROM forge_connections WHERE repository_id=$1", repository).Scan(&encrypted); err != nil || bytes.Contains(encrypted, []byte("synthetic-provider-token")) {
		t.Fatal("provider secret not encrypted")
	}
	if worked, err := service.ReconcileOnce(ctx); err != nil || !worked {
		t.Fatal("periodic reconciliation required a webhook", err)
	}
	stored, err := captures.Get(ctx, subject, repository, snapshot().SessionID, 0)
	if err != nil || len(stored.ConfirmedCommits) != 1 {
		t.Fatal("provider response not reflected")
	}
	if _, err := pool.Exec(ctx, `INSERT INTO repositories(id,project_id,gitlab_url,gitlab_project_id,name) SELECT $1,project_id,gitlab_url,43,'Other' FROM repositories WHERE id=$2`, otherRepository, repository); err != nil {
		t.Fatal(err)
	}
	other := snapshot()
	other.RepositoryID = otherRepository
	if _, err := captures.Ingest(ctx, subject, other); err != nil {
		t.Fatal(err)
	}
	otherStored, err := captures.Get(ctx, subject, otherRepository, other.SessionID, 0)
	if err != nil || len(otherStored.ConfirmedCommits) != 0 {
		t.Fatal("same SHA in another repository inherited confirmation")
	}
	input := snapshot()
	input.Revision = 2
	input.Commits = append(input.Commits, strings.Repeat("b", 40))
	if _, err := captures.Ingest(ctx, subject, input); err != nil {
		t.Fatal(err)
	}
	body := `{"object_kind":"push","project_id":42,"after":"` + strings.Repeat("b", 40) + `","variables":[{"key":"TOKEN","value":"never-store-webhook-variable"}]}`
	delivery := signedDelivery(body, "delivery-1")
	hook := "/v1/integrations/gitlab/" + repository + "/webhook"
	for range 2 {
		if w := perform("POST", hook, delivery.Body, &delivery); w.Code != 202 {
			t.Fatalf("webhook: %d %s", w.Code, w.Body)
		}
	}
	changed := signedDelivery(strings.Replace(body, `"TOKEN"`, `"OTHER"`, 1), "delivery-1")
	if w := perform("POST", hook, changed.Body, &changed); w.Code != 409 {
		t.Fatal("changed delivery identity accepted")
	}
	cross := signedDelivery(strings.Replace(body, `"project_id":42`, `"project_id":43`, 1), "cross-project")
	if w := perform("POST", hook, cross.Body, &cross); w.Code != 401 {
		t.Fatal("cross-project webhook accepted")
	}
	if count(t, pool, "forge_webhook_inbox") != 1 {
		t.Fatal("duplicate inbox event")
	}
	var inboxJSON string
	if err := pool.QueryRow(ctx, "SELECT row_to_json(i)::text FROM forge_webhook_inbox i LIMIT 1").Scan(&inboxJSON); err != nil || strings.Contains(inboxJSON, "never-store-webhook-variable") {
		t.Fatal("raw provider variables persisted")
	}
	stored, err = captures.Get(ctx, subject, repository, input.SessionID, 0)
	if err != nil || len(stored.ConfirmedCommits) != 1 {
		t.Fatal("webhook confirmed an unverified commit")
	}
	available.Store(true)
	if worked, err := service.ReconcileOnce(ctx); err != nil || !worked {
		t.Fatal("webhook did not wake reconciliation", err)
	}
	stored, err = captures.Get(ctx, subject, repository, input.SessionID, 0)
	if err != nil || len(stored.ConfirmedCommits) != 2 {
		t.Fatal("second provider commit not confirmed")
	}
	if w := perform("DELETE", route, nil, nil); w.Code != 204 {
		t.Fatal("disable failed")
	}
	if w := perform("POST", hook, delivery.Body, &delivery); w.Code != 401 {
		t.Fatal("disabled integration accepted webhook")
	}
}

func TestWorkerLeaseFencesRotationExpiryAndConcurrentClaims(t *testing.T) {
	service, store, pool, _ := forgeFixture(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, "UPDATE memberships SET role='admin' WHERE user_id=$1", subject); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Connect(ctx, subject, repository, providerSecrets()); err != nil {
		t.Fatal(err)
	}
	if _, err := captureapp.NewService(postgres.NewCaptureStore(pool)).Ingest(ctx, subject, snapshot()); err != nil {
		t.Fatal(err)
	}
	old, found, err := store.Claim(ctx)
	if err != nil || !found {
		t.Fatal("no initial lease")
	}
	if _, found, err := store.Claim(ctx); err != nil || found {
		t.Fatal("same live connection claimed twice")
	}
	if _, err := service.Connect(ctx, subject, repository, providerSecrets()); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkCommit(ctx, old, strings.Repeat("a", 40), true); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("old generation wrote provider evidence")
	}
	current, found, err := store.Claim(ctx)
	if err != nil || !found || current.Generation == old.Generation {
		t.Fatal("rotation did not fence old generation")
	}
	if _, err := pool.Exec(ctx, "UPDATE forge_connections SET lease_until=now()-interval '1 second' WHERE repository_id=$1", repository); err != nil {
		t.Fatal(err)
	}
	recovered, found, err := store.Claim(ctx)
	if err != nil || !found || recovered.Owner == current.Owner {
		t.Fatal("expired lease not recovered")
	}
	if err := store.MarkCommit(ctx, current, strings.Repeat("a", 40), true); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("expired owner wrote provider evidence")
	}
	if err := service.Disconnect(ctx, subject, repository); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkCommit(ctx, recovered, strings.Repeat("a", 40), true); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("disabled provider wrote evidence")
	}
}

func TestReconciliationPagesAllPendingCommitsAndKeepsMissingObjectsPending(t *testing.T) {
	service, _, pool, available := forgeFixture(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, "UPDATE memberships SET role='admin' WHERE user_id=$1", subject); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Connect(ctx, subject, repository, providerSecrets()); err != nil {
		t.Fatal(err)
	}
	input := snapshot()
	input.Commits = nil
	for i := 1; i <= 35; i++ {
		input.Commits = append(input.Commits, fmt.Sprintf("%040x", i))
	}
	if _, err := captureapp.NewService(postgres.NewCaptureStore(pool)).Ingest(ctx, subject, input); err != nil {
		t.Fatal(err)
	}
	available.Store(true)
	for i := 0; i < 3; i++ {
		if worked, err := service.ReconcileOnce(ctx); err != nil || !worked {
			t.Fatal("paged verification stopped early", err)
		}
	}
	stored, err := captureapp.NewService(postgres.NewCaptureStore(pool)).Get(ctx, subject, repository, input.SessionID, 0)
	if err != nil || len(stored.ConfirmedCommits) != 35 {
		t.Fatal("pending commit page omitted objects")
	}
	input.Revision = 2
	input.Commits = append(input.Commits, strings.Repeat("b", 40))
	if _, err := captureapp.NewService(postgres.NewCaptureStore(pool)).Ingest(ctx, subject, input); err != nil {
		t.Fatal(err)
	}
	available.Store(false)
	if err := service.RequestSync(ctx, subject, repository); err != nil {
		t.Fatal(err)
	}
	if worked, err := service.ReconcileOnce(ctx); err != nil || !worked {
		t.Fatal(err)
	}
	stored, err = captureapp.NewService(postgres.NewCaptureStore(pool)).Get(ctx, subject, repository, input.SessionID, 0)
	if err != nil || len(stored.ConfirmedCommits) != 35 {
		t.Fatal("missing provider object was falsely confirmed")
	}
	available.Store(true)
	if err := service.RequestSync(ctx, subject, repository); err != nil {
		t.Fatal(err)
	}
	if worked, err := service.ReconcileOnce(ctx); err != nil || !worked {
		t.Fatal(err)
	}
	stored, err = captureapp.NewService(postgres.NewCaptureStore(pool)).Get(ctx, subject, repository, input.SessionID, 0)
	if err != nil || len(stored.ConfirmedCommits) != 36 {
		t.Fatal("explicit retry did not recheck a previously missing provider object")
	}
}

func TestProviderCredentialFailureIsBlockedVisibleAndExplicitlyRetryable(t *testing.T) {
	service, store, pool, _ := forgeFixture(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, "UPDATE memberships SET role='admin' WHERE user_id=$1", subject); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Connect(ctx, subject, repository, providerSecrets()); err != nil {
		t.Fatal(err)
	}
	box, err := secretbox.New("test", map[string][]byte{"test": bytes.Repeat([]byte{1}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	expired := providerSecrets()
	expired.APIToken = "expired-provider-token"
	raw, _ := json.Marshal(expired)
	sealed, err := box.Seal("gitlab:"+repository, raw)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE forge_connections SET secret_envelope=$2 WHERE repository_id=$1", repository, sealed); err != nil {
		t.Fatal(err)
	}
	if worked, err := service.ReconcileOnce(ctx); !worked || err == nil {
		t.Fatal("expired provider credential was not surfaced")
	}
	status, err := service.Status(ctx, subject, repository)
	if err != nil || !status.Blocked || status.LastErrorCode != "provider_unauthorized" {
		t.Fatal("credential failure health is not actionable")
	}
	if _, found, err := store.Claim(ctx); err != nil || found {
		t.Fatal("blocked credential was retried automatically")
	}
	if _, err := service.Connect(ctx, subject, repository, providerSecrets()); err != nil {
		t.Fatal(err)
	}
	if worked, err := service.ReconcileOnce(ctx); err != nil || !worked {
		t.Fatal("credential replacement did not resume verification")
	}
	status, err = service.Status(ctx, subject, repository)
	if err != nil || status.Blocked || status.LastErrorCode != "" {
		t.Fatal("successful retry retained stale failure state")
	}
}

func TestConcurrentWebhookRetriesAndWorkerClaimsHaveOneDurableEffect(t *testing.T) {
	service, store, pool, _ := forgeFixture(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, "UPDATE memberships SET role='admin' WHERE user_id=$1", subject); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Connect(ctx, subject, repository, providerSecrets()); err != nil {
		t.Fatal(err)
	}
	delivery := signedDelivery(`{"object_kind":"push","project_id":42,"after":"`+strings.Repeat("a", 40)+`"}`, "concurrent-delivery")
	var group sync.WaitGroup
	var created atomic.Int32
	failures := make(chan error, 12)
	for range 12 {
		group.Add(1)
		go func() {
			defer group.Done()
			fresh, err := service.Receive(ctx, repository, delivery)
			if err != nil {
				failures <- err
			}
			if fresh {
				created.Add(1)
			}
		}()
	}
	group.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	var sequence int64
	if err := pool.QueryRow(ctx, "SELECT requested_seq FROM forge_connections WHERE repository_id=$1", repository).Scan(&sequence); err != nil || sequence != 2 || created.Load() != 1 {
		t.Fatal("retry duplicated inbox scheduling")
	}
	var claimed atomic.Int32
	failures = make(chan error, 12)
	for range 12 {
		group.Add(1)
		go func() {
			defer group.Done()
			_, found, err := store.Claim(ctx)
			if err != nil {
				failures <- err
			}
			if found {
				claimed.Add(1)
			}
		}()
	}
	group.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	if claimed.Load() != 1 {
		t.Fatal("concurrent workers leased the same connection")
	}
}
