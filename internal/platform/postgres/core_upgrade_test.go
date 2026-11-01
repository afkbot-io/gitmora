package postgres_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"workgraph.local/platform/internal/access"
	capture "workgraph.local/platform/internal/capture/application"
	capturedomain "workgraph.local/platform/internal/capture/domain"
	"workgraph.local/platform/internal/platform/postgres"
	retrieval "workgraph.local/platform/internal/retrieval/application"
	search "workgraph.local/platform/internal/retrieval/domain"
)

func TestCoreUpgradePreservesCapturesSearchAndExistingAuthority(t *testing.T) {
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
		if name.Name() >= "0021" {
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
		if _, e = pool.Exec(ctx, `INSERT INTO schema_migrations(name,checksum)VALUES($1,$2)`, filepath.ToSlash(path), fmt.Sprintf("%x", sum)); e != nil {
			t.Fatal(e)
		}
	}
	seed(t, pool)
	service := capture.NewService(postgres.NewCaptureStore(pool))
	input := snapshot()
	input.Messages[0].Text = "CoreUpgradeSentinel локальная сессия"
	receipt, e := capturedomain.Sanitize(input)
	if e != nil {
		t.Fatal(e)
	}
	// Seed the historical schema using its original wire representation. Current writers require the new schema.
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO capture_sessions(repository_id,session_id,originator_id,identity_digest,latest_revision) VALUES($1,$2,$3,$4,1)`, []any{repository, input.SessionID, subject, fmt.Sprintf("%x", sha256.Sum256([]byte("legacy identity")))}},
		{`INSERT INTO capture_revisions(repository_id,session_id,revision,digest,canonical) VALUES($1,$2,1,$3,$4)`, []any{repository, input.SessionID, receipt.Digest(), receipt.CanonicalJSON()}},
		{`INSERT INTO capture_context_scopes(repository_id,session_id,started_at_text,agent_name) VALUES($1,$2,$3,$4)`, []any{repository, input.SessionID, input.StartedAt.UTC().Format(time.RFC3339Nano), input.Agent.Name}},
	} {
		if _, e = pool.Exec(ctx, q.sql, q.args...); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = pool.Exec(ctx, `INSERT INTO retrieval_jobs(id,source_kind,project_id,repository_id,session_id,revision,source_digest,source_observed_at) VALUES('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa','capture',$1,$2,$3,1,$4,now())`, project, repository, input.SessionID, receipt.Digest()); e != nil {
		t.Fatal(e)
	}
	issued, e := access.IssueCredential(subject, "Upgrade", []access.Permission{access.ReadCapture}, time.Now(), time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	keys := postgres.NewCredentialStore(pool)
	if e = keys.Save(ctx, issued.Record); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `UPDATE api_tokens SET scopes=ARRAY['capture:read','review:read','knowledge:write'] WHERE id=$1`, issued.Record.ID); e != nil {
		t.Fatal(e)
	}
	retired, e := access.IssueCredential(subject, "Retired", []access.Permission{access.ReadCapture}, time.Now(), time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	if e = keys.Save(ctx, retired.Record); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `UPDATE api_tokens SET scopes=ARRAY['review:run'] WHERE id=$1`, retired.Record.ID); e != nil {
		t.Fatal(e)
	}
	if e = postgres.Migrate(ctx, pool); e != nil {
		t.Fatal(e)
	}
	if e = postgres.Migrate(ctx, pool); e != nil {
		t.Fatal("repeat migration", e)
	}
	principal, e := keys.Authenticate(ctx, issued.Secret)
	if e != nil || !slices.Equal(principal.Scopes, []access.Permission{access.ReadCapture}) {
		t.Fatal("surviving authority changed", principal, e)
	}
	if _, e = keys.Authenticate(ctx, retired.Secret); e == nil {
		t.Fatal("retired-only credential remained active")
	}
	stored, e := service.Get(ctx, subject, repository, input.SessionID, 1)
	if e != nil || stored.Session.Digest() != receipt.Digest() {
		t.Fatal("capture changed", e)
	}
	index := retrieval.NewIndexer(postgres.NewRetrievalStore(pool))
	if _, e = index.ReconcileOnce(ctx); e != nil {
		t.Fatal(e)
	}
	hits, e := retrieval.NewService(postgres.NewRetrievalStore(pool)).Search(ctx, principal, search.Query{ProjectID: project, Text: "CoreUpgradeSentinel"})
	if e != nil || len(hits.Items) != 1 {
		t.Fatal("search lost", hits, e)
	}
	for _, table := range []string{"inference_runs", "inference_cancellations", "review_runs", "review_cancellations", "knowledge_entries", "knowledge_references", "knowledge_events"} {
		var absent bool
		if e = pool.QueryRow(ctx, `SELECT to_regclass($1) IS NULL`, table).Scan(&absent); e != nil || !absent {
			t.Fatal("retired table retained", table, e)
		}
	}
	var scopes []string
	var revoked bool
	if e = pool.QueryRow(ctx, `SELECT scopes,revoked_at IS NOT NULL FROM api_tokens WHERE id=$1`, retired.Record.ID).Scan(&scopes, &revoked); e != nil || len(scopes) != 0 || !revoked {
		t.Fatal("retired authority replaced", scopes, e)
	}
	if count(t, pool, "memberships") != 2 || count(t, pool, "repositories") != 1 {
		t.Fatal("catalog changed")
	}
}
