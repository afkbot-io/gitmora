package postgres_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	capture "workgraph.local/platform/internal/capture/domain"
	"workgraph.local/platform/internal/platform/postgres"
)

func TestTaskRemovalUpgradePreservesCaptureBytesCommitAndForgeCredential(t *testing.T) {
	pool := databaseSetup(t, false)
	ctx := context.Background()
	if _, e := pool.Exec(ctx, `CREATE TABLE schema_migrations(name text PRIMARY KEY,checksum text NOT NULL,applied_at timestamptz NOT NULL DEFAULT now())`); e != nil {
		t.Fatal(e)
	}
	names, e := os.ReadDir("migrations")
	if e != nil {
		t.Fatal(e)
	}
	for _, name := range names {
		if name.Name() >= "0025" {
			continue
		}
		path := filepath.Join("migrations", name.Name())
		raw, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = pool.Exec(ctx, string(raw)); e != nil {
			t.Fatal(e)
		}
		sum := sha256.Sum256(raw)
		if _, e = pool.Exec(ctx, `INSERT INTO schema_migrations(name,checksum) VALUES($1,$2)`, filepath.ToSlash(path), fmt.Sprintf("%x", sum)); e != nil {
			t.Fatal(e)
		}
	}
	seed(t, pool)
	input := snapshot()
	input.Task = &capture.TaskRef{CountryID: taskCountry, TaskID: taskUUID}
	session, e := capture.Sanitize(input)
	if e != nil {
		t.Fatal(e)
	}
	envelope := bytes.Repeat([]byte{83}, 64)
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO capture_sessions(repository_id,session_id,originator_id,identity_digest,latest_revision) VALUES($1,$2,$3,$4,1)`, []any{repository, input.SessionID, subject, fmt.Sprintf("%x", sha256.Sum256([]byte("legacy identity")))}},
		{`INSERT INTO capture_revisions(repository_id,session_id,revision,digest,canonical) VALUES($1,$2,1,$3,$4)`, []any{repository, input.SessionID, session.Digest(), session.CanonicalJSON()}},
		{`INSERT INTO capture_context_scopes(repository_id,session_id,started_at_text,agent_name,country_id,task_id) VALUES($1,$2,$3,$4,$5,$6)`, []any{repository, input.SessionID, input.StartedAt.Format("2006-01-02T15:04:05Z07:00"), input.Agent.Name, taskCountry, taskUUID}},
		{`INSERT INTO commits(repository_id,sha,confirmed_at) VALUES($1,$2,now())`, []any{repository, input.Commits[0]}},
		{`INSERT INTO capture_revision_commits(repository_id,session_id,revision,sha) VALUES($1,$2,1,$3)`, []any{repository, input.SessionID, input.Commits[0]}},
		{`INSERT INTO forge_connections(repository_id,webhook_mode,secret_envelope,updated_by) VALUES($1,'disabled',$2,$3)`, []any{repository, envelope, subject}},
		{`INSERT INTO work_connections(project_id,endpoint,country_id,secret_envelope,updated_by) VALUES($1,'https://retired.invalid/mcp',$2,$3,$4)`, []any{project, taskCountry, envelope, subject}},
		{`INSERT INTO work_task_observations(project_id,country_id,task_id,model,digest) VALUES($1,$2,$3,'{}',$4)`, []any{project, taskCountry, taskUUID, session.Digest()}},
		{`INSERT INTO retrieval_jobs(id,source_kind,project_id,country_id,task_id,source_digest,source_observed_at) VALUES(gen_random_uuid(),'task',$1,$2,$3,$4,now())`, []any{project, taskCountry, taskUUID, session.Digest()}},
	} {
		if _, e = pool.Exec(ctx, q.sql, q.args...); e != nil {
			t.Fatal(e)
		}
	}
	if e = postgres.Migrate(ctx, pool); e != nil {
		t.Fatal(e)
	}
	if e = postgres.Migrate(ctx, pool); e != nil {
		t.Fatal("repeat", e)
	}
	var raw, secret []byte
	var digest, title string
	if e = pool.QueryRow(ctx, `SELECT canonical,digest FROM capture_revisions`).Scan(&raw, &digest); e != nil || !bytes.Equal(raw, session.CanonicalJSON()) || digest != session.Digest() {
		t.Fatal("capture bytes changed", e)
	}
	if e = pool.QueryRow(ctx, `SELECT secret_envelope FROM forge_connections`).Scan(&secret); e != nil || !bytes.Equal(secret, envelope) {
		t.Fatal("forge credential changed", e)
	}
	if e = pool.QueryRow(ctx, `SELECT title FROM capture_context_scopes`).Scan(&title); e != nil || title != session.Snapshot().Messages[0].Text {
		t.Fatal("title backfill lost", title, e)
	}
	if count(t, pool, "commits") != 1 || count(t, pool, "capture_revision_commits") != 1 || count(t, pool, "repositories") != 1 || count(t, pool, "retrieval_jobs") != 0 {
		t.Fatal("unrelated data changed or task index retained")
	}
	for _, table := range []string{"work_connections", "work_jobs", "work_tasks", "work_task_observations", "work_capture_bindings", "work_link_evidence"} {
		var absent bool
		if e = pool.QueryRow(ctx, `SELECT to_regclass($1) IS NULL`, table).Scan(&absent); e != nil || !absent {
			t.Fatal("retired table retained", table, e)
		}
	}
}
