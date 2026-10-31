package postgres_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
	"workgraph.local/platform/internal/access"
	captures "workgraph.local/platform/internal/capture/application"
	capture "workgraph.local/platform/internal/capture/domain"
	pack "workgraph.local/platform/internal/contextpack/domain"
	forge "workgraph.local/platform/internal/forge/domain"
	"workgraph.local/platform/internal/platform/postgres"
)

func contextPrincipal(scopes ...access.Permission) access.Principal {
	return access.Principal{UserID: reader, Scopes: scopes}
}

func TestContextPackExpandsOnlyFullyVerifiedNativeStreamsAndPaginates(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	service := captures.NewService(postgres.NewCaptureStore(pool))
	root := snapshot()
	root.StartedAt = root.StartedAt.Add(123 * time.Nanosecond)
	root.Messages[0].Text = "Root report"
	canonical, _ := capture.Sanitize(root)
	child := root
	child.SessionID = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
	child.Messages = []capture.Message{{Role: "assistant", Text: "Late window without its own commit"}}
	child.Commits = nil
	child.Continuation = &capture.Continuation{RootSessionID: root.SessionID, PreviousSessionID: root.SessionID, PreviousRevision: 1, PreviousDigest: canonical.Digest(), Segment: 2, Reason: "window"}
	forged := child
	forged.SessionID = "dddddddd-dddd-4ddd-8ddd-dddddddddddd"
	c := *child.Continuation
	c.PreviousDigest = strings.Repeat("f", 64)
	forged.Continuation = &c
	for _, s := range []capture.Snapshot{child, forged, root} {
		if _, err := service.Ingest(ctx, subject, s); err != nil {
			t.Fatal(err)
		}
	}
	nanos := child
	nanos.SessionID = "eeeeeeee-eeee-4eee-8eee-eeeeeeeeeeee"
	nanos.StartedAt = nanos.StartedAt.Add(time.Nanosecond)
	if _, err := service.Ingest(ctx, subject, nanos); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE commits SET confirmed_at=now()"); err != nil {
		t.Fatal(err)
	}
	store := postgres.NewContextStore(pool)
	query := pack.Query{ProjectID: project, RepositoryID: repository, CommitSHA: strings.Repeat("a", 40), Limit: 1}
	first, err := store.Read(ctx, contextPrincipal(access.ReadCapture), query)
	if err != nil || len(first.Captures.Items) != 1 || first.Captures.Next == nil || !first.Captures.Items[0].ChainVerified {
		t.Fatalf("%+v %v", first, err)
	}
	query.After = first.Captures.Next
	second, err := store.Read(ctx, contextPrincipal(access.ReadCapture), query)
	if err != nil || len(second.Captures.Items) != 1 || second.Captures.Items[0].Source.SessionID != child.SessionID || !second.Captures.Items[0].ParentReferenceVerified || !second.Captures.Items[0].ChainVerified || second.Captures.Items[0].ConfirmedCount != 0 || second.Captures.Next != nil {
		t.Fatalf("%+v %v", second, err)
	}
	query = pack.Query{ProjectID: project, RepositoryID: repository, SessionID: forged.SessionID, Limit: 12}
	bad, err := store.Read(ctx, contextPrincipal(access.ReadCapture), query)
	if err != nil || len(bad.Captures.Items) != 1 || bad.Captures.Items[0].ChainVerified || bad.Captures.Items[0].ParentReferenceVerified {
		t.Fatalf("forged reference expanded %+v %v", bad, err)
	}
	narrow, err := store.Read(ctx, contextPrincipal(access.ReadIntegration), query)
	if err != nil || len(narrow.Captures.Items) != 0 || !narrow.Captures.Omitted {
		t.Fatal("capture capability widened", narrow, err)
	}
	query.SessionID = nanos.SessionID
	nanoView, err := store.Read(ctx, contextPrincipal(access.ReadCapture), query)
	if err != nil || len(nanoView.Captures.Items) != 1 || nanoView.Captures.Items[0].ParentReferenceVerified || nanoView.Captures.Items[0].ChainVerified {
		t.Fatal("nanosecond scope difference collapsed", nanoView, err)
	}
}

func TestContextPackUsesCurrentHeadAndScopeWithPermissionAndSourceGates(t *testing.T) {
	pool, _, tasks, _ := automationFixture(t)
	ctx := context.Background()
	seedLinkedMR(t, pool, true, 42)
	if _, err := pool.Exec(ctx, "UPDATE commits SET confirmed_at=now()"); err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	for i, sha := range []string{strings.Repeat("a", 40), strings.Repeat("b", 40)} {
		p := forge.Pipeline{ID: int64(i + 1), ProjectID: 42, SHA: sha, Status: "success", UpdatedAt: time.Now()}
		raw, _ := json.Marshal(p)
		if _, err := pool.Exec(ctx, `INSERT INTO forge_pipelines(repository_id,id,sha,model,provider_updated_at) VALUES($1,$2,$3,$4,now())`, repository, p.ID, sha, raw); err != nil {
			t.Fatal(err)
		}
	}
	store := postgres.NewContextStore(pool)
	query := pack.Query{ProjectID: project, RepositoryID: repository, MR: 7, ExpectedHead: strings.Repeat("b", 40), Limit: 6}
	result, err := store.Read(ctx, contextPrincipal(access.ReadIntegration), query)
	if err != nil || len(result.MergeRequests.Items) != 1 {
		t.Fatal(result, err)
	}
	mr := result.MergeRequests.Items[0]
	if mr.ExpectedHeadMatches == nil || *mr.ExpectedHeadMatches || len(mr.Pipelines) != 1 || !mr.Pipelines[0].MatchesObservedHead || mr.Pipelines[0].MatchesExpectedHead || mr.Pipelines[0].SHA != strings.Repeat("a", 40) {
		t.Fatal("stale CI applied", mr)
	}
	taskQuery := pack.Query{ProjectID: project, CountryID: taskCountry, TaskID: taskUUID, Limit: 6}
	result, err = store.Read(ctx, contextPrincipal(access.ReadCapture, access.ReadIntegration), taskQuery)
	if err != nil || result.Task == nil || len(result.MergeRequests.Items) != 1 || len(result.Captures.Items) != 1 {
		t.Fatal(result, err)
	}
	narrow, err := store.Read(ctx, contextPrincipal(access.ReadIntegration), taskQuery)
	if err != nil || narrow.Task == nil || len(narrow.MergeRequests.Items) != 0 || len(narrow.Captures.Items) != 0 {
		t.Fatal("capture-derived MR discovery leaked", narrow, err)
	}
	bySHA, err := store.Read(ctx, contextPrincipal(access.ReadIntegration), pack.Query{ProjectID: project, RepositoryID: repository, CommitSHA: strings.Repeat("a", 40), Limit: 6})
	if err != nil || len(bySHA.MergeRequests.Items) != 1 || len(bySHA.Captures.Items) != 0 || bySHA.MergeRequests.Omitted {
		t.Fatal("explicit provider SHA incorrectly required capture access", bySHA, err)
	}
	var raw []byte
	if err := pool.QueryRow(ctx, `SELECT model FROM forge_mr_observations WHERE repository_id=$1 AND iid=7`, repository).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var newer forge.MergeRequest
	json.Unmarshal(raw, &newer)
	newer.Head = strings.Repeat("b", 40)
	newer.UpdatedAt = time.Now().UTC()
	raw, _ = json.Marshal(newer)
	for _, v := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO forge_mr_codes(repository_id,iid,code_key,complete) VALUES($1,7,$2,false)`, []any{repository, newer.CodeKey()}},
		{`INSERT INTO forge_mr_observations(repository_id,iid,digest,code_key,model,provider_updated_at) VALUES($1,7,$2,$3,$4,now())`, []any{repository, newer.Digest(), newer.CodeKey(), raw}},
		{`UPDATE forge_merge_requests SET digest=$2 WHERE repository_id=$1 AND iid=7`, []any{repository, newer.Digest()}},
	} {
		if _, err := pool.Exec(ctx, v.sql, v.args...); err != nil {
			t.Fatal(err)
		}
	}
	changed, err := store.Read(ctx, contextPrincipal(access.ReadIntegration), query)
	if err != nil || len(changed.MergeRequests.Items) != 1 || len(changed.MergeRequests.Items[0].Pipelines) != 1 || changed.MergeRequests.Items[0].Pipelines[0].ID != 2 || !*changed.MergeRequests.Items[0].ExpectedHeadMatches || changed.MergeRequests.Items[0].CommitsComplete {
		t.Fatal("old pipeline survived head change", changed, err)
	}
	if _, err := pool.Exec(ctx, "UPDATE forge_connections SET blocked=true; UPDATE work_tasks SET available=false"); err != nil {
		t.Fatal(err)
	}
	hidden, err := store.Read(ctx, contextPrincipal(access.ReadCapture, access.ReadIntegration), taskQuery)
	if err != nil || hidden.Task != nil || len(hidden.MergeRequests.Items) != 0 || len(hidden.Captures.Items) != 1 {
		t.Fatal("unavailable provider source disclosed", hidden, err)
	}
	taskQuery.CountryID = repository
	hidden, err = store.Read(ctx, contextPrincipal(access.ReadIntegration), taskQuery)
	if err != nil || hidden.Task != nil {
		t.Fatal("cross-country task disclosed", hidden, err)
	}
}

func TestContextPackMRDiscoveryHasIndependentAuthorizedPagination(t *testing.T) {
	pool, _, _, _ := automationFixture(t)
	ctx := context.Background()
	seedLinkedMR(t, pool, true, 42)
	if _, err := pool.Exec(ctx, "UPDATE commits SET confirmed_at=now()"); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := pool.QueryRow(ctx, `SELECT model FROM forge_mr_observations WHERE repository_id=$1 AND iid=7`, repository).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var base forge.MergeRequest
	json.Unmarshal(raw, &base)
	for iid := int64(8); iid <= 12; iid++ {
		m := base
		m.IID = iid
		m.ID = 100 + iid
		m.URL = fmt.Sprintf("https://gitlab.internal/team/project/-/merge_requests/%d", iid)
		raw, _ := json.Marshal(m)
		for _, v := range []struct {
			sql  string
			args []any
		}{
			{`INSERT INTO forge_mr_codes(repository_id,iid,code_key,complete) VALUES($1,$2,$3,true)`, []any{repository, iid, m.CodeKey()}},
			{`INSERT INTO forge_mr_observations(repository_id,iid,digest,code_key,model,provider_updated_at) VALUES($1,$2,$3,$4,$5,now())`, []any{repository, iid, m.Digest(), m.CodeKey(), raw}},
			{`INSERT INTO forge_merge_requests(repository_id,iid,digest,provider_updated_at) VALUES($1,$2,$3,now())`, []any{repository, iid, m.Digest()}},
			{`INSERT INTO forge_mr_commits(repository_id,iid,code_key,source_project_id,sha) VALUES($1,$2,$3,42,$4)`, []any{repository, iid, m.CodeKey(), strings.Repeat("a", 40)}},
		} {
			if _, err := pool.Exec(ctx, v.sql, v.args...); err != nil {
				t.Fatal(err)
			}
		}
	}
	store := postgres.NewContextStore(pool)
	q := pack.Query{ProjectID: project, CountryID: taskCountry, TaskID: taskUUID, Limit: 6}
	first, err := store.Read(ctx, contextPrincipal(access.ReadCapture, access.ReadIntegration), q)
	if err != nil || len(first.MergeRequests.Items) != 4 || first.MergeRequests.Next == nil {
		t.Fatal(first, err)
	}
	q.MRAfter = first.MergeRequests.Next
	second, err := store.Read(ctx, contextPrincipal(access.ReadCapture, access.ReadIntegration), q)
	if err != nil || len(second.MergeRequests.Items) != 2 || second.MergeRequests.Next != nil || second.MergeRequests.Items[0].IID != 11 {
		t.Fatal(second, err)
	}
}
