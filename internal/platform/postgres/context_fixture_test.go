package postgres_test

import (
	"context"
	"encoding/json"
	"github.com/jackc/pgx/v5/pgxpool"
	"strings"
	"testing"
	"time"
	captures "workgraph.local/platform/internal/capture/application"
	forge "workgraph.local/platform/internal/forge/domain"
	"workgraph.local/platform/internal/platform/postgres"
)

// These UUIDs occur only in immutable legacy-capture compatibility fixtures.
const taskCountry = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
const taskUUID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"

func contextFixture(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := database(t)
	seed(t, pool)
	if _, e := captures.NewService(postgres.NewCaptureStore(pool)).Ingest(context.Background(), subject, snapshot()); e != nil {
		t.Fatal(e)
	}
	return pool
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
