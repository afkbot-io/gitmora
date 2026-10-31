package gitlab

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"workgraph.local/platform/internal/forge/domain"
	"workgraph.local/platform/internal/platform/egress"
	"workgraph.local/platform/internal/platform/netroute"
)

func TestPrivateGitLabTLSCustomPortPrefixAndCorporateCA(t *testing.T) {
	var requests atomic.Int64
	sha := strings.Repeat("a", 40)
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("PRIVATE-TOKEN") != "synthetic-gitlab-token" {
			t.Error("provider credential lost inside TLS")
		}
		switch r.URL.Path {
		case "/gitlab/api/v4/projects/42":
			_, _ = w.Write([]byte(`{"id":42}`))
		case "/gitlab/api/v4/projects/42/repository/commits/" + sha:
			_, _ = w.Write([]byte(`{"id":"` + sha + `"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer upstream.Close()
	route, err := netroute.NewBoundDialer(strings.TrimPrefix(upstream.URL, "https://"), netroute.Private, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	relay, err := egress.New(context.Background(), route, egress.Options{MaxTunnels: 2, MaxLifetime: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	proxy := httptest.NewServer(relay)
	defer proxy.Close()
	defer relay.Close()
	base := upstream.URL + "/gitlab"
	client, err := NewWithProxy([]string{base}, proxy.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := client.http.Transport.(*http.Transport)
	defer transport.CloseIdleConnections()
	repository := domain.Repository{BaseURL: base, ProjectID: 42}
	if err := client.VerifyProject(context.Background(), repository, "synthetic-gitlab-token"); err == nil || requests.Load() != 0 {
		t.Fatal("untrusted TLS certificate reached authenticated provider handler")
	}
	pool := x509.NewCertPool()
	pool.AddCert(upstream.Certificate())
	// Test-only corporate trust injection; production uses the mounted OS/SSL_CERT_FILE trust roots.
	transport.TLSClientConfig = &tls.Config{RootCAs: pool}
	if err := client.VerifyProject(context.Background(), repository, "synthetic-gitlab-token"); err != nil {
		t.Fatal(err)
	}
	if found, err := client.ConfirmCommit(context.Background(), repository, "synthetic-gitlab-token", sha); err != nil || !found {
		t.Fatal("private commit verification failed", err)
	}
	repository.BaseURL = upstream.URL + "/other-installation"
	if err := client.VerifyProject(context.Background(), repository, "synthetic-gitlab-token"); err == nil || requests.Load() != 2 {
		t.Fatal("non-allowlisted installation prefix reached provider")
	}
}
