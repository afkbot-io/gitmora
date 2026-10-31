package gitlab_test

import (
	"context"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"workgraph.local/platform/internal/forge/domain"
	"workgraph.local/platform/internal/forge/gitlab"
)

func TestCodeProviderBindsRawBlobToExactSHAPathAndEnumeratesDiffPages(t *testing.T) {
	sha := strings.Repeat("a", 40)
	filePath := "src/код+value %.go"
	raw := []byte("package auth\nfunc RefreshToken(){}\n")
	hash := sha1.Sum(append([]byte(fmt.Sprintf("blob %d\x00", len(raw))), raw...))
	blob := fmt.Sprintf("%x", hash)
	wrong := false
	rawCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != "synthetic-token" {
			t.Error("credential missing")
		}
		if strings.HasSuffix(r.URL.Path, "/diff") {
			if r.URL.Query().Get("page") != "1" {
				t.Error("page missing")
			}
			w.Header().Set("X-Next-Page", "2")
			_ = json.NewEncoder(w).Encode([]map[string]any{{"new_path": filePath, "b_mode": "100644", "diff": "omitted"}, {"new_path": "link", "b_mode": "120000"}, {"new_path": "removed.go", "b_mode": "000000", "deleted_file": true}})
			return
		}
		if r.URL.Query().Get("ref") != sha {
			t.Error("mutable ref requested")
		}
		expected := "/gitlab/api/v4/projects/42/repository/files/" + url.PathEscape(filePath)
		if r.Method == "HEAD" {
			if r.URL.EscapedPath() != expected {
				t.Error("path escaping", r.URL.EscapedPath())
			}
			w.Header().Set("X-Gitlab-Blob-Id", blob)
			w.Header().Set("X-Gitlab-Commit-Id", sha)
			w.Header().Set("X-Gitlab-Ref", sha)
			w.Header().Set("X-Gitlab-File-Path", filePath)
			w.Header().Set("X-Gitlab-Size", fmt.Sprint(len(raw)))
			if wrong {
				w.Header().Set("X-Gitlab-Commit-Id", strings.Repeat("b", 40))
			}
			return
		}
		if r.URL.EscapedPath() != expected+"/raw" || r.URL.Query().Get("lfs") != "false" {
			t.Error("unexpected raw destination", r.URL)
		}
		rawCalls++
		_, _ = w.Write(raw)
	}))
	defer server.Close()
	client, _ := gitlab.NewWithProxy([]string{server.URL + "/gitlab"}, "")
	repo := domain.Repository{ID: "11111111-1111-4111-8111-111111111111", BaseURL: server.URL + "/gitlab", ProjectID: 42}
	page, err := client.CodeDiff(context.Background(), repo, "synthetic-token", sha, 1)
	if err != nil || page.NextPage != 2 || len(page.Files) != 3 || page.Files[1].Skip != "symlink" || page.Files[2].Skip != "deleted" {
		t.Fatal(page, err)
	}
	observation, err := client.CodeFile(context.Background(), repo, "synthetic-token", sha, filePath)
	if err != nil || observation.File == nil || observation.File.Identity.BlobID != blob || observation.File.Content != string(raw) {
		t.Fatal(observation, err)
	}
	wrong = true
	if _, err = client.CodeFile(context.Background(), repo, "synthetic-token", sha, filePath); err == nil || rawCalls != 1 {
		t.Fatal("wrong commit fetched or accepted", err)
	}
}
func TestCodeProviderMarksMissingAndOversizedWithoutDownloadingBodies(t *testing.T) {
	mode := 404
	rawCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "HEAD" {
			rawCalls++
		}
		if mode == 404 {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("X-Gitlab-Blob-Id", strings.Repeat("b", 40))
		w.Header().Set("X-Gitlab-Commit-Id", strings.Repeat("a", 40))
		w.Header().Set("X-Gitlab-Ref", strings.Repeat("a", 40))
		w.Header().Set("X-Gitlab-File-Path", "large.bin")
		w.Header().Set("X-Gitlab-Size", fmt.Sprint(domain.MaxRawFileBytes+1))
	}))
	defer server.Close()
	client, _ := gitlab.NewWithProxy([]string{server.URL}, "")
	repo := domain.Repository{ID: "11111111-1111-4111-8111-111111111111", BaseURL: server.URL, ProjectID: 42}
	result, err := client.CodeFile(context.Background(), repo, "synthetic-token", strings.Repeat("a", 40), "large.bin")
	if err != nil || !result.Missing {
		t.Fatal(result, err)
	}
	mode = 200
	result, err = client.CodeFile(context.Background(), repo, "synthetic-token", strings.Repeat("a", 40), "large.bin")
	if err != nil || result.Skip != "too_large" || rawCalls != 0 {
		t.Fatal(result, err)
	}
}
