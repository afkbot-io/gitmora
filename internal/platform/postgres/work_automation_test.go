package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	captureapp "workgraph.local/platform/internal/capture/application"
	capture "workgraph.local/platform/internal/capture/domain"
	forge "workgraph.local/platform/internal/forge/domain"
	"workgraph.local/platform/internal/platform/postgres"
	"workgraph.local/platform/internal/platform/secretbox"
	"workgraph.local/platform/internal/work/application"
	"workgraph.local/platform/internal/work/domain"
)

func automationFixture(t *testing.T) (*pgxpool.Pool, *postgres.WorkStore, *application.Service, domain.Config) {
	t.Helper()
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `UPDATE memberships SET role='admin' WHERE user_id=$1`, subject); err != nil {
		t.Fatal(err)
	}
	snap := snapshot()
	snap.Task = &capture.TaskRef{CountryID: taskCountry, TaskID: taskUUID}
	if _, err := captureapp.NewService(postgres.NewCaptureStore(pool)).Ingest(ctx, subject, snap); err != nil {
		t.Fatal(err)
	}
	store := postgres.NewWorkStore(pool)
	vault, _ := secretbox.New("test", map[string][]byte{"test": make([]byte, 32)})
	remote := &taskProvider{task: domain.Task{CountryID: taskCountry, ID: taskUUID, Number: 4, Title: "Bound task", Status: "IN_PROGRESS", Progress: 20, UpdatedAt: time.Now(), URL: "https://tasks.internal/task/4?countryId=" + taskCountry + "&taskId=" + taskUUID}}
	service := application.NewService(store, vault, remote)
	config := domain.Config{Endpoint: "https://tasks.internal/mcp", CountryID: taskCountry, Token: "synthetic-service-token"}
	if _, err := service.Connect(ctx, subject, project, config); err != nil {
		t.Fatal(err)
	}
	return pool, store, service, config
}

func TestSameSHAInTargetDoesNotImplyMembershipForAnUnregisteredFork(t *testing.T) {
	pool, _, service, config := automationFixture(t)
	ctx := context.Background()
	seedLinkedMR(t, pool, true, 99)
	if _, err := pool.Exec(ctx, `UPDATE commits SET confirmed_at=now()`); err != nil {
		t.Fatal(err)
	}
	config.AutoLinkMRs = true
	if _, err := service.Connect(ctx, subject, project, config); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if page, err := service.Links(ctx, reader, project, taskUUID); err != nil || len(page.Items) != 0 {
		t.Fatal("target SHA incorrectly matched a fork source", page, err)
	}
	forkRepo := "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	if _, err := pool.Exec(ctx, `INSERT INTO repositories(id,project_id,gitlab_url,gitlab_project_id,name) VALUES($1,$2,'https://gitlab.internal',99,'fork')`, forkRepo, project); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO forge_connections(repository_id,webhook_mode,secret_envelope,updated_by,last_synced_at) VALUES($1,'signature',$2,$3,now())`, forkRepo, make([]byte, 32), subject); err != nil {
		t.Fatal(err)
	}
	snap := snapshot()
	snap.RepositoryID = forkRepo
	snap.Task = &capture.TaskRef{CountryID: taskCountry, TaskID: taskUUID}
	if _, err := captureapp.NewService(postgres.NewCaptureStore(pool)).Ingest(ctx, subject, snap); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE commits SET confirmed_at=now()`); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := service.ReconcileOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	page, err := service.Links(ctx, reader, project, taskUUID)
	if err != nil || len(page.Items) != 1 || page.Items[0].State != "complete" || page.Items[0].Evidence.RepositoryID != forkRepo {
		t.Fatal("registered scoped fork did not link", page, err)
	}
}

func TestQueuedMRLinkRevalidatesAssociationAndPolicyBeforeDelivery(t *testing.T) {
	pool, store, service, config := automationFixture(t)
	ctx := context.Background()
	seedLinkedMR(t, pool, true, 42)
	if _, err := service.ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE commits SET confirmed_at=now()`); err != nil {
		t.Fatal(err)
	}
	config.AutoLinkMRs = true
	if _, err := service.Connect(ctx, subject, project, config); err != nil {
		t.Fatal(err)
	}
	// Refresh runs first while discovery persists the link intent.
	if _, err := service.ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE forge_mr_codes SET complete=false`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	page, err := service.Links(ctx, reader, project, taskUUID)
	if err != nil || len(page.Items) != 1 || page.Items[0].State != "blocked" || page.Items[0].ErrorCode != "association_unavailable" {
		t.Fatal("retired/incomplete association reached delivery", page, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE forge_mr_codes SET complete=true; UPDATE work_connections SET discovery_at=now()`); err != nil {
		t.Fatal(err)
	}
	lease, found, err := store.Claim(ctx)
	if err != nil || !found || lease.Kind != "link" {
		t.Fatal(lease, found, err)
	}
	if err := store.Finish(ctx, lease, application.Outcome{}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal("link completed without remote readback evidence", err)
	}
	changed := lease
	changed.ReadStartedAt = changed.ReadStartedAt.Add(time.Hour)
	if err := store.Finish(ctx, changed, application.Outcome{}); !errors.Is(err, domain.ErrLease) {
		t.Fatal("substituted read timestamp accepted", err)
	}
	changed = lease
	changed.Link.URL = "https://gitlab.internal/other/-/merge_requests/7"
	if err := store.Finish(ctx, changed, application.Outcome{}); !errors.Is(err, domain.ErrLease) {
		t.Fatal("substituted outbound payload accepted", err)
	}
	config.AutoLinkMRs = false
	if _, err := service.Connect(ctx, subject, project, config); err != nil {
		t.Fatal(err)
	}
	if err := store.Finish(ctx, lease, application.Outcome{}); !errors.Is(err, domain.ErrLease) {
		t.Fatal("disabled policy failed to fence worker", err)
	}
	if _, err := service.ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.Claim(ctx); err != nil || found {
		t.Fatal("disabled policy claimed pending link", found, err)
	}
}

func TestCaptureTaskBindingAutomaticallyDiscoversTaskWithoutPublishingLinks(t *testing.T) {
	pool, _, service, _ := automationFixture(t)
	ctx := context.Background()
	if count(t, pool, "work_capture_bindings") != 1 {
		t.Fatal("task binding was not normalized atomically")
	}
	if worked, err := service.ReconcileOnce(ctx); err != nil || !worked {
		t.Fatal(worked, err)
	}
	if view, err := service.Task(ctx, reader, project, taskUUID); err != nil || view.Task.ID != taskUUID {
		t.Fatal(view, err)
	}
	if links, err := service.Links(ctx, reader, project, taskUUID); err != nil || len(links.Items) != 0 {
		t.Fatal(links, err)
	}
}

func seedLinkedMR(t *testing.T, pool *pgxpool.Pool, complete bool, source int64, urls ...string) {
	t.Helper()
	ctx := context.Background()
	sha := strings.Repeat("a", 40)
	m := forge.MergeRequest{ID: 107, IID: 7, ProjectID: 42, SourceProjectID: source, State: "opened", Title: "Source title", URL: "https://gitlab.internal/team/project/-/merge_requests/7", Head: sha, Base: strings.Repeat("b", 40), Start: strings.Repeat("c", 40), UpdatedAt: time.Now().UTC()}
	if len(urls) > 0 {
		m.URL = urls[0]
	}
	raw, _ := json.Marshal(m)
	queries := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO forge_connections(repository_id,webhook_mode,secret_envelope,updated_by,last_synced_at) VALUES($1,'signature',$2,$3,now()) ON CONFLICT(repository_id) DO UPDATE SET last_synced_at=now()`, []any{repository, make([]byte, 32), subject}},
		{`INSERT INTO forge_mr_codes(repository_id,iid,code_key,complete) VALUES($1,7,$2,$3)`, []any{repository, m.CodeKey(), complete}},
		{`INSERT INTO forge_mr_observations(repository_id,iid,digest,code_key,model,provider_updated_at) VALUES($1,7,$2,$3,$4,now())`, []any{repository, m.Digest(), m.CodeKey(), raw}},
		{`INSERT INTO forge_merge_requests(repository_id,iid,digest,provider_updated_at) VALUES($1,7,$2,now())`, []any{repository, m.Digest()}},
		{`INSERT INTO forge_mr_commits(repository_id,iid,code_key,source_project_id,sha) VALUES($1,7,$2,$3,$4)`, []any{repository, m.CodeKey(), source, sha}},
	}
	for _, q := range queries {
		if _, err := pool.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInvalidProviderLinkRemainsBlockedAcrossCredentialRepair(t *testing.T) {
	pool, store, service, config := automationFixture(t)
	ctx := context.Background()
	seedLinkedMR(t, pool, true, 42, "https://gitlab.internal/team/project/-/merge_requests/8")
	if _, err := service.ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE commits SET confirmed_at=now()`); err != nil {
		t.Fatal(err)
	}
	config.AutoLinkMRs = true
	if _, err := service.Connect(ctx, subject, project, config); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	page, err := service.Links(ctx, reader, project, taskUUID)
	if err != nil || len(page.Items) != 1 || page.Items[0].State != "blocked" || page.Items[0].ErrorCode != "link_invalid" || page.Items[0].Link.URL != "" {
		t.Fatal("invalid provider link reached delivery or retained its URL", page, err)
	}
	if _, err := service.Connect(ctx, subject, project, config); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if _, found, err := store.Claim(ctx); err != nil || found {
		t.Fatal("credential repair revived permanently invalid link", found, err)
	}
	if err := service.Refresh(ctx, subject, project, taskUUID); err != nil {
		t.Fatal(err)
	}
	if lease, found, err := store.Claim(ctx); err != nil || !found || lease.Kind != "refresh" {
		t.Fatal("invalid link starved independent task reads", lease, found, err)
	}
}

func TestAutomaticMRLinksRequireOptInConfirmedCommitAndCompleteScopedMembership(t *testing.T) {
	pool, _, service, config := automationFixture(t)
	ctx := context.Background()
	seedLinkedMR(t, pool, false, 42)
	if _, err := service.ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	config.AutoLinkMRs = true
	if _, err := service.Connect(ctx, subject, project, config); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if page, err := service.Links(ctx, reader, project, taskUUID); err != nil || len(page.Items) != 0 {
		t.Fatal("unconfirmed/incomplete MR linked", page, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE commits SET confirmed_at=now(); UPDATE work_connections SET discovery_at=now()`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if page, err := service.Links(ctx, reader, project, taskUUID); err != nil || len(page.Items) != 0 {
		t.Fatal("incomplete membership linked", page, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE forge_mr_codes SET complete=true; UPDATE work_connections SET discovery_at=now()`); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := service.ReconcileOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	page, err := service.Links(ctx, reader, project, taskUUID)
	if err != nil || len(page.Items) != 1 || page.Items[0].State != "complete" || page.Items[0].Link.IID != 7 {
		t.Fatal(page, err)
	}
	if view, err := service.Task(ctx, reader, project, taskUUID); err != nil || !view.Task.HasLink(page.Items[0].Link.URL) {
		t.Fatal(view, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE work_connections SET discovery_at=now()`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if count(t, pool, "work_link_evidence") != 1 {
		t.Fatal("identical evidence duplicated")
	}
}

func TestConcurrentTaskCompletionsDoNotUpgradeSharedConnectionLocks(t *testing.T) {
	pool, store, service, _ := automationFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first, found, err := store.Claim(ctx)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	if err := service.Refresh(ctx, subject, project, taskCountry); err != nil {
		t.Fatal(err)
	}
	second, found, err := store.Claim(ctx)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	if _, err := pool.Exec(ctx, `CREATE FUNCTION pause_task_finish() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_sleep(0.1); RETURN NEW; END $$; CREATE TRIGGER pause_task_finish BEFORE UPDATE ON work_jobs FOR EACH ROW EXECUTE FUNCTION pause_task_finish()`); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	for _, lease := range []application.Lease{first, second} {
		go func() { results <- store.Finish(ctx, lease, application.Outcome{}) }()
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal("concurrent task completion failed", err)
		}
	}
}
