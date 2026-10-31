package gitlab_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"workgraph.local/platform/internal/forge/domain"
	"workgraph.local/platform/internal/forge/gitlab"
)

func TestExplicitPrivateProxyReceivesOnlyCONNECTWithoutProviderCredentials(t *testing.T) {
	var calls atomic.Int64
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "CONNECT" || r.RequestURI != "gitlab.com:443" || r.Header.Get("PRIVATE-TOKEN") != "" || r.Header.Get("Authorization") != "" {
			t.Error("provider credential or wrong route entered CONNECT")
		}
		w.WriteHeader(403)
	}))
	defer proxy.Close()
	client, e := gitlab.NewWithProxy([]string{"https://gitlab.com"}, proxy.URL)
	if e != nil {
		t.Fatal(e)
	}
	if e = client.VerifyProject(context.Background(), domain.Repository{BaseURL: "https://gitlab.com", ProjectID: 42}, "synthetic-provider-token"); e == nil || calls.Load() != 1 {
		t.Fatal("explicit proxy was ignored", e)
	}
	if _, e = gitlab.NewWithProxy([]string{"http://127.0.0.1:1234"}, proxy.URL); e == nil {
		t.Fatal("proxied HTTP base could send a provider credential outside TLS")
	}
	for _, endpoint := range []string{"http://user:private@127.0.0.1:8080", "http://127.0.0.1:8080/path", "http://127.0.0.1:8080?key=private", "http://169.254.169.254:8080", "http://1.1.1.1:8080", "https://proxy:8080"} {
		if _, e = gitlab.NewWithProxy([]string{"https://gitlab.com"}, endpoint); e == nil {
			t.Fatal("unsafe proxy configuration accepted")
		}
	}
}
