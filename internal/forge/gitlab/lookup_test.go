package gitlab_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"workgraph.local/platform/internal/forge/gitlab"
)

func TestPrivatePathLookupPinsConfiguredBaseAndCanonicalIdentity(t *testing.T) {
	var base string
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.EscapedPath() != "/gitlab/api/v4/projects/team%2Fsub%2Fproject" || r.Header.Get("PRIVATE-TOKEN") != "dedicated-read-token" {
			t.Error("wrong scope or credential", r.URL.EscapedPath())
		}
		json.NewEncoder(w).Encode(map[string]any{"id": 42, "web_url": base + "/team/sub/project", "path_with_namespace": "team/sub/project"})
	}))
	defer server.Close()
	base = server.URL + "/gitlab"
	c, e := gitlab.NewWithProxy([]string{base}, "")
	if e != nil {
		t.Fatal(e)
	}
	resolved, e := c.LookupRepositoryWithToken(context.Background(), base+"/team/sub/project.git", "dedicated-read-token")
	if e != nil || resolved.ID != 42 || resolved.URL != base {
		t.Fatal(resolved, e)
	}
	if _, e = c.LookupRepositoryWithToken(context.Background(), server.URL+"/foreign/team/project", "dedicated-read-token"); e == nil || calls != 1 {
		t.Fatal("foreign path acquired credentials", e, calls)
	}
}
