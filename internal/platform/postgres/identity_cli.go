package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"workgraph.local/platform/internal/access"
	directory "workgraph.local/platform/internal/directory/domain"
	"workgraph.local/platform/internal/identifier"
	"workgraph.local/platform/internal/identity/domain"
)

const cliColumns = `e.id::text,e.device_hash,e.proof_hash,e.credential_hash,e.code_hash,e.label,e.scopes,e.ttl_seconds,e.configuration,e.created_at,e.expires_at,e.status,COALESCE(e.decided_by::text,''),COALESCE(e.token_id::text,''),e.credential_expires_at`

func scanCLI(row pgx.Row) (domain.CLIEnrollment, error) {
	var v domain.CLIEnrollment
	var scopes []string
	e := row.Scan(&v.Request.ID, &v.Request.DeviceHash, &v.Request.ProofHash, &v.Request.CredentialHash, &v.Request.CodeHash, &v.Request.Label, &scopes, &v.Request.TTLSeconds, &v.Configuration, &v.CreatedAt, &v.ExpiresAt, &v.Status, &v.UserID, &v.TokenID, &v.CredentialExpiresAt)
	for _, s := range scopes {
		v.Request.Scopes = append(v.Request.Scopes, access.Permission(s))
	}
	return v, e
}

// CreateCLIEnrollment admits bounded hash-only commitments; live replay never changes their deadline or input.
func (s *IdentityStore) CreateCLIEnrollment(ctx context.Context, configuration string, input domain.CLIRequest) (domain.CLIEnrollment, error) {
	i, e := input.Normalize()
	if e != nil {
		return domain.CLIEnrollment{}, e
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return domain.CLIEnrollment{}, e
	}
	defer tx.Rollback(ctx)
	if e = directoryLock(ctx, tx); e != nil {
		return domain.CLIEnrollment{}, e
	}
	if e = identityConfiguration(ctx, tx, configuration); e != nil {
		return domain.CLIEnrollment{}, e
	}
	// A flow is ephemeral; permanent accepted credentials/audit remain independently retained.
	if _, e = tx.Exec(ctx, `DELETE FROM identity_cli_enrollments WHERE expires_at<=now()`); e != nil {
		return domain.CLIEnrollment{}, e
	}
	previous, e := scanCLI(tx.QueryRow(ctx, `SELECT `+cliColumns+` FROM identity_cli_enrollments e WHERE e.id=$1 FOR UPDATE`, i.ID))
	if e == nil {
		a, _ := json.Marshal(previous.Request)
		b, _ := json.Marshal(i)
		if string(a) != string(b) || previous.Configuration != configuration {
			return domain.CLIEnrollment{}, domain.ErrConflict
		}
		return previous, tx.Commit(ctx)
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return domain.CLIEnrollment{}, e
	}
	var n int
	var exists bool
	if e = tx.QueryRow(ctx, `SELECT count(*) FROM identity_cli_enrollments`).Scan(&n); e != nil {
		return domain.CLIEnrollment{}, e
	}
	if n >= 1000 {
		return domain.CLIEnrollment{}, domain.ErrCapacity
	}
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM api_tokens WHERE secret_hash=$1)`, i.CredentialHash).Scan(&exists); e != nil {
		return domain.CLIEnrollment{}, e
	}
	if exists {
		return domain.CLIEnrollment{}, domain.ErrConflict
	}
	caps := make([]string, len(i.Scopes))
	for j, p := range i.Scopes {
		caps[j] = string(p)
	}
	if _, e = tx.Exec(ctx, `INSERT INTO identity_cli_enrollments(id,device_hash,proof_hash,credential_hash,code_hash,label,scopes,ttl_seconds,configuration)VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, i.ID, i.DeviceHash, i.ProofHash, i.CredentialHash, i.CodeHash, i.Label, caps, i.TTLSeconds, configuration); e != nil {
		return domain.CLIEnrollment{}, identityWriteError(e)
	}
	v, e := scanCLI(tx.QueryRow(ctx, `SELECT `+cliColumns+` FROM identity_cli_enrollments e WHERE e.id=$1`, i.ID))
	if e != nil {
		return v, e
	}
	return v, tx.Commit(ctx)
}

type cliBrowser struct {
	actor   directoryActor
	binding string
	version int64
}

// ReauthenticateSession records freshly verified OIDC identity without rotating or extending same-account browser authority.
func (s *IdentityStore) ReauthenticateSession(ctx context.Context, cookie, configuration string, expected domain.Binding) (domain.Session, error) {
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return domain.Session{}, e
	}
	defer tx.Rollback(ctx)
	if e = directoryLock(ctx, tx); e != nil {
		return domain.Session{}, e
	}
	if e = identityConfiguration(ctx, tx, configuration); e != nil {
		return domain.Session{}, e
	}
	b, e := scanIdentityBinding(tx.QueryRow(ctx, `SELECT `+identityBindingColumns+` FROM identity_bindings b JOIN users u ON u.id=b.user_id AND u.enabled AND u.deleted_at IS NULL WHERE b.id=$1 AND b.enabled FOR SHARE OF b,u`, expected.ID))
	if errors.Is(e, pgx.ErrNoRows) || e == nil && b != expected {
		return domain.Session{}, access.ErrUnauthenticated
	}
	if e != nil {
		return domain.Session{}, e
	}
	var out domain.Session
	var caps []string
	e = tx.QueryRow(ctx, `SELECT s.id::text,s.binding_id::text,s.user_id::text,s.token_id::text,t.scopes,s.csrf_hash,s.expires_at FROM identity_sessions s JOIN api_tokens t ON t.id=s.token_id AND t.user_id=s.user_id AND t.auth_kind='browser' AND t.revoked_at IS NULL AND t.expires_at>now() WHERE s.cookie_hash=$1 AND s.configuration=$2 AND s.user_id=$3 AND s.binding_id=$4 AND s.binding_version=$5 AND s.revoked_at IS NULL AND s.expires_at>now() FOR SHARE OF s,t`, cookie, configuration, b.UserID, b.ID, b.Version).Scan(&out.ID, &out.BindingID, &out.Principal.UserID, &out.Principal.TokenID, &caps, &out.CSRFHash, &out.ExpiresAt)
	if errors.Is(e, pgx.ErrNoRows) {
		return domain.Session{}, access.ErrUnauthenticated
	}
	if e != nil {
		return out, e
	}
	for _, p := range caps {
		out.Principal.Scopes = append(out.Principal.Scopes, access.Permission(p))
	}
	if _, e = tx.Exec(ctx, `UPDATE identity_sessions SET cli_verified_at=now() WHERE id=$1`, out.ID); e != nil {
		return domain.Session{}, e
	}
	request, e := identifier.New()
	if e != nil {
		return domain.Session{}, e
	}
	r := directory.Receipt{RequestID: request, Action: "cli_reauthenticated", Kind: "session", EntityID: out.ID, ActorID: b.UserID}
	r.Version, e = directoryNextVersion(ctx, tx)
	if e != nil {
		return domain.Session{}, e
	}
	if _, e = directorySaveReceipt(ctx, tx, r, out.Principal.TokenID, "oidc", directoryFingerprint([]byte("cli_reauth:"+request))); e != nil {
		return domain.Session{}, e
	}
	return out, tx.Commit(ctx)
}

func currentCLIBrowser(ctx context.Context, tx pgx.Tx, session domain.Session, configuration string, fresh bool) (cliBrowser, error) {
	if e := identityConfiguration(ctx, tx, configuration); e != nil {
		return cliBrowser{}, e
	}
	a, e := directoryAuthenticate(ctx, tx, session.Principal)
	if e != nil {
		return cliBrowser{}, e
	}
	var v cliBrowser
	var recent bool
	e = tx.QueryRow(ctx, `SELECT s.binding_id::text,s.binding_version,COALESCE(s.cli_verified_at,s.created_at)<=now() AND COALESCE(s.cli_verified_at,s.created_at)>now()-make_interval(secs => $6)
 FROM identity_sessions s JOIN api_tokens t ON t.id=s.token_id AND t.auth_kind='browser'
 JOIN identity_bindings b ON b.id=s.binding_id AND b.user_id=s.user_id AND b.enabled AND b.version=s.binding_version
 WHERE s.id=$1 AND s.user_id=$2 AND s.token_id=$3 AND s.binding_id=$4 AND s.configuration=$5 AND s.revoked_at IS NULL AND s.expires_at>now() FOR SHARE OF s,b`, session.ID, session.Principal.UserID, session.Principal.TokenID, session.BindingID, configuration, domain.CLIFreshSession.Seconds()).Scan(&v.binding, &v.version, &recent)
	if errors.Is(e, pgx.ErrNoRows) {
		return v, access.ErrDenied
	}
	if e != nil {
		return v, e
	}
	if fresh && !recent {
		return v, domain.ErrFreshLogin
	}
	v.actor = a
	return v, nil
}

func cliForBrowser(ctx context.Context, tx pgx.Tx, code, configuration, user string) (domain.CLIEnrollment, error) {
	v, e := scanCLI(tx.QueryRow(ctx, `SELECT `+cliColumns+` FROM identity_cli_enrollments e WHERE e.code_hash=$1 AND e.configuration=$2 AND e.expires_at>now() FOR UPDATE`, code, configuration))
	if errors.Is(e, pgx.ErrNoRows) {
		return v, domain.ErrLogin
	}
	if e != nil {
		return v, e
	}
	if v.UserID != "" && v.UserID != user {
		return domain.CLIEnrollment{}, domain.ErrConflict
	}
	return v, nil
}

// InspectCLIEnrollment rechecks an actual current browser credential; API tokens cannot impersonate it.
func (s *IdentityStore) InspectCLIEnrollment(ctx context.Context, session domain.Session, configuration, code string) (domain.CLIEnrollment, error) {
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return domain.CLIEnrollment{}, e
	}
	defer tx.Rollback(ctx)
	if e = directoryLock(ctx, tx); e != nil {
		return domain.CLIEnrollment{}, e
	}
	if _, e = currentCLIBrowser(ctx, tx, session, configuration, false); e != nil {
		return domain.CLIEnrollment{}, e
	}
	v, e := cliForBrowser(ctx, tx, code, configuration, session.Principal.UserID)
	if e != nil {
		return v, e
	}
	if e = cliActive(ctx, tx, configuration, v); e != nil {
		return domain.CLIEnrollment{}, e
	}
	return v, tx.Commit(ctx)
}

// DecideCLIEnrollment atomically pins consent, current browser scopes and an independently revocable OIDC-bound root.
func (s *IdentityStore) DecideCLIEnrollment(ctx context.Context, session domain.Session, configuration, code, fingerprint string, approve bool) (domain.CLIEnrollment, error) {
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return domain.CLIEnrollment{}, e
	}
	defer tx.Rollback(ctx)
	if e = directoryLock(ctx, tx); e != nil {
		return domain.CLIEnrollment{}, e
	}
	browser, e := currentCLIBrowser(ctx, tx, session, configuration, approve)
	if e != nil {
		return domain.CLIEnrollment{}, e
	}
	v, e := cliForBrowser(ctx, tx, code, configuration, session.Principal.UserID)
	if e != nil {
		return v, e
	}
	if v.Fingerprint() != fingerprint {
		return domain.CLIEnrollment{}, domain.ErrConflict
	}
	wanted := "denied"
	if approve {
		wanted = "approved"
	}
	if v.Status != "pending" {
		if v.Status != wanted {
			return domain.CLIEnrollment{}, domain.ErrConflict
		}
		if e = cliActive(ctx, tx, configuration, v); e != nil {
			return domain.CLIEnrollment{}, e
		}
		return v, tx.Commit(ctx)
	}
	if approve {
		for _, p := range v.Request.Scopes {
			if !browser.actor.principal.Has(p) {
				return domain.CLIEnrollment{}, access.ErrDenied
			}
		}
	}
	var now time.Time
	if e = tx.QueryRow(ctx, `SELECT now()`).Scan(&now); e != nil {
		return domain.CLIEnrollment{}, e
	}
	var token any
	var expiry any
	entity, kind, action := v.Request.ID, "cli_enrollment", "cli_denied"
	if approve {
		var live int
		if e = tx.QueryRow(ctx, `SELECT count(*)FROM api_tokens WHERE user_id=$1 AND revoked_at IS NULL AND expires_at>now()`, session.Principal.UserID).Scan(&live); e != nil {
			return domain.CLIEnrollment{}, e
		}
		if live >= 100 {
			return domain.CLIEnrollment{}, domain.ErrCapacity
		}
		id, e := identifier.New()
		if e != nil {
			return domain.CLIEnrollment{}, e
		}
		expires := now.Add(time.Duration(v.Request.TTLSeconds) * time.Second)
		record := access.CredentialRecord{ID: id, UserID: session.Principal.UserID, Label: v.Request.Label, Hash: v.Request.CredentialHash, Scopes: slices.Clone(v.Request.Scopes), CreatedAt: now, ExpiresAt: expires}
		if e = saveCredential(ctx, tx, record); e != nil {
			return domain.CLIEnrollment{}, identityWriteError(e)
		}
		if _, e = tx.Exec(ctx, `INSERT INTO identity_cli_grants(token_id,binding_id,binding_version,configuration,policy)VALUES($1,$2,$3,$4,$5)`, id, browser.binding, browser.version, configuration, domain.CLIPolicy); e != nil {
			return domain.CLIEnrollment{}, e
		}
		token, expiry = id, expires
		entity, kind, action = id, "token", "cli_approved"
	}
	if _, e = tx.Exec(ctx, `UPDATE identity_cli_enrollments SET status=$2,decided_by=$3,decided_at=$4,token_id=$5,credential_expires_at=$6 WHERE id=$1`, v.Request.ID, wanted, session.Principal.UserID, now, token, expiry); e != nil {
		return domain.CLIEnrollment{}, e
	}
	request, e := identifier.New()
	if e != nil {
		return domain.CLIEnrollment{}, e
	}
	r := directory.Receipt{RequestID: request, Action: directory.Action(action), Kind: kind, EntityID: entity, ActorID: session.Principal.UserID}
	r.Version, e = directoryNextVersion(ctx, tx)
	if e != nil {
		return domain.CLIEnrollment{}, e
	}
	if _, e = directorySaveReceipt(ctx, tx, r, session.Principal.TokenID, "oidc", directoryFingerprint([]byte(action+":"+v.Request.ID+":"+fingerprint))); e != nil {
		return domain.CLIEnrollment{}, e
	}
	v, e = scanCLI(tx.QueryRow(ctx, `SELECT `+cliColumns+` FROM identity_cli_enrollments e WHERE e.id=$1`, v.Request.ID))
	if e != nil {
		return v, e
	}
	return v, tx.Commit(ctx)
}

// PollCLIEnrollment discloses only proof-owned metadata and never reports a revoked/expired root as active.
func (s *IdentityStore) PollCLIEnrollment(ctx context.Context, configuration, id, device, proof string) (domain.CLIEnrollment, error) {
	if !identifier.Valid(id) {
		return domain.CLIEnrollment{}, domain.ErrInvalid
	}
	v, e := scanCLI(s.pool.QueryRow(ctx, `SELECT `+cliColumns+` FROM identity_cli_enrollments e JOIN identity_runtime r ON r.id=1 AND r.configuration=e.configuration WHERE e.id=$1 AND e.configuration=$2 AND e.device_hash=$3 AND e.proof_hash=$4 AND e.expires_at>now()`, id, configuration, device, proof))
	if errors.Is(e, pgx.ErrNoRows) {
		return v, domain.ErrLogin
	}
	if e != nil {
		return v, e
	}
	if e = cliActive(ctx, s.pool, configuration, v); e != nil {
		return domain.CLIEnrollment{}, e
	}
	return v, nil
}

type cliQuery interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

// CancelCLIEnrollment terminally closes a proof-owned pending flow, serialized with browser approval.
// Already approved keys require their ordinary authenticated revocation; polling proofs cannot revoke them.
func (s *IdentityStore) CancelCLIEnrollment(ctx context.Context, configuration, id, device, proof string) (domain.CLIEnrollment, error) {
	if !identifier.Valid(id) {
		return domain.CLIEnrollment{}, domain.ErrInvalid
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return domain.CLIEnrollment{}, e
	}
	defer tx.Rollback(ctx)
	if e = directoryLock(ctx, tx); e != nil {
		return domain.CLIEnrollment{}, e
	}
	if e = identityConfiguration(ctx, tx, configuration); e != nil {
		return domain.CLIEnrollment{}, e
	}
	v, e := scanCLI(tx.QueryRow(ctx, `SELECT `+cliColumns+` FROM identity_cli_enrollments e WHERE e.id=$1 AND e.configuration=$2 AND e.device_hash=$3 AND e.proof_hash=$4 FOR UPDATE`, id, configuration, device, proof))
	if errors.Is(e, pgx.ErrNoRows) {
		return domain.CLIEnrollment{}, domain.ErrLogin
	}
	if e != nil {
		return v, e
	}
	if v.Status == "approved" {
		return domain.CLIEnrollment{}, domain.ErrConflict
	}
	if v.Status == "pending" {
		if _, e = tx.Exec(ctx, `UPDATE identity_cli_enrollments SET status='cancelled',decided_at=now() WHERE id=$1`, id); e != nil {
			return domain.CLIEnrollment{}, e
		}
		v.Status = "cancelled"
	}
	return v, tx.Commit(ctx)
}

func cliActive(ctx context.Context, q cliQuery, configuration string, v domain.CLIEnrollment) error {
	if v.Status != "approved" {
		return nil
	}
	var live bool
	caps := make([]string, len(v.Request.Scopes))
	for i, p := range v.Request.Scopes {
		caps[i] = string(p)
	}
	e := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM identity_cli_grants g JOIN api_tokens t ON t.id=g.token_id AND t.auth_kind='api' AND t.parent_id IS NULL AND t.revoked_at IS NULL AND t.expires_at>now() JOIN users u ON u.id=t.user_id AND u.enabled AND u.deleted_at IS NULL JOIN identity_bindings b ON b.id=g.binding_id AND b.user_id=u.id AND b.enabled AND b.version=g.binding_version JOIN identity_runtime r ON r.id=1 AND r.configuration=g.configuration WHERE g.token_id=$1 AND g.configuration=$2 AND g.policy=$3 AND t.user_id=$4 AND t.secret_hash=$5 AND t.expires_at=$6 AND t.scopes=$7::text[] AND t.label=$8)`, v.TokenID, configuration, domain.CLIPolicy, v.UserID, v.Request.CredentialHash, v.CredentialExpiresAt, caps, v.Request.Label).Scan(&live)
	if e != nil {
		return e
	}
	if !live {
		return domain.ErrLogin
	}
	return nil
}
