package bootstrap_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/bootstrap"
)

type adminStore struct {
	calls int
	err   error
	setup bootstrap.AdminSetup
}

func (s *adminStore) InitializeAdmin(_ context.Context, setup bootstrap.AdminSetup) error {
	s.calls++
	s.setup = setup
	return s.err
}

func TestAdministratorInitializationRequiresNoInventedProviderAndPublishesOnlyAfterCommit(t *testing.T) {
	port := &adminStore{}
	now := time.Now()
	service := bootstrap.NewAdminService(port, func() time.Time { return now })
	r, e := service.Initialize(context.Background(), "Administrator", 24*time.Hour)
	if e != nil || r.UserID == "" || access.CredentialHash(r.Credential.Secret) != port.setup.Credential.Hash {
		t.Fatal("initial administrator", e)
	}
	if !r.Credential.Record.ExpiresAt.Equal(now.Add(24 * time.Hour)) {
		t.Fatal("unexpected bootstrap lifetime")
	}
	p := access.Principal{Scopes: r.Credential.Record.Scopes}
	for _, scope := range []access.Permission{access.ReadDirectory, access.ManagePlatform, access.ManageCredentials, access.WriteCapture} {
		if !p.Has(scope) {
			t.Fatal("initial operator cannot configure/use the platform")
		}
	}
	port.err = bootstrap.ErrAlreadyInitialized
	if r, e = service.Initialize(context.Background(), "Another", time.Hour); !errors.Is(e, bootstrap.ErrAlreadyInitialized) || r.Credential.Secret != "" {
		t.Fatal("uncommitted credential published", e)
	}
	before := port.calls
	for _, input := range []struct {
		name string
		ttl  time.Duration
	}{{"", time.Hour}, {"bad\nname", time.Hour}, {"Valid", time.Second}, {"Valid", 91 * 24 * time.Hour}} {
		if _, e = service.Initialize(context.Background(), input.name, input.ttl); !errors.Is(e, bootstrap.ErrInvalid) {
			t.Fatal("invalid root parameters", e)
		}
	}
	if port.calls != before {
		t.Fatal("invalid root reached persistence")
	}
}
