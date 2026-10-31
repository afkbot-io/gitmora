package postgres_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/capture/application"
	"workgraph.local/platform/internal/capture/domain"
	"workgraph.local/platform/internal/platform/postgres"
)

const subject = "33333333-3333-4333-8333-333333333333"
const reader = "44444444-4444-4444-8444-444444444444"
const project = "55555555-5555-4555-8555-555555555555"
const repository = "11111111-1111-4111-8111-111111111111"
const otherRepository = "66666666-6666-4666-8666-666666666666"

func database(t *testing.T) *pgxpool.Pool { return databaseSetup(t, true) }

func databaseSetup(t *testing.T, migrate bool) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv("WORKGRAPH_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("PostgreSQL integration: run scripts/test-integration.sh")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	schema := "test_" + hex.EncodeToString(random[:])
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	config.MaxConns = 8
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := admin.Exec(cleanup, "DROP SCHEMA "+identifier+" CASCADE"); err != nil {
			t.Error(err)
		}
		admin.Close()
	})
	if migrate {
		if err := postgres.Migrate(ctx, pool); err != nil {
			t.Fatal(err)
		}
	}
	return pool
}

func seed(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	batch := &pgx.Batch{}
	batch.Queue("INSERT INTO users(id,display_name) VALUES($1,'Developer'),($2,'Reader')", subject, reader)
	batch.Queue("INSERT INTO projects(id,name) VALUES($1,'Team')", project)
	batch.Queue("INSERT INTO memberships(project_id,user_id,role) VALUES($1,$2,'writer'),($1,$3,'reader')", project, subject, reader)
	batch.Queue("INSERT INTO repositories(id,project_id,gitlab_url,gitlab_project_id,name) VALUES($1,$2,'https://gitlab.internal',42,'app')", repository, project)
	if err := pool.SendBatch(context.Background(), batch).Close(); err != nil {
		t.Fatal(err)
	}
}

func snapshot() domain.Snapshot {
	return domain.Snapshot{SchemaVersion: 1, RepositoryID: repository, SessionID: "22222222-2222-4222-8222-222222222222", Revision: 1, Agent: domain.Agent{Name: "local-agent", Model: "local-model"}, StartedAt: time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC), Messages: []domain.Message{{Role: "user", Text: "DB_PASSWORD=must-never-persist"}}, Commits: []string{strings.Repeat("a", 40)}}
}

func count(t *testing.T, pool *pgxpool.Pool, table string) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM "+pgx.Identifier{table}.Sanitize()).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestCapturePersistsAtomicSanitizedEvidenceAndIdempotentRetries(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	service := application.NewService(postgres.NewCaptureStore(pool))
	created, err := service.Ingest(ctx, subject, snapshot())
	if err != nil || !created.Created {
		t.Fatalf("ingest: %+v %v", created, err)
	}
	retry, err := service.Ingest(ctx, subject, snapshot())
	if err != nil || retry.Created || retry.Digest != created.Digest {
		t.Fatalf("retry: %+v %v", retry, err)
	}
	if count(t, pool, "capture_revisions") != 1 || count(t, pool, "outbox_events") != 1 {
		t.Fatal("duplicate persistence")
	}
	stored, err := service.Get(ctx, reader, repository, snapshot().SessionID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(stored.Session.CanonicalJSON()), "must-never-persist") {
		t.Fatal("secret persisted")
	}
	if len(stored.ConfirmedCommits) != 0 {
		t.Fatal("client claim was treated as provider confirmation")
	}
	changed := snapshot()
	changed.Messages[0].Text = "Changed evidence"
	if _, err := service.Ingest(ctx, subject, changed); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("expected identity conflict: %v", err)
	}
	if _, err := service.Ingest(ctx, reader, snapshot()); !errors.Is(err, access.ErrDenied) {
		t.Fatalf("reader wrote capture: %v", err)
	}
	if count(t, pool, "outbox_events") != 1 {
		t.Fatal("conflict/denial added work")
	}
}

func TestConcurrentRetriesCreateOneRevisionAndEvent(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	service := application.NewService(postgres.NewCaptureStore(pool))
	var wg sync.WaitGroup
	var created atomic.Int64
	failures := make(chan error, 16)
	for range 16 {
		wg.Go(func() {
			result, err := service.Ingest(context.Background(), subject, snapshot())
			if err != nil {
				failures <- err
			}
			if result.Created {
				created.Add(1)
			}
		})
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if created.Load() != 1 || count(t, pool, "capture_revisions") != 1 || count(t, pool, "outbox_events") != 1 {
		t.Fatal("concurrent duplicate delivery was not atomic")
	}
}

func TestOutboxFailureRollsBackEveryCaptureWrite(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	service := application.NewService(postgres.NewCaptureStore(pool))
	if _, err := pool.Exec(ctx, "ALTER TABLE outbox_events ADD CONSTRAINT simulated_failure CHECK(false) NOT VALID"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Ingest(ctx, subject, snapshot()); err == nil {
		t.Fatal("reported success after outbox failure")
	}
	for _, table := range []string{"capture_sessions", "capture_revisions", "commits", "capture_revision_commits", "outbox_events"} {
		if count(t, pool, table) != 0 {
			t.Errorf("partial commit in %s", table)
		}
	}
	if _, err := pool.Exec(ctx, "ALTER TABLE outbox_events DROP CONSTRAINT simulated_failure"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Ingest(ctx, subject, snapshot()); err != nil {
		t.Fatal(err)
	}
}

func TestOutOfOrderRevisionsAndImmutableSessionIdentity(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	service := application.NewService(postgres.NewCaptureStore(pool))
	second := snapshot()
	second.Revision = 2
	second.Messages[0].Text = "Later observation"
	if _, err := service.Ingest(ctx, subject, second); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Ingest(ctx, subject, snapshot()); err != nil {
		t.Fatal(err)
	}
	latest, err := service.Get(ctx, subject, repository, second.SessionID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if latest.Session.Snapshot().Revision != 2 {
		t.Fatal("late older upload replaced latest revision")
	}
	third := snapshot()
	third.Revision = 3
	third.Task = &domain.TaskRef{CountryID: project, TaskID: repository}
	if _, err := service.Ingest(ctx, subject, third); !errors.Is(err, application.ErrConflict) {
		t.Fatalf("session retargeting accepted: %v", err)
	}
}

func TestAccessIsCheckedForEveryRepositoryEvenWithKnownSessionAndSHA(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	service := application.NewService(postgres.NewCaptureStore(pool))
	if _, err := service.Ingest(ctx, subject, snapshot()); err != nil {
		t.Fatal(err)
	}
	_, err := pool.Exec(ctx, "INSERT INTO projects(id,name) VALUES($1,'Other team')", otherRepository)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, "INSERT INTO repositories(id,project_id,gitlab_url,gitlab_project_id,name) VALUES($1,$1,'https://gitlab.internal',43,'other')", otherRepository)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Get(ctx, subject, otherRepository, snapshot().SessionID, 0); !errors.Is(err, access.ErrDenied) {
		t.Fatalf("cross-project read: %v", err)
	}
	copy := snapshot()
	copy.RepositoryID = otherRepository
	if _, err := service.Ingest(ctx, subject, copy); !errors.Is(err, access.ErrDenied) {
		t.Fatalf("cross-project write: %v", err)
	}
}

func TestAuthorizationLockPreventsConcurrentMembershipRevocation(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	store := postgres.NewCaptureStore(pool)
	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	if _, err := connection.Exec(ctx, "SET lock_timeout='100ms'"); err != nil {
		t.Fatal(err)
	}
	err = store.WithinAuthorized(ctx, subject, repository, access.WriteCapture, func(_ application.Transaction) error {
		_, err := connection.Exec(ctx, "UPDATE memberships SET role='reader' WHERE project_id=$1 AND user_id=$2", project, subject)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "55P03" {
			t.Fatalf("membership was not locked through operation: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connection.Exec(ctx, "UPDATE memberships SET role='reader' WHERE project_id=$1 AND user_id=$2", project, subject); err != nil {
		t.Fatal(err)
	}
	if _, err := application.NewService(store).Ingest(ctx, subject, snapshot()); !errors.Is(err, access.ErrDenied) {
		t.Fatalf("revocation was not enforced: %v", err)
	}
}

func TestMigrationsAreIdempotentAndDetectDrift(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	if err := postgres.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE schema_migrations SET checksum='tampered'"); err != nil {
		t.Fatal(err)
	}
	if err := postgres.Migrate(ctx, pool); err == nil {
		t.Fatal("migration drift was accepted")
	}
}

func TestMigrationsRejectUnknownHistoryEvenWithEmptyChecksum(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, "INSERT INTO schema_migrations(name,checksum) VALUES('unknown.sql','')"); err != nil {
		t.Fatal(err)
	}
	if err := postgres.Migrate(ctx, pool); err == nil {
		t.Fatal("unknown migration with empty checksum was accepted")
	}
}

func TestSchemaVerificationRejectsMissingMigration(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	if err := postgres.VerifySchema(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "DELETE FROM schema_migrations WHERE name='migrations/0002_credentials.sql'"); err != nil {
		t.Fatal(err)
	}
	if err := postgres.VerifySchema(ctx, pool); err == nil {
		t.Fatal("incomplete migration history was considered ready")
	}
}

func TestMaximumCommitAssociationsPersistInOneRevision(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	input := snapshot()
	input.Commits = make([]string, 2000)
	for i := range input.Commits {
		input.Commits[i] = fmt.Sprintf("%040x", i+1)
	}
	if _, err := application.NewService(postgres.NewCaptureStore(pool)).Ingest(context.Background(), subject, input); err != nil {
		t.Fatal(err)
	}
	if count(t, pool, "commits") != 2000 || count(t, pool, "capture_revision_commits") != 2000 {
		t.Fatal("bulk commit associations were lost")
	}
}

func TestAnotherWriterCannotReplaceSessionOriginatorAndDisabledUsersAreDenied(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	service := application.NewService(postgres.NewCaptureStore(pool))
	if _, err := service.Ingest(ctx, subject, snapshot()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE memberships SET role='writer' WHERE user_id=$1", reader); err != nil {
		t.Fatal(err)
	}
	second := snapshot()
	second.Revision = 2
	if _, err := service.Ingest(ctx, reader, second); !errors.Is(err, access.ErrDenied) {
		t.Fatalf("another writer modified session: %v", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE users SET enabled=false WHERE id=$1", subject); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Get(ctx, subject, repository, second.SessionID, 0); !errors.Is(err, access.ErrDenied) {
		t.Fatalf("disabled user retrieved data: %v", err)
	}
}
