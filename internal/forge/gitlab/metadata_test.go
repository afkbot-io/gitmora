package gitlab_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"workgraph.local/platform/internal/forge/domain"
	"workgraph.local/platform/internal/forge/gitlab"
)

func TestProviderMetadataPaginationIsScopedAndUsesRequiredFilters(t *testing.T) {
	hostile := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != "synthetic-token" {
			t.Error("missing token")
		}
		if r.URL.Query().Get("order_by") != "updated_at" || r.URL.Query().Get("updated_before") == "" {
			t.Error("collection window is not fixed")
		}
		if hostile {
			w.Header().Set("Link", `<https://outside.invalid/api/v4/projects/42/merge_requests?page=2>; rel="next"`)
		} else {
			w.Header().Set("X-Next-Page", "2")
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{{"iid": 7, "project_id": 42, "updated_at": "2026-10-29T10:00:00Z"}})
	}))
	defer server.Close()
	provider, err := gitlab.NewWithProxy([]string{server.URL}, "")
	if err != nil {
		t.Fatal(err)
	}
	repo := domain.Repository{BaseURL: server.URL, ProjectID: 42}
	cursor := domain.Cursor{Kind: domain.MergeRequests, Page: 1, After: time.Unix(0, 0), Before: time.Now()}
	page, err := provider.Changes(context.Background(), repo, "synthetic-token", cursor)
	if err != nil || page.NextPage != 2 || len(page.MergeRequests) != 1 {
		t.Fatal("provider page omitted rows", err)
	}
	hostile = true
	if _, err := provider.Changes(context.Background(), repo, "synthetic-token", cursor); err == nil {
		t.Fatal("foreign pagination target accepted")
	}
}

func TestMRCommitPageRejectsAHeadChangeDuringFetch(t *testing.T) {
	head := strings.Repeat("a", 40)
	reads := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/commits") {
			_ = json.NewEncoder(w).Encode([]map[string]string{{"id": head}})
			return
		}
		reads++
		current := head
		if reads > 1 {
			current = strings.Repeat("d", 40)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 100, "iid": 7, "project_id": 42, "source_project_id": 42, "state": "opened", "sha": current, "updated_at": "2026-10-29T10:00:00Z", "diff_refs": map[string]string{"head_sha": current, "base_sha": strings.Repeat("b", 40), "start_sha": strings.Repeat("c", 40)}})
	}))
	defer server.Close()
	provider, err := gitlab.NewWithProxy([]string{server.URL}, "")
	if err != nil {
		t.Fatal(err)
	}
	expected := domain.MergeRequest{ID: 100, IID: 7, ProjectID: 42, SourceProjectID: 42, State: "opened", Head: head, Base: strings.Repeat("b", 40), Start: strings.Repeat("c", 40), UpdatedAt: time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)}
	page, err := provider.MRCommits(context.Background(), domain.Repository{BaseURL: server.URL, ProjectID: 42}, "synthetic-token", expected, 1)
	if err != nil || page.Replacement == nil || len(page.Commits) != 0 {
		t.Fatal("moving MR commit set was attributed to the old head", err)
	}
}

func TestUnavailableCollectionIsExplicitAndDoesNotPretendToBeAnEmptySnapshot(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	provider, err := gitlab.NewWithProxy([]string{server.URL}, "")
	if err != nil {
		t.Fatal(err)
	}
	page, err := provider.Changes(context.Background(), domain.Repository{BaseURL: server.URL, ProjectID: 42}, "synthetic-token", domain.Cursor{Kind: domain.Pipelines, Page: 1, After: time.Unix(0, 0), Before: time.Now()})
	if err != nil || !page.Unavailable {
		t.Fatal("disabled/missing collection was conflated with a completed empty stream")
	}
}
