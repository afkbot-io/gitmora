package postgres_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"workgraph.local/platform/internal/access"
	directory "workgraph.local/platform/internal/directory/domain"
	"workgraph.local/platform/internal/identity/domain"
	"workgraph.local/platform/internal/platform/postgres"
)

func oidcConfig() domain.Config {
	return domain.Config{Issuer: "https://idp.internal/realms/workgraph", ClientID: "workgraph", PublicURL: "https://workgraph.internal", AuthStyle: "basic", SessionSeconds: 28800, Policy: domain.Policy}
}

func TestIdentityBindingIsExplicitImmutableAndDisablingClosesSessions(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	dir := postgres.NewDirectoryStore(pool)
	if e := dir.GrantOperatorOffline(ctx, subject); e != nil {
		t.Fatal(e)
	}
	p := directoryPrincipal(t, pool, subject, access.ReadDirectory, access.ManagePlatform)
	store := postgres.NewIdentityStore(pool)
	cfg := oidcConfig()
	if e := store.Activate(ctx, cfg.Fingerprint()); e != nil {
		t.Fatal(e)
	}
	input := domain.BindInput{RequestID: requestID(t), ID: "77777777-7777-4777-8777-777777777777", UserID: reader, Subject: "subject-1"}
	if _, e := store.Resolve(ctx, domain.Identity{Issuer: cfg.Issuer, Subject: input.Subject}); !errors.Is(e, domain.ErrLogin) {
		t.Fatal("implicit provisioning occurred", e)
	}
	receipt, created, e := store.Bind(ctx, p, cfg.Issuer, input)
	if e != nil || !created {
		t.Fatal(receipt, created, e)
	}
	repeat, created, e := store.Bind(ctx, p, cfg.Issuer, input)
	if e != nil || created || repeat != receipt {
		t.Fatal("binding replay changed", repeat, e)
	}
	changed := input
	changed.RequestID = requestID(t)
	changed.ID = "88888888-8888-4888-8888-888888888888"
	changed.UserID = subject
	if _, _, e = store.Bind(ctx, p, cfg.Issuer, changed); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("subject was transferred", e)
	}
	binding, e := store.Resolve(ctx, domain.Identity{Issuer: cfg.Issuer, Subject: input.Subject})
	if e != nil {
		t.Fatal(e)
	}
	cookie, _ := domain.NewSecret(domain.SessionPrefix)
	csrf, _ := domain.NewSecret(domain.CSRFPrefix)
	record, e := access.IssueCredential(reader, "Browser", domain.InteractiveScopes(), time.Now(), 8*time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	seed := domain.SessionSeed{ID: "99999999-9999-4999-8999-999999999999", CookieHash: domain.SecretHash(domain.SessionPrefix, cookie), CSRFHash: domain.SecretHash(domain.CSRFPrefix, csrf), Configuration: cfg.Fingerprint(), Binding: binding, Credential: record.Record}
	session, e := store.OpenSession(ctx, seed)
	if e != nil || session.Principal.UserID != reader {
		t.Fatal(session, e)
	}
	got, e := store.Session(ctx, seed.CookieHash, cfg.Fingerprint())
	if e != nil || got.Principal.TokenID != record.Record.ID {
		t.Fatal(got, e)
	}
	if _, e = postgres.NewCredentialStore(pool).Authenticate(ctx, record.Secret); !errors.Is(e, access.ErrUnauthenticated) {
		t.Fatal("browser credential accepted as bearer", e)
	}
	off := false
	command := domain.BindingChange{RequestID: requestID(t), ExpectedVersion: binding.Version, Enabled: &off}
	if _, _, e = store.ChangeBinding(ctx, p, cfg.Issuer, binding.ID, command); e != nil {
		t.Fatal(e)
	}
	if _, e = store.Session(ctx, seed.CookieHash, cfg.Fingerprint()); !errors.Is(e, access.ErrUnauthenticated) {
		t.Fatal("disabled binding retained session", e)
	}
	list, e := store.Bindings(ctx, p, cfg.Issuer, directory.Cursor{})
	if e != nil || len(list.Items) != 1 || list.Items[0].Enabled {
		t.Fatal(list, e)
	}
	on := true
	command.RequestID = requestID(t)
	command.ExpectedVersion = list.Items[0].Version
	command.Enabled = &on
	if _, _, e = store.ChangeBinding(ctx, p, cfg.Issuer, binding.ID, command); e != nil {
		t.Fatal(e)
	}
	if _, e = store.Session(ctx, seed.CookieHash, cfg.Fingerprint()); !errors.Is(e, access.ErrUnauthenticated) {
		t.Fatal("re-enable resurrected session", e)
	}
	if e = store.Activate(ctx, ""); e != nil {
		t.Fatal(e)
	}
	if e = store.Activate(ctx, cfg.Fingerprint()); e != nil {
		t.Fatal(e)
	}
	if _, e = store.Session(ctx, seed.CookieHash, cfg.Fingerprint()); !errors.Is(e, access.ErrUnauthenticated) {
		t.Fatal("configuration rollback resurrected session", e)
	}
}

func TestIdentityLoginFlowIsBrowserBoundExpiringAndSingleUse(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	store := postgres.NewIdentityStore(pool)
	config := oidcConfig().Fingerprint()
	if e := store.Activate(ctx, config); e != nil {
		t.Fatal(e)
	}
	state, _ := domain.NewSecret(domain.StatePrefix)
	browser, _ := domain.NewSecret(domain.FlowPrefix)
	flow := domain.Flow{StateHash: domain.SecretHash(domain.StatePrefix, state), BrowserHash: domain.SecretHash(domain.FlowPrefix, browser), Configuration: config}
	if e := store.CreateFlow(ctx, flow); e != nil {
		t.Fatal(e)
	}
	wrong := flow
	wrong.BrowserHash = domain.Hash([]byte("other-browser"))
	if e := store.ConsumeFlow(ctx, wrong); !errors.Is(e, domain.ErrLogin) {
		t.Fatal("flow stolen by another browser", e)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 5)
	for range 5 {
		wg.Go(func() { errs <- store.ConsumeFlow(ctx, flow) })
	}
	wg.Wait()
	close(errs)
	accepted := 0
	for e := range errs {
		if e == nil {
			accepted++
		} else if !errors.Is(e, domain.ErrLogin) {
			t.Fatal(e)
		}
	}
	if accepted != 1 {
		t.Fatal("code callback consumed more than once", accepted)
	}
	state, _ = domain.NewSecret(domain.StatePrefix)
	flow.StateHash = domain.SecretHash(domain.StatePrefix, state)
	if e := store.CreateFlow(ctx, flow); e != nil {
		t.Fatal(e)
	}
	if _, e := pool.Exec(ctx, `UPDATE identity_login_flows SET expires_at=now()-interval '1 second'`); e != nil {
		t.Fatal(e)
	}
	if e := store.ConsumeFlow(ctx, flow); !errors.Is(e, domain.ErrLogin) {
		t.Fatal("expired flow accepted", e)
	}
}
