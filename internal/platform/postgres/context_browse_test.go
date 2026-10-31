package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"workgraph.local/platform/internal/access"
	captureapp "workgraph.local/platform/internal/capture/application"
	"workgraph.local/platform/internal/contextpack/domain"
	forge "workgraph.local/platform/internal/forge/domain"
	"workgraph.local/platform/internal/platform/postgres"
	work "workgraph.local/platform/internal/work/domain"
)

func TestBrowseFiltersUnavailableRowsBeforeLimitAndKeepsLargeCursorExact(t *testing.T) {
	pool, _, tasks, _ := automationFixture(t)
	ctx := context.Background()
	seedLinkedMR(t, pool, true, 42)
	if _, err := tasks.ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := pool.QueryRow(ctx, "SELECT model FROM work_task_observations WHERE task_id=$1", taskUUID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var task work.Task
	if err := json.Unmarshal(raw, &task); err != nil {
		t.Fatal(err)
	}
	task.ID = "00000000-0000-4000-8000-000000000001"
	task.Number = 9007199254740993
	task.Title = "Hidden before visible"
	raw, _ = json.Marshal(task)
	if _, err := pool.Exec(ctx, `INSERT INTO work_task_observations(project_id,country_id,task_id,model,digest) VALUES($1,$2,$3,$4,$5)`, project, taskCountry, task.ID, raw, task.Digest()); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO work_tasks(project_id,country_id,task_id,digest,available,read_started_at) VALUES($1,$2,$3,$4,false,now())`, project, taskCountry, task.ID, task.Digest()); err != nil {
		t.Fatal(err)
	}
	store := postgres.NewContextStore(pool)
	p := contextPrincipal(access.ReadIntegration)
	page, err := store.Browse(ctx, p, domain.BrowseQuery{ProjectID: project, Kind: "task", Limit: 1})
	if err != nil || len(page.Items) != 1 || page.Items[0].Query.TaskID != taskUUID || page.Next != nil {
		t.Fatal("unavailable task consumed limit", page, err)
	}
	if _, err := pool.Exec(ctx, "UPDATE work_tasks SET available=true WHERE task_id=$1", task.ID); err != nil {
		t.Fatal(err)
	}
	page, err = store.Browse(ctx, p, domain.BrowseQuery{ProjectID: project, Kind: "task", Limit: 1})
	if err != nil || len(page.Items) != 1 || page.Items[0].Number != "9007199254740993" {
		t.Fatal("task number rounded", page, err)
	}
	if err := pool.QueryRow(ctx, "SELECT model FROM forge_mr_observations WHERE iid=7").Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var model forge.MergeRequest
	if err := json.Unmarshal(raw, &model); err != nil {
		t.Fatal(err)
	}
	for _, iid := range []int64{6, 9007199254740993} {
		model.IID = iid
		model.ID = iid + 100
		model.URL = fmt.Sprintf("https://gitlab.internal/team/project/-/merge_requests/%d", iid)
		raw, _ = json.Marshal(model)
		if _, err := pool.Exec(ctx, `INSERT INTO forge_mr_codes(repository_id,iid,code_key,complete) VALUES($1,$2,$3,true)`, repository, iid, model.CodeKey()); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO forge_mr_observations(repository_id,iid,digest,code_key,model,provider_updated_at) VALUES($1,$2,$3,$4,$5,now())`, repository, iid, model.Digest(), model.CodeKey(), raw); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO forge_merge_requests(repository_id,iid,digest,provider_updated_at,available) VALUES($1,$2,$3,now(),$4)`, repository, iid, model.Digest(), iid != 6); err != nil {
			t.Fatal(err)
		}
	}
	q := domain.BrowseQuery{ProjectID: project, Kind: "merge_request", Limit: 1}
	page, err = store.Browse(ctx, p, q)
	if err != nil || len(page.Items) != 1 || page.Items[0].Query.MR != 7 || page.Next == nil {
		t.Fatal("unavailable MR consumed limit", page, err)
	}
	q.After = page.Next
	page, err = store.Browse(ctx, p, q)
	if err != nil || len(page.Items) != 1 || page.Items[0].Cursor.IID != "9007199254740993" || page.Next != nil {
		t.Fatal("MR cursor rounded or skipped", page, err)
	}
	if _, err := pool.Exec(ctx, "UPDATE forge_connections SET last_synced_at=now()-interval '11 minutes'"); err != nil {
		t.Fatal(err)
	}
	q.After = nil
	page, err = store.Browse(ctx, p, q)
	if err != nil || len(page.Items) != 0 {
		t.Fatal("expired provider availability retained MR headers", page, err)
	}
}

func TestBrowseScopesBeforePaginationAndHidesRevokedSources(t *testing.T) {
	pool, _, tasks, _ := automationFixture(t)
	ctx := context.Background()
	seedLinkedMR(t, pool, true, 42)
	if _, err := tasks.ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	snap := snapshot()
	snap.SessionID = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	if _, err := captureapp.NewService(postgres.NewCaptureStore(pool)).Ingest(ctx, subject, snap); err != nil {
		t.Fatal(err)
	}
	store := postgres.NewContextStore(pool)
	p := contextPrincipal(access.ReadCapture)
	q := domain.BrowseQuery{ProjectID: project, Kind: "session", Limit: 1}
	first, err := store.Browse(ctx, p, q)
	if err != nil || len(first.Items) != 1 || first.Next == nil {
		t.Fatal(first, err)
	}
	q.After = first.Next
	second, err := store.Browse(ctx, p, q)
	if err != nil || len(second.Items) != 1 || second.Next != nil || second.Items[0].Query.SessionID == first.Items[0].Query.SessionID {
		t.Fatal(second, err)
	}
	q = domain.BrowseQuery{ProjectID: project, Kind: "task", Limit: 1}
	if _, err := store.Browse(ctx, p, q); !errors.Is(err, access.ErrDenied) {
		t.Fatal("capture permission leaked tasks", err)
	}
	p = contextPrincipal(access.ReadIntegration)
	page, err := store.Browse(ctx, p, q)
	if err != nil || len(page.Items) != 1 || page.Items[0].Query.TaskID != taskUUID {
		t.Fatal(page, err)
	}
	q.Kind = "merge_request"
	page, err = store.Browse(ctx, p, q)
	if err != nil || len(page.Items) != 1 || page.Items[0].Query.MR != 7 || page.Items[0].Query.ExpectedHead == "" {
		t.Fatal(page, err)
	}
	if _, err := pool.Exec(ctx, "UPDATE forge_connections SET blocked=true; UPDATE work_tasks SET available=false"); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"task", "merge_request"} {
		q.Kind = kind
		page, err = store.Browse(ctx, p, q)
		if err != nil || len(page.Items) != 0 || page.Next != nil {
			t.Fatal("revoked source leaked into page", page, err)
		}
	}
	q.Kind = "session"
	if _, err := store.Browse(ctx, p, q); !errors.Is(err, access.ErrDenied) {
		t.Fatal("integration permission leaked sessions", err)
	}
	q.ProjectID = "99999999-9999-4999-8999-999999999999"
	q.Kind = "task"
	if _, err := store.Browse(ctx, p, q); !errors.Is(err, access.ErrDenied) {
		t.Fatal("foreign project cursor broadened scope", err)
	}
	q.ProjectID = project
	if _, err := pool.Exec(ctx, "DELETE FROM memberships WHERE user_id=$1", reader); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Browse(ctx, p, q); !errors.Is(err, access.ErrDenied) {
		t.Fatal("removed member retained discovery", err)
	}
}
