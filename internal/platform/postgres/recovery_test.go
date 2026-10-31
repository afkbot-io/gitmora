package postgres_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"workgraph.local/platform/internal/access"
	captureapp "workgraph.local/platform/internal/capture/application"
	identity "workgraph.local/platform/internal/identity/domain"
	"workgraph.local/platform/internal/platform/postgres"
	"workgraph.local/platform/internal/platform/secretbox"
	recovery "workgraph.local/platform/internal/recovery/application"
	"workgraph.local/platform/internal/recovery/domain"
)

func recoveryPlan(t *testing.T, pool *pgxpool.Pool, parent string) recovery.Prepared {
	t.Helper()
	schema, e := postgres.SchemaFingerprint()
	if e != nil {
		t.Fatal(e)
	}
	p, e := recovery.NewService(postgres.NewRecoveryStore(pool), time.Now).Prepare(domain.Provenance{BackupID: requestID(t), Schema: schema, ParentID: parent}, "Recovery operator", time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	return p
}

func TestRecoveryQuarantinesOldAuthorityAndExternalWritesWithoutErasingEvidence(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	credential, e := access.IssueCredential(subject, "Old key", []access.Permission{access.ReadCapture, access.WriteCapture}, time.Now(), 24*time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	creds := postgres.NewCredentialStore(pool)
	if e = creds.Save(ctx, credential.Record); e != nil {
		t.Fatal(e)
	}
	if _, e = captureapp.NewService(postgres.NewCaptureStore(pool)).Ingest(ctx, subject, snapshot()); e != nil {
		t.Fatal(e)
	}
	var canonical []byte
	if e = pool.QueryRow(ctx, `SELECT canonical FROM capture_revisions`).Scan(&canonical); e != nil {
		t.Fatal(e)
	}
	identities := postgres.NewIdentityStore(pool)
	cfg := oidcConfig()
	if e = identities.Activate(ctx, cfg.Fingerprint()); e != nil {
		t.Fatal(e)
	}
	browser, cookie := cliBrowser(t, pool, identities, subject, "restore-owner")
	intent, secrets := enrollmentInput(t)
	entry, e := identities.CreateCLIEnrollment(ctx, cfg.Fingerprint(), intent)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = identities.DecideCLIEnrollment(ctx, browser, cfg.Fingerprint(), intent.CodeHash, entry.Fingerprint(), true); e != nil {
		t.Fatal(e)
	}
	pending, _ := enrollmentInput(t)
	if _, e = identities.CreateCLIEnrollment(ctx, cfg.Fingerprint(), pending); e != nil {
		t.Fatal(e)
	}
	vault, e := secretbox.New("primary", map[string][]byte{"primary": bytes.Repeat([]byte{7}, 32)})
	if e != nil {
		t.Fatal(e)
	}
	envelope, e := vault.Seal("tasktopia:"+project, []byte("synthetic-provider-credential"))
	if e != nil {
		t.Fatal(e)
	}
	_, e = pool.Exec(ctx, `INSERT INTO work_connections(project_id,endpoint,country_id,secret_envelope,updated_by,auto_link_merge_requests)VALUES($1,'https://tasktopia.internal/mcp',$2,$3,$4,true)`, project, taskCountry, envelope, subject)
	if e != nil {
		t.Fatal(e)
	}
	forgeEnvelope, e := vault.Seal("gitlab:"+repository, []byte("synthetic-gitlab-credential"))
	if e != nil {
		t.Fatal(e)
	}
	_, e = pool.Exec(ctx, `INSERT INTO forge_connections(repository_id,webhook_mode,secret_envelope,updated_by,lease_owner,lease_until,code_lease_owner,code_lease_until)VALUES($1,'signature',$2,$3,gen_random_uuid(),now()+interval '1 hour',gen_random_uuid(),now()+interval '1 hour')`, repository, forgeEnvelope, subject)
	if e != nil {
		t.Fatal(e)
	}
	_, e = pool.Exec(ctx, `INSERT INTO work_jobs(id,project_id,country_id,task_id,kind,request_key,document,state,lease_owner,lease_until,read_started_at)VALUES(gen_random_uuid(),$1,$2,$3,'publication','pending','{}','pending',gen_random_uuid(),now()+interval '1 hour',now()),(gen_random_uuid(),$1,$2,$3,'publication','complete','{}','complete',NULL,NULL,NULL)`, project, taskCountry, taskUUID)
	if e != nil {
		t.Fatal(e)
	}
	p := recoveryPlan(t, pool, "")
	store := postgres.NewRecoveryStore(pool)
	r, e := store.Quarantine(ctx, p.Plan)
	if e != nil || r.ID != p.Plan.ID || r.TokenID != p.Plan.Key.ID || r.Counts.Memberships != 2 || r.Counts.TaskWrites != 1 {
		t.Fatal("recovery quarantine", r, e)
	}
	for _, raw := range []string{credential.Secret, secrets.Credential} {
		if _, e = creds.Authenticate(ctx, raw); !errors.Is(e, access.ErrUnauthenticated) {
			t.Fatal("restored key remains usable", e)
		}
	}
	if _, e = identities.Session(ctx, identity.SecretHash(identity.SessionPrefix, cookie), cfg.Fingerprint()); e == nil {
		t.Fatal("browser survived recovery")
	}
	if _, e = identities.Resolve(ctx, identity.Identity{Issuer: cfg.Issuer, Subject: "restore-owner"}); e == nil {
		t.Fatal("binding survived recovery")
	}
	operator, e := creds.Authenticate(ctx, p.Secret())
	if e != nil || !operator.Has(access.ManagePlatform) || operator.Has(access.ReadCapture) {
		t.Fatal("recovery metadata authority", e)
	}
	var canonicalAfter, retainedEnvelope []byte
	if e = pool.QueryRow(ctx, `SELECT canonical FROM capture_revisions`).Scan(&canonicalAfter); e != nil || !bytes.Equal(canonical, canonicalAfter) {
		t.Fatal("immutable capture changed", e)
	}
	if e = pool.QueryRow(ctx, `SELECT secret_envelope FROM work_connections`).Scan(&retainedEnvelope); e != nil || !bytes.Equal(retainedEnvelope, envelope) {
		t.Fatal("provider ciphertext lost", e)
	}
	if _, e = vault.Open("tasktopia:"+project, retainedEnvelope); e != nil {
		t.Fatal("retained keyring cannot decrypt provider", e)
	}
	if e = pool.QueryRow(ctx, `SELECT secret_envelope FROM forge_connections`).Scan(&retainedEnvelope); e != nil || !bytes.Equal(retainedEnvelope, forgeEnvelope) {
		t.Fatal("GitLab ciphertext changed", e)
	}
	if _, e = vault.Open("gitlab:"+repository, retainedEnvelope); e != nil {
		t.Fatal("retained GitLab keyring/scope cannot decrypt", e)
	}
	var complete int
	if e = pool.QueryRow(ctx, `SELECT count(*)FROM work_jobs WHERE state='complete'AND request_key='complete'`).Scan(&complete); e != nil || complete != 1 {
		t.Fatal("completed external write was altered", e)
	}
	metadata, e := postgres.ReadRecoveryMetadata(ctx, pool)
	if e != nil || metadata.ParentID != p.Plan.ID || metadata.Schema != p.Plan.Schema || metadata.PostgresVersion/10000 != 18 {
		t.Fatal("source metadata does not name committed authority epoch", e)
	}
	for label, sql := range map[string]string{
		"old users":   `SELECT count(*)FROM users WHERE enabled AND id<>'` + p.Plan.UserID + `'`,
		"old keys":    `SELECT count(*)FROM api_tokens WHERE revoked_at IS NULL AND id<>'` + p.Plan.Key.ID + `'`,
		"memberships": `SELECT count(*)FROM memberships`,
		"flows":       `SELECT count(*)FROM identity_cli_enrollments`,
		"forge":       `SELECT count(*)FROM forge_connections WHERE enabled OR NOT blocked OR lease_owner IS NOT NULL OR code_lease_owner IS NOT NULL`,
		"work":        `SELECT count(*)FROM work_connections WHERE enabled OR NOT blocked OR auto_link_merge_requests`,
		"writes":      `SELECT count(*)FROM work_jobs WHERE state='pending' OR lease_owner IS NOT NULL`,
	} {
		var n int
		if e = pool.QueryRow(ctx, sql).Scan(&n); e != nil || n != 0 {
			t.Fatal(label, n, e)
		}
	}
	if count(t, pool, "recovery_memberships") != 2 || count(t, pool, "recovery_operators") != 1 {
		t.Fatal("old authority metadata was erased")
	}
	ack, e := store.Quarantine(ctx, p.Plan)
	if e != nil || ack != r {
		t.Fatal("lost-ACK retry changed recovery", e)
	}
	changed := p.Plan
	changed.Name = "Another operator"
	if _, e = store.Quarantine(ctx, changed); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("recovery identity retargeted", e)
	}
	// Enabling an old account cannot revive its keys, inherited roles or OIDC binding.
	if _, e = pool.Exec(ctx, `UPDATE users SET enabled=true WHERE id=$1`, subject); e != nil {
		t.Fatal(e)
	}
	if _, e = creds.Authenticate(ctx, credential.Secret); !errors.Is(e, access.ErrUnauthenticated) {
		t.Fatal("old authority resurrected after account reenable", e)
	}
	if count(t, pool, "memberships") != 0 {
		t.Fatal("old roles resurrected")
	}
}

func TestRecoveryIsAtomicAndRejectsSchemaDriftOrAStaleAuthorityParent(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	store := postgres.NewRecoveryStore(pool)
	p := recoveryPlan(t, pool, "")
	if _, e := pool.Exec(ctx, `ALTER TABLE recovery_events ADD CONSTRAINT simulate_recovery_failure CHECK(false)NOT VALID`); e != nil {
		t.Fatal(e)
	}
	if _, e := store.Quarantine(ctx, p.Plan); e == nil {
		t.Fatal("quarantine succeeded without receipt")
	}
	var enabled int
	if e := pool.QueryRow(ctx, `SELECT count(*)FROM users WHERE enabled`).Scan(&enabled); e != nil || enabled != 2 || count(t, pool, "memberships") != 2 {
		t.Fatal("partial quarantine", e)
	}
	if _, e := pool.Exec(ctx, `ALTER TABLE recovery_events DROP CONSTRAINT simulate_recovery_failure`); e != nil {
		t.Fatal(e)
	}
	bad := p.Plan
	bad.Schema = strings.Repeat("f", 64)
	if _, e := store.Quarantine(ctx, bad); !errors.Is(e, domain.ErrSchema) {
		t.Fatal("wrong application schema accepted", e)
	}
	if _, e := pool.Exec(ctx, `UPDATE schema_migrations SET checksum=repeat('f',64)WHERE name='migrations/0001_capture.sql'`); e != nil {
		t.Fatal(e)
	}
	if _, e := store.Quarantine(ctx, p.Plan); !errors.Is(e, domain.ErrSchema) {
		t.Fatal("database drift accepted", e)
	}
}

func TestConcurrentRecoveryHasOneAuthorityEpochAndExactReplay(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	store := postgres.NewRecoveryStore(pool)
	a, b := recoveryPlan(t, pool, ""), recoveryPlan(t, pool, "")
	var x, y error
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, x = store.Quarantine(ctx, a.Plan) }()
	go func() { defer wg.Done(); _, y = store.Quarantine(ctx, b.Plan) }()
	wg.Wait()
	if (x == nil) == (y == nil) || x != nil && !errors.Is(x, domain.ErrConflict) || y != nil && !errors.Is(y, domain.ErrConflict) || count(t, pool, "recovery_events") != 1 {
		t.Fatal("parallel recovery changed authority twice", x, y)
	}
	if x != nil {
		a = b
	}
	next := recoveryPlan(t, pool, a.Plan.ID)
	if _, e := store.Quarantine(ctx, next.Plan); e != nil {
		t.Fatal("explicit next epoch failed", e)
	}
	if _, e := store.Quarantine(ctx, a.Plan); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("older committed recovery replay claimed current authority", e)
	}
	if _, e := postgres.NewCredentialStore(pool).Authenticate(ctx, a.Secret()); !errors.Is(e, access.ErrUnauthenticated) {
		t.Fatal("older recovery key survived next epoch", e)
	}
}
