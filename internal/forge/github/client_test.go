package github_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"workgraph.local/platform/internal/forge/application"
	"workgraph.local/platform/internal/forge/domain"
	"workgraph.local/platform/internal/forge/github"
)

const repositoryURL = "https://github.com/example/project"
const commitSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestPublicRepositoryIdentityCommitAndCredentialBoundaries(t *testing.T) {
	private := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Method != "GET" {
			t.Error("unexpected credential or mutation")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/repos/example/project", "/repositories/42":
			json.NewEncoder(w).Encode(map[string]any{"id": 42, "private": private, "html_url": repositoryURL, "name": "project"})
		case "/repos/example/project/commits/" + commitSHA:
			json.NewEncoder(w).Encode(map[string]any{"sha": commitSHA})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := github.New(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := client.LookupRepository(context.Background(), repositoryURL)
	if err != nil || resolved.Provider != "github" || resolved.ID != 42 {
		t.Fatal(resolved, err)
	}
	repo := domain.Repository{ID: "11111111-1111-4111-8111-111111111111", Provider: "github", BaseURL: repositoryURL, ProjectID: 42}
	if err = client.VerifyProject(context.Background(), repo, ""); err != nil {
		t.Fatal(err)
	}
	found, err := client.ConfirmCommit(context.Background(), repo, "", commitSHA)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	repo.ProjectID = 99
	if err = client.VerifyProject(context.Background(), repo, ""); err == nil {
		t.Fatal("wrong repository accepted")
	}
	repo.ProjectID = 42
	private = true
	if err = client.VerifyProject(context.Background(), repo, ""); err == nil {
		t.Fatal("private repository accepted in anonymous mode")
	}
	if _, err = client.LookupRepository(context.Background(), "https://other.invalid/example/project"); err == nil {
		t.Fatal("lookup selected an external destination")
	}
}

func TestRedirectsAndRateLimitsDoNotLeakTokensOrBecomeAnonymous(t *testing.T) {
	calls := 0
	sink := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(200) }))
	defer sink.Close()
	status := 302
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fixture-only-token" {
			t.Error("expected explicit token")
		}
		if status == 302 {
			w.Header().Set("Location", sink.URL)
		} else {
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.Header().Set("X-RateLimit-Reset", time.Now().Add(time.Minute).Format("unused"))
			w.Header().Set("Retry-After", "60")
		}
		w.WriteHeader(status)
	}))
	defer server.Close()
	client, err := github.New(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	repo := domain.Repository{Provider: "github", BaseURL: repositoryURL, ProjectID: 42}
	if err = client.VerifyProject(context.Background(), repo, "fixture-only-token"); err == nil || calls != 0 {
		t.Fatal("redirect followed", calls, err)
	}
	status = 403
	err = client.VerifyProject(context.Background(), repo, "fixture-only-token")
	var provider *application.ProviderError
	if !errors.As(err, &provider) || provider.Code != "provider_rate_limited" || !provider.Retryable || provider.RetryAfter != time.Minute {
		t.Fatal("rate-limit policy", err)
	}
}

func TestPaginationCannotChangeAuthorityPathFiltersOrSkipPages(t *testing.T) {
	link := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if link != "" {
			w.Header().Set("Link", link)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[]`))
	}))
	defer server.Close()
	client, err := github.New(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	path := "/repos/example/project/pulls?direction=desc&page=2&per_page=50&sort=updated&state=all"
	repo := domain.Repository{Provider: "github", BaseURL: repositoryURL, ProjectID: 42}
	cursor := domain.Cursor{Kind: domain.MergeRequests, Page: 1, After: time.Now().Add(-time.Hour), Before: time.Now()}
	for _, target := range []string{
		"https://other.invalid" + path,
		server.URL + "/repos/other/project/pulls?direction=desc&page=2&per_page=50&sort=updated&state=all",
		server.URL + "/repos/example/project/pulls?page=2",
		server.URL + "/repos/example/project/pulls?direction=desc&page=3&per_page=50&sort=updated&state=all",
		server.URL + path + "&page=2",
	} {
		link = "<" + target + `>; rel="next"`
		if _, err := client.Changes(context.Background(), repo, "", cursor); err == nil {
			t.Fatal("untrusted pagination accepted", target)
		}
	}
	link = "<" + server.URL + path + `>; rel="next"`
	page, err := client.Changes(context.Background(), repo, "", cursor)
	if err != nil || page.NextPage != 2 {
		t.Fatal("valid bounded continuation rejected", page, err)
	}
}
