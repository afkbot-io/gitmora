package postgres_test

import (
	"context"
	"errors"
	"testing"
	"workgraph.local/platform/internal/identity/domain"
	"workgraph.local/platform/internal/platform/postgres"
)

func TestRegistrationCreatesOnlyNewExactIdentityWithoutProjectOrOperatorAuthority(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	s := postgres.NewIdentityStore(pool)
	ctx := context.Background()
	config := oidcConfig()
	config.RegistrationEnabled = true
	if e := s.ActivatePolicy(ctx, config.Fingerprint(), true); e != nil {
		t.Fatal(e)
	}
	who := domain.Identity{Issuer: config.Issuer, Subject: "new-exact-provider-subject", DisplayName: "New engineer"}
	b, e := s.Register(ctx, who, config.Fingerprint())
	if e != nil || b.UserID == "" {
		t.Fatal(b, e)
	}
	again, e := s.Register(ctx, who, config.Fingerprint())
	if e != nil || again.ID != b.ID || again.UserID != b.UserID {
		t.Fatal("duplicate account", again, e)
	}
	if count(t, pool, "users") != 3 || count(t, pool, "memberships") != 2 || count(t, pool, "directory_operators") != 0 {
		t.Fatal("signup gained implicit authority")
	}
	if _, e = pool.Exec(ctx, `UPDATE identity_bindings SET enabled=false WHERE id=$1`, b.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Register(ctx, who, config.Fingerprint()); !errors.Is(e, domain.ErrLogin) {
		t.Fatal("disabled binding resurrected", e)
	}
	who.Subject = "another-subject"
	if _, e = s.Register(ctx, who, "stale"); !errors.Is(e, domain.ErrLogin) {
		t.Fatal("stale policy admitted account", e)
	}
}

func TestDisablingRegistrationFencesOtherInstancesWithoutDisablingExistingAccounts(t *testing.T) {
	pool := database(t)
	s := postgres.NewIdentityStore(pool)
	ctx := context.Background()
	config := oidcConfig()
	if e := s.ActivatePolicy(ctx, config.Fingerprint(), true); e != nil {
		t.Fatal(e)
	}
	who := domain.Identity{Issuer: config.Issuer, Subject: "existing", DisplayName: "Existing account"}
	original, e := s.Register(ctx, who, config.Fingerprint())
	if e != nil {
		t.Fatal(e)
	}
	if e = s.ActivatePolicy(ctx, config.Fingerprint(), false); e != nil {
		t.Fatal(e)
	}
	if existing, e := s.Resolve(ctx, who); e != nil || existing.ID != original.ID {
		t.Fatal("signup disable blocked login", e)
	}
	who.Subject = "new-subject"
	if _, e = s.Register(ctx, who, config.Fingerprint()); !errors.Is(e, domain.ErrLogin) {
		t.Fatal("stale API instance bypassed closed registration", e)
	}
	if _, e = s.CreateAccountOffline(ctx, who); e != nil {
		t.Fatal("operator creation requires public signup", e)
	}
}
