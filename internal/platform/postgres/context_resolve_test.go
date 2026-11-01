package postgres_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"workgraph.local/platform/internal/access"
	captures "workgraph.local/platform/internal/capture/application"
	pack "workgraph.local/platform/internal/contextpack/domain"
	"workgraph.local/platform/internal/platform/postgres"
	retrieval "workgraph.local/platform/internal/retrieval/domain"
)

func TestResolveRequiresTrustedCurrentObservationAndProjectMembership(t *testing.T) {
	pool := contextFixture(t)
	ctx := context.Background()
	seedLinkedMR(t, pool, true, 42)
	store := postgres.NewContextStore(pool)
	p := contextPrincipal(access.ReadIntegration)
	q := pack.ResolveQuery{ProjectID: project, URL: "https://gitlab.internal/team/project/-/merge_requests/7/?tab=commits#changes"}
	got, err := store.Resolve(ctx, p, q)
	if err != nil || got.Query.MR != 7 || got.Query.RepositoryID != repository || got.Query.ExpectedHead != strings.Repeat("a", 40) {
		t.Fatal(got, err)
	}
	if _, err = store.Resolve(ctx, contextPrincipal(access.ReadCapture), q); !errors.Is(err, access.ErrDenied) {
		t.Fatal("capture scope resolved provider data", err)
	}
	foreign := q
	foreign.ProjectID = "99999999-9999-4999-8999-999999999999"
	if _, err = store.Resolve(ctx, p, foreign); !errors.Is(err, access.ErrDenied) {
		t.Fatal("foreign project leaked observation", err)
	}
	unknown := q
	unknown.URL = "https://127.0.0.1/team/project/-/merge_requests/7"
	if _, err = store.Resolve(ctx, p, unknown); !errors.Is(err, retrieval.ErrNotFound) {
		t.Fatal("unobserved URL resolved", err)
	}
	for _, statement := range []string{"UPDATE forge_connections SET last_synced_at=now()-interval '11 minutes'", "UPDATE forge_connections SET last_synced_at=now(),blocked=true", "UPDATE forge_connections SET blocked=false,enabled=false"} {
		if _, err = pool.Exec(ctx, statement); err != nil {
			t.Fatal(err)
		}
		if _, err = store.Resolve(ctx, p, q); !errors.Is(err, retrieval.ErrNotFound) {
			t.Fatal("unavailable observation retained", err)
		}
	}
}

func TestMRPackIncludesVerifiedNonHeadCommitsWithoutBroadeningToOtherCommits(t *testing.T) {
	pool := contextFixture(t)
	ctx := context.Background()
	seedLinkedMR(t, pool, true, 42)
	svc := captures.NewService(postgres.NewCaptureStore(pool))
	for i, sha := range []string{strings.Repeat("b", 40), strings.Repeat("d", 40)} {
		snap := snapshot()
		snap.SessionID = []string{"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"}[i]
		snap.Commits = []string{sha}
		if _, err := svc.Ingest(ctx, subject, snap); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, "UPDATE commits SET confirmed_at=now()"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO forge_mr_commits(repository_id,iid,code_key,source_project_id,sha) SELECT repository_id,iid,code_key,42,$1 FROM forge_mr_codes`, strings.Repeat("b", 40)); err != nil {
		t.Fatal(err)
	}
	store := postgres.NewContextStore(pool)
	p := contextPrincipal(access.ReadIntegration, access.ReadCapture)
	q := pack.Query{ProjectID: project, RepositoryID: repository, MR: 7, Limit: 6}
	result, err := store.Read(ctx, p, q)
	if err != nil || len(result.Captures.Items) != 2 || len(result.MergeRequests.Items) != 1 || result.MergeRequests.Items[0].SourceCommitCount != 2 {
		t.Fatal("non-head commit omitted or unrelated capture leaked", result, err)
	}
	if _, err = pool.Exec(ctx, "UPDATE forge_mr_codes SET complete=false"); err != nil {
		t.Fatal(err)
	}
	result, err = store.Read(ctx, p, q)
	if err != nil || len(result.Captures.Items) != 1 || result.Captures.Items[0].Source.SessionID != snapshot().SessionID {
		t.Fatal("partial membership treated as authoritative", result, err)
	}
}

func TestRecentBrowseHasStableTimestampCursorAndLatestTitle(t *testing.T) {
	pool := contextFixture(t)
	ctx := context.Background()
	svc := captures.NewService(postgres.NewCaptureStore(pool))
	snap := snapshot()
	snap.SessionID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	snap.Messages[0].Text = "Latest title"
	if _, err := svc.Ingest(ctx, subject, snap); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE capture_revisions SET created_at='2026-10-30T10:00:00Z'`); err != nil {
		t.Fatal(err)
	}
	store := postgres.NewContextStore(pool)
	p := contextPrincipal(access.ReadCapture)
	q := pack.BrowseQuery{ProjectID: project, Kind: "session", Order: "recent", Limit: 1}
	page, err := store.Browse(ctx, p, q)
	if err != nil || len(page.Items) != 1 || page.Next == nil || page.Next.ObservedAt == nil || page.Items[0].Title != "Latest title" {
		t.Fatal(page, err)
	}
	q.After = page.Next
	second, err := store.Browse(ctx, p, q)
	if err != nil || len(second.Items) != 1 || second.Next != nil || second.Items[0].Query.SessionID == page.Items[0].Query.SessionID {
		t.Fatal("tied timestamp skipped/duplicated", second, err)
	}
	snap.Revision = 3
	snap.Messages[0].Text = "Newest revision"
	if _, err = svc.Ingest(ctx, subject, snap); err != nil {
		t.Fatal(err)
	}
	snap.Revision = 2
	snap.Messages[0].Text = "Late older revision"
	if _, err = svc.Ingest(ctx, subject, snap); err != nil {
		t.Fatal(err)
	}
	q.After = nil
	page, err = store.Browse(ctx, p, q)
	if err != nil || page.Items[0].Title != "Newest revision" || page.Items[0].Revision != 3 {
		t.Fatal("late revision replaced title", page, err)
	}
}
