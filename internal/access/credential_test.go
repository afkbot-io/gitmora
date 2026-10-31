package access_test

import (
	"strings"
	"testing"
	"time"
	"workgraph.local/platform/internal/access"
)

func TestCredentialsHaveIndependentSecretsAndScopedImmutableRecords(t *testing.T) {
	now := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)
	scopes := []access.Permission{access.ReadCapture, access.WriteCapture, access.ReadCapture}
	first, err := access.IssueCredential("33333333-3333-4333-8333-333333333333", "Laptop", scopes, now, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	second, err := access.IssueCredential(first.Record.UserID, "Laptop 2", scopes, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if first.Secret == second.Secret || first.Record.ID == second.Record.ID || !strings.HasPrefix(first.Secret, "wg_") {
		t.Fatal("credential entropy or identity failure")
	}
	if first.Record.Hash == first.Secret || access.CredentialHash(first.Secret) != first.Record.Hash {
		t.Fatal("invalid storage digest")
	}
	scopes[0] = access.Permission("invented")
	if len(first.Record.Scopes) != 2 || !(access.Principal{UserID: first.Record.UserID, Scopes: first.Record.Scopes}).Has(access.WriteCapture) {
		t.Fatal("scope ownership failure")
	}
	if !first.Record.ExpiresAt.Equal(now.Add(24 * time.Hour)) {
		t.Fatal("expiration mismatch")
	}
}

func TestInvalidCredentialInputAndUnknownScopesRejected(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		user, label string
		scopes      []access.Permission
		ttl         time.Duration
	}{
		{"", "Laptop", []access.Permission{access.ReadCapture}, time.Hour},
		{"33333333-3333-4333-8333-333333333333", "", []access.Permission{access.ReadCapture}, time.Hour},
		{"33333333-3333-4333-8333-333333333333", "Laptop", nil, time.Hour},
		{"33333333-3333-4333-8333-333333333333", "Laptop", []access.Permission{"invented"}, time.Hour},
		{"33333333-3333-4333-8333-333333333333", "Laptop", []access.Permission{access.ReadCapture}, 0},
		{"33333333-3333-4333-8333-333333333333", "Laptop", []access.Permission{access.ReadCapture}, 366 * 24 * time.Hour},
	} {
		if _, err := access.IssueCredential(tc.user, tc.label, tc.scopes, now, tc.ttl); err == nil {
			t.Fatal("invalid credential accepted")
		}
	}
	for _, raw := range []string{"", "wg_short", strings.Repeat("x", 200), "Bearer wg_example"} {
		if access.CredentialHash(raw) != "" {
			t.Fatal("malformed credential was hashed")
		}
	}
}
