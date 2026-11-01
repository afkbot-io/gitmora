package github_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"workgraph.local/platform/internal/forge/domain"
	"workgraph.local/platform/internal/forge/github"
)

func TestPullRequestDiffFenceExactCIAndProviderCommitCap(t *testing.T) {
	head := commitSHA
	base := strings.Repeat("b", 40)
	mergeBase := strings.Repeat("c", 40)
	count := 1
	changeOnCommit := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var value any
		switch {
		case r.URL.Path == "/repos/example/project/pulls/7":
			value = map[string]any{"id": 70, "number": 7, "state": "open", "title": "Review", "body": "Evidence", "html_url": repositoryURL + "/pull/7", "updated_at": "2026-11-01T00:00:00Z", "merged": false, "merge_commit_sha": strings.Repeat("d", 40), "commits": count, "head": map[string]any{"sha": head, "repo": map[string]int{"id": 42}}, "base": map[string]any{"sha": base, "repo": map[string]int{"id": 42}}}
		case strings.HasPrefix(r.URL.Path, "/repos/example/project/compare/"):
			value = map[string]any{"base_commit": map[string]string{"sha": base}, "merge_base_commit": map[string]string{"sha": mergeBase}}
		case r.URL.Path == "/repos/example/project/pulls/7/commits":
			values := []map[string]string{{"sha": head}}
			if count > 250 {
				values = nil
				for i := 0; i < 50; i++ {
					values = append(values, map[string]string{"sha": fmt.Sprintf("%040x", i+1)})
				}
			}
			value = values
			if changeOnCommit {
				head = strings.Repeat("e", 40)
			}
		case r.URL.Path == "/repos/example/project/actions/runs":
			value = map[string]any{"workflow_runs": []any{map[string]any{"id": 12, "head_sha": head, "head_branch": "main", "status": "completed", "conclusion": "success", "event": "push", "html_url": repositoryURL + "/actions/runs/12", "updated_at": "2026-11-01T00:00:00Z", "repository": map[string]int{"id": 42}}}}
		default:
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(value)
	}))
	defer server.Close()
	client, err := github.New(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	repo := domain.Repository{Provider: "github", BaseURL: repositoryURL, ProjectID: 42}
	model, found, err := client.MR(context.Background(), repo, "", 7)
	if err != nil || !found || model.Base != mergeBase || model.Start != base || model.Merge != "" || !model.CodeReady() {
		t.Fatal("invented diff/merge state", model, err)
	}
	page, err := client.MRCommits(context.Background(), repo, "", model, 1)
	if err != nil || len(page.Commits) != 1 || page.NextPage != 0 || page.Truncated {
		t.Fatal(page, err)
	}
	changeOnCommit = true
	page, err = client.MRCommits(context.Background(), repo, "", model, 1)
	if err != nil || page.Replacement == nil || len(page.Commits) != 0 {
		t.Fatal("changed PR head accepted", page, err)
	}
	changeOnCommit = false
	count = 251
	model, _, err = client.MR(context.Background(), repo, "", 7)
	if err != nil {
		t.Fatal(err)
	}
	page, err = client.MRCommits(context.Background(), repo, "", model, 5)
	if err != nil || !page.Truncated || page.NextPage != 0 {
		t.Fatal("provider cap declared complete", page, err)
	}
	window := domain.Cursor{Kind: domain.Pipelines, Page: 1, After: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Before: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)}
	changes, err := client.Changes(context.Background(), repo, "", window)
	if err != nil || len(changes.Pipelines) != 1 || changes.Pipelines[0].SHA != head {
		t.Fatal("CI detached from exact head", changes, err)
	}
}
