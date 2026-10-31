package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIdentityGatewayHealthRequiresExactIssuerAtNumericLoopbackDiscovery(t *testing.T) {
	value := `{"issuer":"https://identity.internal/realms/gitmora","authorization_endpoint":"https://identity.internal/authorize"}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, value)
	}))
	defer server.Close()
	endpoint := server.URL + "/realms/gitmora/.well-known/openid-configuration"
	args := []string{"healthcheck", "--url", endpoint, "--oidc-issuer", "https://identity.internal/realms/gitmora"}
	if err := run(context.Background(), args, io.Discard); err != nil {
		t.Fatal("valid local discovery probe denied", err)
	}
	for _, raw := range []string{`{"issuer":"https://other.internal/realms/gitmora"}`, `{"issuer":"https://identity.internal/realms/gitmora","issuer":"https://identity.internal/realms/gitmora"}`, `{"status":"ready"}`} {
		value = raw
		if err := run(context.Background(), args, io.Discard); err == nil {
			t.Fatal("wrong/ambiguous/missing issuer passed")
		}
	}
	for _, args := range [][]string{{"healthcheck", "--oidc-issuer", "https://identity.internal/realms/gitmora"}, {"healthcheck", "--url", endpoint, "--oidc-issuer", "http://identity.internal/realms/gitmora"}, {"healthcheck", "--url", "http://identity.internal/realms/gitmora/.well-known/openid-configuration", "--oidc-issuer", "https://identity.internal/realms/gitmora"}} {
		if err := run(context.Background(), args, io.Discard); err == nil {
			t.Fatal("widened probe destination/path")
		}
	}
}

func TestIdentityServerCommandHelpNeedsNoApplicationDatabase(t *testing.T) {
	t.Setenv("WORKGRAPH_DATABASE_URL", "")
	if err := run(context.Background(), []string{"identity-server", "--help"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"identity-server", "init", "--help"}, io.Discard); err != nil {
		t.Fatal(err)
	}
}
