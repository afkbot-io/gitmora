package postgres_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"workgraph.local/platform/internal/access"
	directory "workgraph.local/platform/internal/directory/domain"
	"workgraph.local/platform/internal/identity/domain"
	"workgraph.local/platform/internal/platform/postgres"
)

func cliBrowser(t *testing.T, pool *pgxpool.Pool, store *postgres.IdentityStore, user, external string) (domain.Session, string) {
	t.Helper()
	ctx := context.Background()
	cfg := oidcConfig()
	dir := postgres.NewDirectoryStore(pool)
	if e := dir.GrantOperatorOffline(ctx, subject); e != nil {
		t.Fatal(e)
	}
	operator := directoryPrincipal(t, pool, subject, access.ReadDirectory, access.ManagePlatform)
	i := domain.BindInput{RequestID: requestID(t), ID: requestID(t), UserID: user, Subject: external}
	if _, _, e := store.Bind(ctx, operator, cfg.Issuer, i); e != nil {
		t.Fatal(e)
	}
	b, e := store.Resolve(ctx, domain.Identity{Issuer: cfg.Issuer, Subject: external})
	if e != nil {
		t.Fatal(e)
	}
	cookie, _ := domain.NewSecret(domain.SessionPrefix)
	csrf, _ := domain.NewSecret(domain.CSRFPrefix)
	issued, e := access.IssueCredential(user, "Browser", domain.InteractiveScopes(), time.Now(), 8*time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	s, e := store.OpenSession(ctx, domain.SessionSeed{ID: requestID(t), CookieHash: domain.SecretHash(domain.SessionPrefix, cookie), CSRFHash: domain.SecretHash(domain.CSRFPrefix, csrf), Configuration: cfg.Fingerprint(), Binding: b, Credential: issued.Record})
	if e != nil {
		t.Fatal(e)
	}
	return s, cookie
}

func enrollmentInput(t *testing.T) (domain.CLIRequest, domain.CLISecrets) {
	t.Helper()
	s, e := domain.NewCLISecrets()
	if e != nil {
		t.Fatal(e)
	}
	return domain.CLIRequest{ID: requestID(t), DeviceHash: domain.SecretHash(domain.CLIDevicePrefix, s.Device), ProofHash: domain.SecretHash(domain.CLIProofPrefix, s.Proof), CredentialHash: access.CredentialHash(s.Credential), CodeHash: domain.CLICodeHash(s.Code), Label: "Owned collector", Scopes: []access.Permission{access.ManageCredentials, access.WriteCapture, access.ReadDirectory}, TTLSeconds: 86400}, s
}

func TestCLIDeviceCancellationFencesBrowserApprovalAndRequiresBothProofs(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	store := postgres.NewIdentityStore(pool)
	cfg := oidcConfig()
	if e := store.Activate(ctx, cfg.Fingerprint()); e != nil {
		t.Fatal(e)
	}
	browser, _ := cliBrowser(t, pool, store, subject, "cancellation-owner")
	i, secret := enrollmentInput(t)
	v, e := store.CreateCLIEnrollment(ctx, cfg.Fingerprint(), i)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = store.CancelCLIEnrollment(ctx, cfg.Fingerprint(), i.ID, i.ProofHash, i.DeviceHash); !errors.Is(e, domain.ErrLogin) {
		t.Fatal("swapped proofs cancelled request", e)
	}
	closed, e := store.CancelCLIEnrollment(ctx, cfg.Fingerprint(), i.ID, i.DeviceHash, i.ProofHash)
	if e != nil || closed.Status != "cancelled" || closed.UserID != "" || closed.TokenID != "" {
		t.Fatal("device cancellation", closed.Status, e)
	}
	if _, e = store.DecideCLIEnrollment(ctx, browser, cfg.Fingerprint(), i.CodeHash, v.Fingerprint(), true); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("forgotten request approved later", e)
	}
	if _, e = postgres.NewCredentialStore(pool).Authenticate(ctx, secret.Credential); !errors.Is(e, access.ErrUnauthenticated) {
		t.Fatal("cancelled key was active", e)
	}
	closed, e = store.CancelCLIEnrollment(ctx, cfg.Fingerprint(), i.ID, i.DeviceHash, i.ProofHash)
	if e != nil || closed.Status != "cancelled" {
		t.Fatal("uncertain cancellation replay", e)
	}
	inspected, e := store.InspectCLIEnrollment(ctx, browser, cfg.Fingerprint(), i.CodeHash)
	if e != nil || inspected.Status != "cancelled" {
		t.Fatal("cancelled browser metadata", e)
	}
	for range 8 {
		i, secret = enrollmentInput(t)
		v, e = store.CreateCLIEnrollment(ctx, cfg.Fingerprint(), i)
		if e != nil {
			t.Fatal(e)
		}
		var cancelErr, approveErr error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, cancelErr = store.CancelCLIEnrollment(ctx, cfg.Fingerprint(), i.ID, i.DeviceHash, i.ProofHash)
		}()
		go func() {
			defer wg.Done()
			_, approveErr = store.DecideCLIEnrollment(ctx, browser, cfg.Fingerprint(), i.CodeHash, v.Fingerprint(), true)
		}()
		wg.Wait()
		if (cancelErr == nil) == (approveErr == nil) {
			t.Fatal("both race outcomes succeeded or failed", cancelErr, approveErr)
		}
		if cancelErr != nil && !errors.Is(cancelErr, domain.ErrConflict) || approveErr != nil && !errors.Is(approveErr, domain.ErrConflict) {
			t.Fatal(cancelErr, approveErr)
		}
		final, e := store.PollCLIEnrollment(ctx, cfg.Fingerprint(), i.ID, i.DeviceHash, i.ProofHash)
		if e != nil {
			t.Fatal(e)
		}
		_, authErr := postgres.NewCredentialStore(pool).Authenticate(ctx, secret.Credential)
		if final.Status == "cancelled" && !errors.Is(authErr, access.ErrUnauthenticated) || final.Status == "approved" && authErr != nil {
			t.Fatal("race outcome disagrees with credential authority", final.Status, authErr)
		}
	}
}

func TestCLIEnrollmentRequiresExactFreshBrowserConsentAndSurvivesBrowserLogoutOnly(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	store := postgres.NewIdentityStore(pool)
	cfg := oidcConfig()
	if e := store.Activate(ctx, cfg.Fingerprint()); e != nil {
		t.Fatal(e)
	}
	browser, cookie := cliBrowser(t, pool, store, subject, "collector-owner")
	i, secrets := enrollmentInput(t)
	entry, e := store.CreateCLIEnrollment(ctx, cfg.Fingerprint(), i)
	if e != nil {
		t.Fatal(e)
	}
	repeat, e := store.CreateCLIEnrollment(ctx, cfg.Fingerprint(), i)
	if e != nil || repeat.Fingerprint() != entry.Fingerprint() || repeat.ExpiresAt != entry.ExpiresAt {
		t.Fatal("uncertain creation replay changed", e)
	}
	changed := i
	changed.Label = "Different input"
	if _, e = store.CreateCLIEnrollment(ctx, cfg.Fingerprint(), changed); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("registration retargeted", e)
	}
	creds := postgres.NewCredentialStore(pool)
	if _, e = creds.Authenticate(ctx, secrets.Credential); !errors.Is(e, access.ErrUnauthenticated) {
		t.Fatal("unapproved key became usable", e)
	}
	view, e := store.InspectCLIEnrollment(ctx, browser, cfg.Fingerprint(), i.CodeHash)
	if e != nil || view.Status != "pending" {
		t.Fatal(view, e)
	}
	if _, e = store.DecideCLIEnrollment(ctx, browser, cfg.Fingerprint(), i.CodeHash, strings.Repeat("f", 64), true); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("uninspected intent accepted", e)
	}
	approved, e := store.DecideCLIEnrollment(ctx, browser, cfg.Fingerprint(), i.CodeHash, view.Fingerprint(), true)
	if e != nil || approved.Status != "approved" || approved.UserID != subject || approved.CredentialExpiresAt == nil {
		t.Fatal(approved.Status, e)
	}
	ack, e := store.DecideCLIEnrollment(ctx, browser, cfg.Fingerprint(), i.CodeHash, view.Fingerprint(), true)
	if e != nil || ack.TokenID != approved.TokenID || !ack.CredentialExpiresAt.Equal(*approved.CredentialExpiresAt) {
		t.Fatal("uncertain consent created another key", e)
	}
	p, e := creds.Authenticate(ctx, secrets.Credential)
	if e != nil || p.UserID != subject || !p.Has(access.WriteCapture) || p.Has(access.ManagePlatform) {
		t.Fatal(p, e)
	}
	child, e := access.IssueCredential(subject, "Collector child", []access.Permission{access.WriteCapture}, time.Now(), time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = postgres.NewDirectoryStore(pool).IssueToken(ctx, p, directory.TokenInput{RequestID: requestID(t), Label: child.Record.Label, Scopes: child.Record.Scopes, TTLSeconds: 60}, child.Record); e != nil {
		t.Fatal(e)
	}
	if e = store.Logout(ctx, domain.SecretHash(domain.SessionPrefix, cookie), cfg.Fingerprint()); e != nil {
		t.Fatal(e)
	}
	if _, e = creds.Authenticate(ctx, secrets.Credential); e != nil {
		t.Fatal("local browser logout revoked independent CLI consent", e)
	}
	if _, e = store.PollCLIEnrollment(ctx, cfg.Fingerprint(), i.ID, i.DeviceHash, i.ProofHash); e != nil {
		t.Fatal(e)
	}
	if _, e = store.PollCLIEnrollment(ctx, cfg.Fingerprint(), i.ID, i.ProofHash, i.DeviceHash); !errors.Is(e, domain.ErrLogin) {
		t.Fatal("purpose-swapped polling proof accepted", e)
	}
	b, e := store.Resolve(ctx, domain.Identity{Issuer: cfg.Issuer, Subject: "collector-owner"})
	if e != nil {
		t.Fatal(e)
	}
	op := directoryPrincipal(t, pool, subject, access.ReadDirectory, access.ManagePlatform)
	off := false
	if _, _, e = store.ChangeBinding(ctx, op, cfg.Issuer, b.ID, domain.BindingChange{RequestID: requestID(t), ExpectedVersion: b.Version, Enabled: &off}); e != nil {
		t.Fatal(e)
	}
	for _, secret := range []string{secrets.Credential, child.Secret} {
		if _, e = creds.Authenticate(ctx, secret); !errors.Is(e, access.ErrUnauthenticated) {
			t.Fatal("binding loss retained root/child", e)
		}
	}
	if _, e = store.PollCLIEnrollment(ctx, cfg.Fingerprint(), i.ID, i.DeviceHash, i.ProofHash); !errors.Is(e, domain.ErrLogin) {
		t.Fatal("closed grant exposed as active", e)
	}
}

func TestCLIEnrollmentSerializesApprovalAndRejectsForgedStaleOrNarrowBrowser(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	store := postgres.NewIdentityStore(pool)
	cfg := oidcConfig()
	if e := store.Activate(ctx, cfg.Fingerprint()); e != nil {
		t.Fatal(e)
	}
	browser, _ := cliBrowser(t, pool, store, subject, "owner")
	other, _ := cliBrowser(t, pool, store, reader, "other")
	i, secrets := enrollmentInput(t)
	entry, e := store.CreateCLIEnrollment(ctx, cfg.Fingerprint(), i)
	if e != nil {
		t.Fatal(e)
	}
	fake := browser
	fake.ID = requestID(t)
	if _, e = store.InspectCLIEnrollment(ctx, fake, cfg.Fingerprint(), i.CodeHash); !errors.Is(e, access.ErrDenied) {
		t.Fatal("forged browser accepted", e)
	}
	if _, e = pool.Exec(ctx, `UPDATE identity_sessions SET created_at=now()-interval '6 minutes' WHERE id=$1`, browser.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = store.DecideCLIEnrollment(ctx, browser, cfg.Fingerprint(), i.CodeHash, entry.Fingerprint(), true); !errors.Is(e, domain.ErrFreshLogin) {
		t.Fatal("old browser issued long-lived root", e)
	}
	if _, e = pool.Exec(ctx, `UPDATE identity_sessions SET created_at=now() WHERE id=$1`, browser.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `UPDATE api_tokens SET scopes=ARRAY['credentials:manage']::text[] WHERE id=$1`, browser.Principal.TokenID); e != nil {
		t.Fatal(e)
	}
	if _, e = store.DecideCLIEnrollment(ctx, browser, cfg.Fingerprint(), i.CodeHash, entry.Fingerprint(), true); !errors.Is(e, access.ErrDenied) {
		t.Fatal("saved browser scopes overrode current grant", e)
	}
	if _, e = pool.Exec(ctx, `UPDATE api_tokens SET scopes=ARRAY['credentials:manage','capture:write','directory:read']::text[] WHERE id=$1`, browser.Principal.TokenID); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	ids := make(chan string, 2)
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, e := store.DecideCLIEnrollment(ctx, browser, cfg.Fingerprint(), i.CodeHash, entry.Fingerprint(), true)
			if e != nil {
				errs <- e
			} else {
				ids <- v.TokenID
			}
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for e := range errs {
		t.Fatal(e)
	}
	id := ""
	for next := range ids {
		if id != "" && id != next {
			t.Fatal("concurrent consent created two roots")
		}
		id = next
	}
	if id == "" {
		t.Fatal("no approval")
	}
	if _, e = store.DecideCLIEnrollment(ctx, other, cfg.Fingerprint(), i.CodeHash, entry.Fingerprint(), true); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("another account captured decided flow", e)
	}
	if e = store.Activate(ctx, ""); e != nil {
		t.Fatal(e)
	}
	if e = store.Activate(ctx, cfg.Fingerprint()); e != nil {
		t.Fatal(e)
	}
	if _, e = postgres.NewCredentialStore(pool).Authenticate(ctx, secrets.Credential); !errors.Is(e, access.ErrUnauthenticated) {
		t.Fatal("configuration rollback resurrected CLI grant", e)
	}
}

func TestCLIEnrollmentRejectionExpiryAndCredentialReuseDoNotCreateGrants(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	store := postgres.NewIdentityStore(pool)
	cfg := oidcConfig()
	if e := store.Activate(ctx, cfg.Fingerprint()); e != nil {
		t.Fatal(e)
	}
	browser, _ := cliBrowser(t, pool, store, subject, "rejecting-owner")
	i, s := enrollmentInput(t)
	entry, e := store.CreateCLIEnrollment(ctx, cfg.Fingerprint(), i)
	if e != nil {
		t.Fatal(e)
	}
	v, e := store.DecideCLIEnrollment(ctx, browser, cfg.Fingerprint(), i.CodeHash, entry.Fingerprint(), false)
	if e != nil || v.Status != "denied" || v.TokenID != "" {
		t.Fatal(v.Status, e)
	}
	if _, e = store.DecideCLIEnrollment(ctx, browser, cfg.Fingerprint(), i.CodeHash, entry.Fingerprint(), true); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("denial became approval", e)
	}
	if _, e = postgres.NewCredentialStore(pool).Authenticate(ctx, s.Credential); !errors.Is(e, access.ErrUnauthenticated) {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `UPDATE identity_cli_enrollments SET expires_at=now()-interval '1 second' WHERE id=$1`, i.ID); e != nil {
		t.Fatal(e)
	}
	if _, e = store.PollCLIEnrollment(ctx, cfg.Fingerprint(), i.ID, i.DeviceHash, i.ProofHash); !errors.Is(e, domain.ErrLogin) {
		t.Fatal("expired flow polled", e)
	}
}

func TestCLIEnrollmentCannotPresentARevokedRootAsApproved(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	store := postgres.NewIdentityStore(pool)
	cfg := oidcConfig()
	if e := store.Activate(ctx, cfg.Fingerprint()); e != nil {
		t.Fatal(e)
	}
	browser, _ := cliBrowser(t, pool, store, subject, "revoked-collector")
	i, _ := enrollmentInput(t)
	entry, e := store.CreateCLIEnrollment(ctx, cfg.Fingerprint(), i)
	if e != nil {
		t.Fatal(e)
	}
	approved, e := store.DecideCLIEnrollment(ctx, browser, cfg.Fingerprint(), i.CodeHash, entry.Fingerprint(), true)
	if e != nil {
		t.Fatal(e)
	}
	if e = postgres.NewCredentialStore(pool).Revoke(ctx, subject, approved.TokenID); e != nil {
		t.Fatal(e)
	}
	if _, e = store.InspectCLIEnrollment(ctx, browser, cfg.Fingerprint(), i.CodeHash); !errors.Is(e, domain.ErrLogin) {
		t.Fatal("inspection represented revoked root as approved", e)
	}
	if _, e = store.DecideCLIEnrollment(ctx, browser, cfg.Fingerprint(), i.CodeHash, entry.Fingerprint(), true); !errors.Is(e, domain.ErrLogin) {
		t.Fatal("receipt replay represented revoked root as approved", e)
	}
}

func TestCLIReauthenticationKeepsTheBrowserRootAndExpiryAndRequiresItsExactBinding(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	store := postgres.NewIdentityStore(pool)
	cfg := oidcConfig()
	if e := store.Activate(ctx, cfg.Fingerprint()); e != nil {
		t.Fatal(e)
	}
	browser, cookie := cliBrowser(t, pool, store, subject, "reauth-owner")
	binding, e := store.Resolve(ctx, domain.Identity{Issuer: cfg.Issuer, Subject: "reauth-owner"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `UPDATE identity_sessions SET created_at=now()-interval '6 minutes' WHERE id=$1`, browser.ID); e != nil {
		t.Fatal(e)
	}
	i, _ := enrollmentInput(t)
	flow, e := store.CreateCLIEnrollment(ctx, cfg.Fingerprint(), i)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = store.DecideCLIEnrollment(ctx, browser, cfg.Fingerprint(), i.CodeHash, flow.Fingerprint(), true); !errors.Is(e, domain.ErrFreshLogin) {
		t.Fatal(e)
	}
	fresh, e := store.ReauthenticateSession(ctx, domain.SecretHash(domain.SessionPrefix, cookie), cfg.Fingerprint(), binding)
	if e != nil || fresh.ID != browser.ID || fresh.Principal.TokenID != browser.Principal.TokenID || !fresh.ExpiresAt.Equal(browser.ExpiresAt) {
		t.Fatal("reauthentication replaced or extended browser", e)
	}
	if _, e = store.DecideCLIEnrollment(ctx, fresh, cfg.Fingerprint(), i.CodeHash, flow.Fingerprint(), true); e != nil {
		t.Fatal("fresh OIDC did not permit explicit consent", e)
	}
	wrong := binding
	wrong.Version++
	if _, e = store.ReauthenticateSession(ctx, domain.SecretHash(domain.SessionPrefix, cookie), cfg.Fingerprint(), wrong); !errors.Is(e, access.ErrUnauthenticated) {
		t.Fatal("different binding edition reauthenticated", e)
	}
}
