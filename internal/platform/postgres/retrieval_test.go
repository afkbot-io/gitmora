package postgres_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
	workapp "workgraph.local/platform/internal/work/application"
	work "workgraph.local/platform/internal/work/domain"

	"workgraph.local/platform/internal/access"
	captureapp "workgraph.local/platform/internal/capture/application"
	"workgraph.local/platform/internal/platform/postgres"
	"workgraph.local/platform/internal/retrieval/application"
	"workgraph.local/platform/internal/retrieval/domain"
)

func TestRetrievalIndexesAtomicallyRanksAndReauthorizesExactHistory(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	captures := captureapp.NewService(postgres.NewCaptureStore(pool))
	snap := snapshot()
	snap.Messages[0].Text = "Исправлена авторизация refreshToken. Historical session observation."
	snap.Files = []string{"src/auth/refresh_token.go"}
	if _, err := captures.Ingest(ctx, subject, snap); err != nil {
		t.Fatal(err)
	}
	store := postgres.NewRetrievalStore(pool)
	indexer := application.NewIndexer(store)
	service := application.NewService(store)
	principal := access.Principal{UserID: reader, Scopes: []access.Permission{access.ReadCapture}}
	query := domain.Query{ProjectID: project, Text: "авторизации"}
	before, err := service.Search(ctx, principal, query)
	if err != nil || len(before.Items) != 0 || before.PendingSources != 1 {
		t.Fatal("unprocessed outbox pretended indexed", before, err)
	}
	if worked, err := indexer.ReconcileOnce(ctx); err != nil || !worked {
		t.Fatal(worked, err)
	}
	got, err := service.Search(ctx, principal, query)
	if err != nil || len(got.Items) != 1 || !got.Items[0].Current {
		t.Fatal(got, err)
	}
	evidence := got.Items[0].ID
	if got.Items[0].Source.Revision != 1 || got.Items[0].Source.Digest == "" {
		t.Fatal("missing provenance")
	}
	if code, err := service.Search(ctx, principal, domain.Query{ProjectID: project, Text: "refresh_token", Kinds: []string{"code_reference"}}); err != nil || len(code.Items) != 0 {
		t.Fatal("pending commit reference published", code, err)
	}
	_, _ = pool.Exec(ctx, "UPDATE commits SET confirmed_at=now() WHERE repository_id=$1", repository)
	if code, err := service.Search(ctx, principal, domain.Query{ProjectID: project, Text: "refreshToken", Kinds: []string{"code_reference"}}); err != nil || len(code.Items) != 1 || code.Items[0].ConfirmedCount != 1 {
		t.Fatal("verified relation missing", code, err)
	}
	var completed int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM outbox_events WHERE completed_at IS NOT NULL").Scan(&completed); err != nil {
		t.Fatal(err)
	}
	if completed != 1 {
		t.Fatal("index acknowledgment not durable")
	}
	snap.Revision = 2
	snap.Messages[0].Text = "Current implementation has changed."
	if _, err := captures.Ingest(ctx, subject, snap); err != nil {
		t.Fatal(err)
	}
	if _, err := indexer.ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	latest, err := service.Search(ctx, principal, query)
	if err != nil || len(latest.Items) != 0 {
		t.Fatal("older revision presented as current", latest, err)
	}
	historical, err := service.Expand(ctx, principal, domain.Expansion{EvidenceID: evidence})
	if err != nil || historical.Current || !strings.Contains(historical.Content, "авторизация") {
		t.Fatal(historical, err)
	}
	_, _ = pool.Exec(ctx, "DELETE FROM memberships WHERE user_id=$1", reader)
	if _, err := service.Search(ctx, principal, query); !errors.Is(err, access.ErrDenied) {
		t.Fatal("revoked search", err)
	}
	if _, err := service.Expand(ctx, principal, domain.Expansion{EvidenceID: evidence}); !errors.Is(err, domain.ErrNotFound) && !errors.Is(err, access.ErrDenied) {
		t.Fatal("revoked expansion", err)
	}
}

func TestRetrievalDoesNotRankForeignSourcesBeforeApplyingAccess(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	const foreignProject = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	_, err := pool.Exec(ctx, `INSERT INTO projects(id,name) VALUES($1,'Restricted')`, foreignProject)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO memberships(project_id,user_id,role) VALUES($1,$2,'writer')`, foreignProject, subject)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO repositories(id,project_id,gitlab_url,gitlab_project_id,name) VALUES($1,$2,'https://gitlab.internal',99,'Restricted')`, otherRepository, foreignProject)
	if err != nil {
		t.Fatal(err)
	}
	captureService := captureapp.NewService(postgres.NewCaptureStore(pool))
	source := snapshot()
	source.Messages[0].Text = "A lower ranked needle in an authorized source."
	if _, err = captureService.Ingest(ctx, subject, source); err != nil {
		t.Fatal(err)
	}
	foreign := source
	foreign.RepositoryID = otherRepository
	foreign.Agent.Name = "needle"
	foreign.Messages[0].Text = strings.Repeat("needle unauthorized-private-evidence ", 80)
	if _, err = captureService.Ingest(ctx, subject, foreign); err != nil {
		t.Fatal(err)
	}
	store := postgres.NewRetrievalStore(pool)
	indexer := application.NewIndexer(store)
	for i := 0; i < 2; i++ {
		if worked, err := indexer.ReconcileOnce(ctx); err != nil || !worked {
			t.Fatal(worked, err)
		}
	}
	service := application.NewService(store)
	principal := access.Principal{UserID: reader, Scopes: []access.Permission{access.ReadCapture}}
	result, err := service.Search(ctx, principal, domain.Query{ProjectID: project, Text: "needle", Limit: 1})
	if err != nil || len(result.Items) != 1 || result.Items[0].Source.RepositoryID != repository || strings.Contains(result.Items[0].Content, "unauthorized-private-evidence") {
		t.Fatal(result, err)
	}
	if _, err = service.Search(ctx, principal, domain.Query{ProjectID: foreignProject, Text: "needle"}); !errors.Is(err, access.ErrDenied) {
		t.Fatal("cross-project search", err)
	}
	if _, err = service.Search(ctx, principal, domain.Query{ProjectID: project, RepositoryID: otherRepository, Text: "needle"}); !errors.Is(err, access.ErrDenied) {
		t.Fatal("repository scope escaped project", err)
	}
	// Current catalog ownership must be rechecked even when the old index still exists.
	if _, err = pool.Exec(ctx, `UPDATE repositories SET project_id=$2 WHERE id=$1`, repository, foreignProject); err != nil {
		t.Fatal(err)
	}
	moved, err := service.Search(ctx, principal, domain.Query{ProjectID: project, Text: "needle"})
	if err != nil || len(moved.Items) != 0 {
		t.Fatal("stale index leaked a reassigned repository", moved, err)
	}
	if _, err = service.Expand(ctx, principal, domain.Expansion{EvidenceID: result.Items[0].ID}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("old citation bypassed current catalog scope", err)
	}
}

func TestRetrievalLeaseRecoveryFencesExpiredOwnersAndSourceSubstitution(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	if _, err := captureapp.NewService(postgres.NewCaptureStore(pool)).Ingest(ctx, subject, snapshot()); err != nil {
		t.Fatal(err)
	}
	store := postgres.NewRetrievalStore(pool)
	first, found, err := store.Claim(ctx)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	if _, found, err := store.Claim(ctx); err != nil || found {
		t.Fatal("leased source claimed twice", found, err)
	}
	changed := first
	changed.Source.Digest = strings.Repeat("f", 64)
	if err := store.Finish(ctx, changed, nil, ""); !errors.Is(err, domain.ErrLease) {
		t.Fatal("source substituted", err)
	}
	_, err = pool.Exec(ctx, `UPDATE retrieval_jobs SET lease_until=now()-interval '1 second' WHERE id=$1`, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	next, found, err := store.Claim(ctx)
	if err != nil || !found || next.Owner == first.Owner {
		t.Fatal(found, err)
	}
	if err := store.Finish(ctx, first, nil, ""); !errors.Is(err, domain.ErrLease) {
		t.Fatal("expired owner committed", err)
	}
	if err := store.Finish(ctx, next, nil, "index_source_unavailable"); err != nil {
		t.Fatal(err)
	}
	service := application.NewService(store)
	principal := access.Principal{UserID: reader, Scopes: []access.Permission{access.ReadCapture}}
	result, err := service.Search(ctx, principal, domain.Query{ProjectID: project, Text: "something"})
	if err != nil || result.PendingSources != 1 || result.FailedSources != 1 {
		t.Fatal("index failure not visible", result, err)
	}
	var completed int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM outbox_events WHERE completed_at IS NOT NULL").Scan(&completed); err != nil || completed != 0 {
		t.Fatal("failed indexing acknowledged", err)
	}
	_, _ = pool.Exec(ctx, `UPDATE retrieval_jobs SET available_at=now() WHERE id=$1`, first.ID)
	if worked, err := application.NewIndexer(postgres.NewRetrievalStore(pool)).ReconcileOnce(ctx); err != nil || !worked {
		t.Fatal("restarted indexer did not recover", worked, err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM capture_sessions WHERE repository_id=$1`, repository); err != nil {
		t.Fatal(err)
	}
	if count(t, pool, "retrieval_chunks") != 0 || count(t, pool, "retrieval_jobs") != 0 {
		t.Fatal("deleted source projection retained")
	}
}

func TestRetrievalQueueRollback(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	captures := captureapp.NewService(postgres.NewCaptureStore(pool))
	if _, err := pool.Exec(ctx, `ALTER TABLE retrieval_jobs ADD CONSTRAINT simulated_queue_failure CHECK(false) NOT VALID`); err != nil {
		t.Fatal(err)
	}
	if _, err := captures.Ingest(ctx, subject, snapshot()); err == nil {
		t.Fatal("missing transactional indexing work accepted")
	}
	for _, table := range []string{"capture_revisions", "capture_sessions", "outbox_events", "retrieval_jobs"} {
		if count(t, pool, table) != 0 {
			t.Fatal("partial capture acceptance", table)
		}
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE retrieval_jobs DROP CONSTRAINT simulated_queue_failure`); err != nil {
		t.Fatal(err)
	}
	source := snapshot()
	source.Messages[0].Text = "Backfilled searchable context"
	if _, err := captures.Ingest(ctx, subject, source); err != nil {
		t.Fatal(err)
	}
	// A pre-retrieval database upgrade is covered separately with actual legacy migrations.
	if count(t, pool, "retrieval_jobs") != 1 {
		t.Fatal("old source not backfilled")
	}
	if worked, err := application.NewIndexer(postgres.NewRetrievalStore(pool)).ReconcileOnce(ctx); err != nil || !worked {
		t.Fatal(worked, err)
	}
	result, err := application.NewService(postgres.NewRetrievalStore(pool)).Search(ctx, access.Principal{UserID: reader, Scopes: []access.Permission{access.ReadCapture}}, domain.Query{ProjectID: project, Text: "backfilled"})
	if err != nil || len(result.Items) != 1 {
		t.Fatal("backfilled source not searchable", result, err)
	}
}

func TestRetrievalProjectionFailureNeverAcknowledgesPartialSource(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	source := snapshot()
	source.Messages[0].Text = strings.Repeat("authorisation evidence ", 400)
	if _, err := captureapp.NewService(postgres.NewCaptureStore(pool)).Ingest(ctx, subject, source); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE retrieval_chunks ADD CONSTRAINT simulated_chunk_failure CHECK(false) NOT VALID`); err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewIndexer(postgres.NewRetrievalStore(pool)).ReconcileOnce(ctx); err == nil {
		t.Fatal("failed chunk persistence acknowledged")
	}
	if count(t, pool, "retrieval_chunks") != 0 {
		t.Fatal("partial projection persisted")
	}
	var done int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE completed_at IS NOT NULL`).Scan(&done); err != nil || done != 0 {
		t.Fatal("failed projection acknowledged its outbox", err)
	}
	if _, err := pool.Exec(ctx, `ALTER TABLE retrieval_chunks DROP CONSTRAINT simulated_chunk_failure; UPDATE retrieval_jobs SET lease_until=now()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	if worked, err := application.NewIndexer(postgres.NewRetrievalStore(pool)).ReconcileOnce(ctx); err != nil || !worked {
		t.Fatal("projection retry failed", worked, err)
	}
	if count(t, pool, "retrieval_chunks") < 2 {
		t.Fatal("complete multi-chunk source missing")
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE completed_at IS NOT NULL`).Scan(&done); err != nil || done != 1 {
		t.Fatal("completed projection not acknowledged", err)
	}
}

func TestRetrievalTaskSourceCapabilitiesAvailabilityAndIndexLease(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	_, _ = pool.Exec(ctx, "UPDATE memberships SET role='admin' WHERE user_id=$1", subject)
	// Use the actual Work store path; source observations and indexing work share one transaction.
	workStore := postgres.NewWorkStore(pool)
	config := work.Config{Endpoint: "https://tasks.internal/mcp", CountryID: taskCountry, Token: "synthetic-token"}
	if _, err := workStore.Connect(ctx, subject, project, config, make([]byte, 64)); err != nil {
		t.Fatal(err)
	}
	if err := workStore.Refresh(ctx, subject, project, taskUUID); err != nil {
		t.Fatal(err)
	}
	lease, found, err := workStore.Claim(ctx)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	task := work.Task{CountryID: taskCountry, ID: taskUUID, Number: 1, Status: "PLANNING", UpdatedAt: time.Now(), URL: "https://tasks.internal/task/1"}
	task.Title = "Refresh token requirements"
	task.Description = "Авторизация и проверка доступа"
	task.Documents = []work.Document{{ID: taskUUID, FileName: "architecture.md", Title: "Architecture", Content: "The refresh token implementation must validate its issuer."}}
	if err := workStore.Finish(ctx, lease, workapp.Outcome{Task: &task}); err != nil {
		t.Fatal(err)
	}
	store := postgres.NewRetrievalStore(pool)
	indexer := application.NewIndexer(store)
	service := application.NewService(store)
	if _, err := indexer.ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	integration := access.Principal{UserID: reader, Scopes: []access.Permission{access.ReadIntegration}}
	query := domain.Query{ProjectID: project, Text: "refreshToken"}
	hits, err := service.Search(ctx, integration, query)
	if err != nil || len(hits.Items) < 2 || hits.Items[0].Kind != "task" {
		t.Fatal("ranking/source indexing", hits, err)
	}
	docID := ""
	for _, h := range hits.Items {
		if h.Kind == "document" {
			docID = h.ID
		}
	}
	if docID == "" {
		t.Fatal("document not indexed")
	}
	byName, err := service.Search(ctx, integration, domain.Query{ProjectID: project, Text: "architecture.md", Kinds: []string{"document"}})
	if err != nil || len(byName.Items) != 1 {
		t.Fatal("document filename missing from lexical projection", byName, err)
	}
	captureOnly := access.Principal{UserID: reader, Scopes: []access.Permission{access.ReadCapture}}
	if restricted, err := service.Search(ctx, captureOnly, query); err != nil || len(restricted.Items) != 0 {
		t.Fatal("task leaked through capture capability", restricted, err)
	}
	if _, err := service.Expand(ctx, captureOnly, domain.Expansion{EvidenceID: docID}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("task expansion bypassed source capability", err)
	}
	if err := workStore.Disconnect(ctx, subject, project); err != nil {
		t.Fatal(err)
	}
	if restricted, err := service.Search(ctx, integration, query); err != nil || len(restricted.Items) != 0 {
		t.Fatal("disabled source leaked", restricted, err)
	}
	if _, err := service.Expand(ctx, integration, domain.Expansion{EvidenceID: docID}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("disabled source expanded", err)
	}
}
