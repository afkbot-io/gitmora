package github

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"workgraph.local/platform/internal/forge/application"
	"workgraph.local/platform/internal/forge/domain"
)

func TestEnterpriseTokenLookupIdentityAndPrefixedPagination(t *testing.T) {
	calls := 0
	var web string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer dedicated-read-token" {
			t.Error("wrong credential")
		}
		if r.URL.Path != "/forge/api/v3/repos/team/project" && r.URL.Path != "/forge/api/v3/repositories/42" {
			t.Error("wrong API prefix", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{"id": 42, "private": true, "html_url": web + "/team/project", "name": "project"})
	}))
	defer server.Close()
	web = server.URL + "/forge"
	router, e := NewRouterWithEnterpriseProxy("https://api.github.com", "", "", []string{web}, true)
	if e != nil {
		t.Fatal(e)
	}
	client := router.clients[web]
	client.http.Transport.(*http.Transport).TLSClientConfig = server.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	repo := domain.Repository{Provider: "github", BaseURL: web + "/team/project", ProjectID: 42}
	value, e := router.LookupRepositoryWithToken(context.Background(), repo.BaseURL, "dedicated-read-token")
	if e != nil || value.ID != 42 || value.URL != repo.BaseURL {
		t.Fatal(value, e)
	}
	if e = router.VerifyProject(context.Background(), repo, "dedicated-read-token"); e != nil {
		t.Fatal(e)
	}
	header := http.Header{"Link": []string{"<" + web + "/api/v3/repositories/42/pulls?page=2&per_page=50>; rel=\"next\""}}
	page, e := client.nextPage(header, "/repos/team/project/pulls?page=1&per_page=50", 1, repo)
	if e != nil || page != 2 {
		t.Fatal("prefixed pagination", page, e)
	}
	header.Set("Link", "<"+web+"/api/v3/repositories/99/pulls?page=2&per_page=50>; rel=\"next\"")
	if _, e = client.nextPage(header, "/repos/team/project/pulls?page=1&per_page=50", 1, repo); e == nil {
		t.Fatal("foreign repository page accepted")
	}
	_, e = router.LookupRepositoryWithToken(context.Background(), "https://unapproved.example/team/project", "dedicated-read-token")
	var provider *application.ProviderError
	if !errors.As(e, &provider) || provider.Code != "provider_endpoint_not_allowed" || calls != 2 {
		t.Fatal("unapproved destination reached provider", calls, e)
	}
}
