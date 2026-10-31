package postgres_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"workgraph.local/platform/internal/access"
	captureapp "workgraph.local/platform/internal/capture/application"
	capture "workgraph.local/platform/internal/capture/domain"
	"workgraph.local/platform/internal/platform/postgres"
	retrieval "workgraph.local/platform/internal/retrieval/application"
	"workgraph.local/platform/internal/retrieval/domain"
)

func TestLegacySourcesUpgradeToCurrentRetrievalAndCodeQueues(t *testing.T) {
	pool := databaseSetup(t, false)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `CREATE TABLE schema_migrations(name text PRIMARY KEY,checksum text NOT NULL,applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		t.Fatal(err)
	}
	names, err := os.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if name.Name() >= "0006" {
			continue
		}
		path := filepath.Join("migrations", name.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = pool.Exec(ctx, string(raw)); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		if _, err = pool.Exec(ctx, `INSERT INTO schema_migrations(name,checksum) VALUES($1,$2)`, filepath.ToSlash(path), fmt.Sprintf("%x", sum)); err != nil {
			t.Fatal(err)
		}
	}
	seed(t, pool)
	input := snapshot()
	input.Task = &capture.TaskRef{CountryID: taskCountry, TaskID: taskUUID}
	input.Messages[0].Text = "Legacy backfilled searchable evidence"
	session, err := capture.Sanitize(input)
	if err != nil {
		t.Fatal(err)
	}
	event := captureapp.CaptureEvent{RepositoryID: repository, SessionID: input.SessionID, Revision: 1, Digest: session.Digest()}
	payload, _ := json.Marshal(event)
	batch := &pgx.Batch{}
	batch.Queue(`INSERT INTO capture_sessions(repository_id,session_id,originator_id,identity_digest,latest_revision) VALUES($1,$2,$3,$4,1)`, repository, input.SessionID, subject, strings.Repeat("a", 64))
	batch.Queue(`INSERT INTO capture_revisions(repository_id,session_id,revision,digest,canonical) VALUES($1,$2,1,$3,$4)`, repository, input.SessionID, session.Digest(), session.CanonicalJSON())
	batch.Queue(`INSERT INTO commits(repository_id,sha,confirmed_at) VALUES($1,$2,now())`, repository, input.Commits[0])
	batch.Queue(`INSERT INTO capture_revision_commits(repository_id,session_id,revision,sha) VALUES($1,$2,1,$3)`, repository, input.SessionID, input.Commits[0])
	batch.Queue(`INSERT INTO outbox_events(repository_id,event_key,event_type,payload) VALUES($1,$2,'capture.revision_recorded',$3)`, repository, "capture:"+input.SessionID+":1", payload)
	if err = pool.SendBatch(ctx, batch).Close(); err != nil {
		t.Fatal(err)
	}
	if err = postgres.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if count(t, pool, "retrieval_jobs") != 1 || count(t, pool, "forge_code_runs") != 1 || count(t, pool, "work_capture_bindings") != 1 {
		t.Fatal("existing sources/confirmed commits not queued")
	}
	if _, err = retrieval.NewIndexer(postgres.NewRetrievalStore(pool)).ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	result, err := retrieval.NewService(postgres.NewRetrievalStore(pool)).Search(ctx, access.Principal{UserID: reader, Scopes: []access.Permission{access.ReadCapture}}, domain.Query{ProjectID: project, Text: "backfilled"})
	if err != nil || len(result.Items) != 1 {
		t.Fatal("legacy data not retrieved", result, err)
	}
}
