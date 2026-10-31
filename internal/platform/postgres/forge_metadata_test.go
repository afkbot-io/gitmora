package postgres_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	captureapp "workgraph.local/platform/internal/capture/application"
	forgeapp "workgraph.local/platform/internal/forge/application"
	"workgraph.local/platform/internal/forge/domain"
	"workgraph.local/platform/internal/forge/gitlab"
	"workgraph.local/platform/internal/platform/postgres"
	"workgraph.local/platform/internal/platform/secretbox"
)

func TestProviderMetadataResumesPagesAndPreservesMRHistoryAndSquashLineage(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	var mu sync.Mutex
	failSecond := true
	merged := false
	firstPages := 0
	updated := time.Now().UTC().Add(-time.Hour)
	sha := func(letter string) string { return strings.Repeat(letter, 40) }
	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("PRIVATE-TOKEN") != "synthetic-provider-token" {
			w.WriteHeader(401)
			return
		}
		prefix := "/api/v4/projects/42"
		path := strings.TrimPrefix(r.URL.Path, prefix)
		switch path {
		case "":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 42})
			return
		case "/merge_requests":
			if r.URL.Query().Get("page") == "1" {
				firstPages++
				w.Header().Set("X-Next-Page", "2")
				_ = json.NewEncoder(w).Encode([]map[string]any{{"iid": 7, "project_id": 42, "updated_at": updated}, {"iid": 8, "project_id": 42, "updated_at": updated}})
				return
			}
			if failSecond {
				w.WriteHeader(503)
				return
			}
			w.Header().Set("X-Next-Page", "")
			_ = json.NewEncoder(w).Encode([]map[string]any{{"iid": 9, "project_id": 42, "updated_at": updated}})
			return
		case "/pipelines":
			pipeline := map[string]any{"id": 11, "project_id": 42, "sha": sha("a"), "status": "success", "ref": "main", "updated_at": updated, "source": "push"}
			if r.URL.Query().Get("source") == "parent_pipeline" {
				pipeline["id"] = 13
				pipeline["source"] = "parent_pipeline"
			}
			_ = json.NewEncoder(w).Encode([]any{pipeline})
			return
		case "/deployments":
			_ = json.NewEncoder(w).Encode([]any{map[string]any{"id": 21, "sha": sha("f"), "status": "success", "ref": "main", "updated_at": updated, "environment": map[string]any{"id": 5, "name": "production"}, "deployable": map[string]any{"id": 70, "pipeline": map[string]int{"id": 11}}}})
			return
		}
		if strings.HasPrefix(path, "/repository/commits/") {
			_ = json.NewEncoder(w).Encode(map[string]string{"id": strings.TrimPrefix(path, "/repository/commits/")})
			return
		}
		segments := strings.Split(strings.Trim(path, "/"), "/")
		if len(segments) >= 2 && segments[0] == "merge_requests" {
			iid, _ := strconv.ParseInt(segments[1], 10, 64)
			head := sha("a")
			state := "opened"
			squash := ""
			mergeSHA := ""
			source := int64(42)
			if iid == 8 {
				source = 99
			}
			if merged && iid == 7 {
				head = sha("d")
				state = "merged"
				squash = sha("f")
				mergeSHA = sha("e")
			}
			if len(segments) == 3 && segments[2] == "commits" {
				commits := []map[string]string{{"id": head}, {"id": sha("b")}}
				if r.URL.Query().Get("page") == "1" {
					w.Header().Set("X-Next-Page", "2")
				} else {
					commits = []map[string]string{{"id": sha("c")}}
					w.Header().Set("X-Next-Page", "")
				}
				_ = json.NewEncoder(w).Encode(commits)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"id": 100 + iid, "iid": iid, "project_id": 42, "source_project_id": source, "state": state, "title": "DB_PASSWORD=provider-never-persist", "sha": head, "merge_commit_sha": mergeSHA, "squash_commit_sha": squash, "updated_at": updated, "diff_refs": map[string]string{"head_sha": head, "base_sha": sha("b"), "start_sha": sha("c")}})
			return
		}
		w.WriteHeader(404)
	}))
	defer providerServer.Close()
	if _, err := pool.Exec(ctx, "UPDATE repositories SET gitlab_url=$1 WHERE id=$2", providerServer.URL, repository); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "UPDATE memberships SET role='admin' WHERE user_id=$1", subject); err != nil {
		t.Fatal(err)
	}
	create := func() *forgeapp.Service {
		box, err := secretbox.New("test", map[string][]byte{"test": bytes.Repeat([]byte{1}, 32)})
		if err != nil {
			t.Fatal(err)
		}
		client, err := gitlab.NewWithProxy([]string{providerServer.URL}, "")
		if err != nil {
			t.Fatal(err)
		}
		return forgeapp.NewService(postgres.NewForgeStore(pool), box, client, time.Now)
	}
	service := create()
	if _, err := service.Connect(ctx, subject, repository, providerSecrets()); err != nil {
		t.Fatal(err)
	}
	failed := false
	for range 15 {
		worked, err := service.ReconcileOnce(ctx)
		if err != nil {
			failed = true
			break
		}
		if !worked {
			break
		}
	}
	if !failed {
		t.Fatal("injected second-page failure did not surface")
	}
	var page int
	if err := pool.QueryRow(ctx, "SELECT next_page FROM forge_sync_streams WHERE repository_id=$1 AND kind='merge_requests'", repository).Scan(&page); err != nil || page != 2 {
		t.Fatal("failed page advanced or lost progress")
	}
	mu.Lock()
	failSecond = false
	mu.Unlock()
	if _, err := pool.Exec(ctx, "UPDATE forge_connections SET next_sync_at=now() WHERE repository_id=$1", repository); err != nil {
		t.Fatal(err)
	}
	service = create()
	drain := func() {
		t.Helper()
		for range 80 {
			worked, err := service.ReconcileOnce(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !worked {
				return
			}
		}
		t.Fatal("metadata queue did not drain")
	}
	drain()
	mu.Lock()
	calls := firstPages
	mu.Unlock()
	if calls != 1 {
		t.Fatal("restart replayed an already committed source page")
	}
	view, err := service.MergeRequest(ctx, subject, repository, forgeapp.MetadataQuery{IID: 7})
	if err != nil || !view.CommitsComplete || len(view.SourceCommits) != 3 {
		t.Fatalf("MR source membership incomplete: %+v %v", view, err)
	}
	old := view.Observation
	if strings.Contains(view.Model.Title, "provider-never-persist") {
		t.Fatal("provider secret persisted")
	}
	pipelines, err := service.Pipelines(ctx, subject, repository, forgeapp.MetadataQuery{})
	if err != nil || len(pipelines.Items) != 2 {
		t.Fatal("regular or child CI missing", err)
	}
	deployments, err := service.Deployments(ctx, subject, repository, forgeapp.MetadataQuery{})
	if err != nil || len(deployments.Items) != 1 || deployments.Items[0].SHA != sha("f") {
		t.Fatal("deployment SHA missing", err)
	}
	if _, err := service.MergeRequest(ctx, reader, otherRepository, forgeapp.MetadataQuery{IID: 7}); err == nil {
		t.Fatal("unrelated repository evidence exposed")
	}
	mu.Lock()
	merged = true
	updated = updated.Add(time.Minute)
	mu.Unlock()
	if err := service.RequestSync(ctx, subject, repository); err != nil {
		t.Fatal(err)
	}
	drain()
	view, err = service.MergeRequest(ctx, subject, repository, forgeapp.MetadataQuery{IID: 7})
	if err != nil || view.Model.Head != sha("d") || len(view.Results) != 2 || !view.CommitsComplete {
		t.Fatal("merged head/result state missing", err)
	}
	historical, err := service.MergeRequest(ctx, subject, repository, forgeapp.MetadataQuery{IID: 7, Observation: old})
	if err != nil || historical.Model.Head != sha("a") || !historical.CommitsComplete {
		t.Fatal("original immutable head was lost")
	}
	var forbidden int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM forge_commit_lineage WHERE repository_id=$1 AND iid=7 AND code_key=$2 AND source_sha=$3", repository, view.CodeKey, sha("a")).Scan(&forbidden); err != nil || forbidden != 0 {
		t.Fatal("discarded historical head was falsely attributed to squash")
	}
	var forkScope int64
	if err := pool.QueryRow(ctx, "SELECT source_project_id FROM forge_mr_commits WHERE repository_id=$1 AND iid=8 LIMIT 1", repository).Scan(&forkScope); err != nil || forkScope != 99 {
		t.Fatal("fork origin was conflated with target project")
	}
}

func TestFreshPipelineWebhookWakesAnAlreadyCompletedStream(t *testing.T) {
	service, _, pool, _ := forgeFixture(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, "UPDATE memberships SET role='admin' WHERE user_id=$1", subject); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Connect(ctx, subject, repository, providerSecrets()); err != nil {
		t.Fatal(err)
	}
	for range 10 {
		worked, err := service.ReconcileOnce(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !worked {
			break
		}
	}
	delivery := signedDelivery(`{"object_kind":"pipeline","project":{"id":42}}`, "pipeline-after-cycle")
	delivery.Event = "Pipeline Hook"
	if _, err := service.Receive(ctx, repository, delivery); err != nil {
		t.Fatal(err)
	}
	status, err := service.Status(ctx, subject, repository)
	if err != nil {
		t.Fatal(err)
	}
	for _, stream := range status.Streams {
		if stream.Kind == domain.Pipelines && stream.LastCompleteAt != nil {
			t.Fatal("fresh webhook did not wake the completed pipeline collection")
		}
	}
}

func TestProviderPipelineIdentityCannotMoveToAnotherCommit(t *testing.T) {
	service, store, pool, _ := forgeFixture(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, "UPDATE memberships SET role='admin' WHERE user_id=$1", subject); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Connect(ctx, subject, repository, providerSecrets()); err != nil {
		t.Fatal(err)
	}
	write := func(sha string) error {
		lease, found, err := store.Claim(ctx)
		if err != nil || !found {
			t.Fatal("missing metadata lease")
		}
		for range 5 {
			unit, found, err := store.NextMetadata(ctx, lease)
			if err != nil || !found {
				t.Fatal("missing metadata unit")
			}
			if unit.Cursor.Kind == domain.Pipelines {
				return store.ApplyChanges(ctx, lease, unit, domain.ChangePage{Pipelines: []domain.Pipeline{{ID: 11, ProjectID: 42, SHA: sha, Status: "success", UpdatedAt: time.Now()}}})
			}
			if err := store.ApplyChanges(ctx, lease, unit, domain.ChangePage{}); err != nil {
				t.Fatal(err)
			}
		}
		t.Fatal("pipeline stream was not scheduled")
		return nil
	}
	if err := write(strings.Repeat("a", 40)); err != nil {
		t.Fatal(err)
	}
	if err := service.RequestSync(ctx, subject, repository); err != nil {
		t.Fatal(err)
	}
	if err := write(strings.Repeat("b", 40)); err == nil {
		t.Fatal("existing CI identity was moved to another tested SHA")
	}
}

func TestPreparingMRRetryIsScheduledAtItsOwnDueTime(t *testing.T) {
	service, store, pool, _ := forgeFixture(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, "UPDATE memberships SET role='admin' WHERE user_id=$1", subject); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Connect(ctx, subject, repository, providerSecrets()); err != nil {
		t.Fatal(err)
	}
	lease, found, err := store.Claim(ctx)
	if err != nil || !found {
		t.Fatal("missing lease")
	}
	for range 5 {
		unit, found, err := store.NextMetadata(ctx, lease)
		if err != nil || !found {
			t.Fatal("missing metadata unit")
		}
		if unit.MR != nil {
			model := domain.MergeRequest{ID: 107, IID: 7, ProjectID: 42, SourceProjectID: 42, State: "opened", UpdatedAt: time.Now()}
			if err := store.ApplyMR(ctx, lease, unit, &model); err != nil {
				t.Fatal(err)
			}
			break
		}
		page := domain.ChangePage{}
		if unit.Cursor.Kind == domain.MergeRequests {
			page.MergeRequests = []domain.MRHint{{IID: 7, UpdatedAt: time.Now()}}
		}
		if err := store.ApplyChanges(ctx, lease, unit, page); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Finish(ctx, lease, forgeapp.Outcome{}); err != nil {
		t.Fatal(err)
	}
	status, err := service.Status(ctx, subject, repository)
	if err != nil || time.Until(status.NextSyncAt) > time.Minute {
		t.Fatal("preparing MR retry was postponed behind the periodic scan interval")
	}
}

func TestUnavailableStreamRetainsIncompleteStateWhileCommitVerificationContinues(t *testing.T) {
	service, store, pool, _ := forgeFixture(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, "UPDATE memberships SET role='admin' WHERE user_id=$1", subject); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Connect(ctx, subject, repository, providerSecrets()); err != nil {
		t.Fatal(err)
	}
	captures := captureapp.NewService(postgres.NewCaptureStore(pool))
	if _, err := captures.Ingest(ctx, subject, snapshot()); err != nil {
		t.Fatal(err)
	}
	lease, found, err := store.Claim(ctx)
	if err != nil || !found {
		t.Fatal("missing lease")
	}
	unit, found, err := store.NextMetadata(ctx, lease)
	if err != nil || !found {
		t.Fatal("missing stream")
	}
	if err := store.ApplyChanges(ctx, lease, unit, domain.ChangePage{Unavailable: true}); err != nil {
		t.Fatal(err)
	}
	if err := store.Finish(ctx, lease, forgeapp.Outcome{More: true}); err != nil {
		t.Fatal(err)
	}
	if worked, err := service.ReconcileOnce(ctx); err != nil || !worked {
		t.Fatal("unavailable stream blocked independent verification", err)
	}
	stored, err := captures.Get(ctx, subject, repository, snapshot().SessionID, 0)
	if err != nil || len(stored.ConfirmedCommits) != 1 {
		t.Fatal("pending commit was stranded")
	}
	status, err := service.Status(ctx, subject, repository)
	if err != nil || status.Blocked {
		t.Fatal("connection was incorrectly blocked")
	}
	for _, stream := range status.Streams {
		if stream.Kind == unit.Cursor.Kind && (stream.LastCompleteAt != nil || stream.ErrorCode != "provider_collection_unavailable") {
			t.Fatal("unavailable collection was published as complete")
		}
	}
}
