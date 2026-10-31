package gitlab_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"workgraph.local/platform/internal/forge/application"
	"workgraph.local/platform/internal/forge/domain"
	"workgraph.local/platform/internal/forge/gitlab"
)

func TestProviderUsesAllowlistedImmutableProjectAndExactCommitID(t *testing.T) {
	sha := strings.Repeat("a", 40)
	wrong := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") != "synthetic-token" {
			t.Error("token not supplied")
		}
		switch r.URL.Path {
		case "/gitlab/api/v4/projects/42":
			_, _ = w.Write([]byte(`{"id":42,"arbitrary_provider_field":true}`))
		case "/gitlab/api/v4/projects/42/repository/commits/" + sha:
			if wrong {
				_, _ = w.Write([]byte(`{"id":"` + strings.Repeat("b", 40) + `"}`))
			} else {
				_, _ = w.Write([]byte(`{"id":"` + sha + `"}`))
			}
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	provider, err := gitlab.NewWithProxy([]string{server.URL + "/gitlab"}, "")
	if err != nil {
		t.Fatal(err)
	}
	repo := domain.Repository{ID: "11111111-1111-4111-8111-111111111111", BaseURL: server.URL + "/gitlab", ProjectID: 42}
	if err := provider.VerifyProject(context.Background(), repo, "synthetic-token"); err != nil {
		t.Fatal(err)
	}
	if found, err := provider.ConfirmCommit(context.Background(), repo, "synthetic-token", sha); err != nil || !found {
		t.Fatal("commit not confirmed")
	}
	wrong = true
	if _, err := provider.ConfirmCommit(context.Background(), repo, "synthetic-token", sha); err == nil {
		t.Fatal("wrong object confirmed")
	}
	repo.BaseURL = server.URL
	if err := provider.VerifyProject(context.Background(), repo, "synthetic-token"); err == nil {
		t.Fatal("nonallowlisted origin/path accepted")
	}
}

func TestProviderDoesNotForwardCredentialsAcrossRedirectOrExposeErrorBodies(t *testing.T) {
	forwarded := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded = true }))
	defer destination.Close()
	mode := 302
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mode == 302 {
			w.Header().Set("Location", destination.URL)
		} else {
			w.Header().Set("Retry-After", "120")
		}
		w.WriteHeader(mode)
		_, _ = w.Write([]byte("secret-provider-response"))
	}))
	defer source.Close()
	provider, err := gitlab.NewWithProxy([]string{source.URL}, "")
	if err != nil {
		t.Fatal(err)
	}
	repo := domain.Repository{BaseURL: source.URL, ProjectID: 42}
	err = provider.VerifyProject(context.Background(), repo, "synthetic-token")
	if err == nil || forwarded || strings.Contains(err.Error(), "secret-provider-response") {
		t.Fatal("unsafe redirect or diagnostics")
	}
	mode = 429
	err = provider.VerifyProject(context.Background(), repo, "synthetic-token")
	var failure *application.ProviderError
	if !errors.As(err, &failure) || !failure.Retryable || failure.RetryAfter != 2*time.Minute {
		t.Fatal("rate limit state missing")
	}
}
