package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/collector/domain"
	"workgraph.local/platform/internal/identity/native"
)

func TestAuthenticationHelpAndValidationWorkOutsideARepositoryWithoutOpeningASecretStore(t *testing.T) {
	for _, args := range [][]string{{"auth"}, {"auth", "--help"}, {"auth", "login", "--help"}, {"auth", "status", "--help"}, {"auth", "logout", "--help"}} {
		var out bytes.Buffer
		if e := run(context.Background(), args, strings.NewReader(""), &out, &out); e != nil || !strings.Contains(out.String(), "auth") {
			t.Fatal("auth help requires repository or native store", args, e)
		}
	}
	for _, args := range [][]string{{"auth", "login"}, {"auth", "login", "--server", "https://secret@invalid"}, {"auth", "login", "--server", "https://workgraph.internal", "--ttl", "1s"}, {"auth", "status", "--server", "https://workgraph.internal", "--replace"}, {"auth", "logout", "--server", "https://workgraph.internal", "--no-browser"}} {
		var out bytes.Buffer
		if e := run(context.Background(), args, strings.NewReader(""), &out, &out); !errors.Is(e, native.ErrInvalid) {
			t.Fatal("malformed auth input reached OS/repository", args, e)
		}
	}
}

func TestCollectorExplicitEnvironmentNeverFallsThroughToTheKeychainAndNativeReadsAreBatchBounded(t *testing.T) {
	ctx := context.Background()
	cfg := domain.Config{ServerURL: "https://WORKGRAPH.internal:443/", TokenEnv: "WORKGRAPH_TOKEN"}
	r, _ := native.NewPendingRecord("https://workgraph.internal", "Fixture", []access.Permission{access.ManageCredentials, access.WriteCapture}, 86400)
	r.Pending = nil
	r.Account = &native.Account{UserID: "11111111-1111-4111-8111-111111111111", TokenID: "22222222-2222-4222-8222-222222222222", DisplayName: "Fixture", Scopes: []access.Permission{access.WriteCapture, access.ManageCredentials}, ExpiresAt: time.Now().Add(time.Hour)}
	reads := 0
	load := func(_ context.Context, server string) (native.Record, error) {
		reads++
		if server != r.Server {
			t.Fatal("noncanonical credential namespace")
		}
		return r, nil
	}
	for _, value := range []string{"", "invalid"} {
		source := collectorTokenSource(cfg, func(string) (string, bool) { return value, true }, load, time.Now)
		if _, e := source(ctx); !errors.Is(e, native.ErrUnauthorized) || reads != 0 {
			t.Fatal("explicit bad token used keychain fallback", e)
		}
	}
	source := collectorTokenSource(cfg, func(string) (string, bool) { return "", false }, load, time.Now)
	for range 4 {
		key, e := source(ctx)
		if e != nil || key != r.Secret {
			t.Fatal(e)
		}
	}
	if reads != 1 {
		t.Fatal("one drain batch launched repeated native helpers", reads)
	}
	r.Account.ExpiresAt = time.Now().Add(-time.Second)
	source = collectorTokenSource(cfg, func(string) (string, bool) { return "", false }, load, time.Now)
	if _, e := source(ctx); !errors.Is(e, native.ErrUnauthorized) {
		t.Fatal("expired local metadata used", e)
	}
}
