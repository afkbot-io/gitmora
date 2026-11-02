package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"workgraph.local/platform/internal/access"
	directory "workgraph.local/platform/internal/directory/domain"
	"workgraph.local/platform/internal/identifier"
	"workgraph.local/platform/internal/identity/domain"
)

// IdentityStore persists hash-only browser proofs and explicit subject mappings; authorization stays local.
type IdentityStore struct{ pool *pgxpool.Pool }

func NewIdentityStore(pool *pgxpool.Pool) *IdentityStore { return &IdentityStore{pool} }

// Activate fences homogeneous deployment policy and revokes browser grants on changes, including disable/rollback.
func (s *IdentityStore) Activate(ctx context.Context, configuration string) error {
	return s.ActivatePolicy(ctx, configuration, false)
}

// ActivatePolicy serializes signup changes without revoking unchanged login/session authority.
func (s *IdentityStore) ActivatePolicy(ctx context.Context, configuration string, registration bool) error {
	if configuration != "" && len(configuration) != 64 || configuration == "" && registration {
		return domain.ErrInvalid
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if e = directoryLock(ctx, tx); e != nil {
		return e
	}
	var old string
	if e = tx.QueryRow(ctx, `SELECT configuration FROM identity_runtime WHERE id=1 FOR UPDATE`).Scan(&old); e != nil {
		return e
	}
	if old == configuration {
		if _, e = tx.Exec(ctx, `UPDATE identity_runtime SET registration_enabled=$1,updated_at=now()WHERE id=1`, registration); e != nil {
			return e
		}
		return tx.Commit(ctx)
	}
	_, e = tx.Exec(ctx, `WITH RECURSIVE revoked(id)AS(SELECT id FROM api_tokens WHERE auth_kind='browser' OR id IN(SELECT token_id FROM identity_cli_grants) UNION SELECT t.id FROM api_tokens t JOIN revoked r ON t.parent_id=r.id)UPDATE api_tokens SET revoked_at=COALESCE(revoked_at,now())WHERE id IN(SELECT id FROM revoked)`)
	if e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `UPDATE identity_sessions SET revoked_at=now()WHERE revoked_at IS NULL;DELETE FROM identity_login_flows;DELETE FROM identity_cli_enrollments`); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `UPDATE identity_runtime SET configuration=$1,registration_enabled=$2,updated_at=now()WHERE id=1`, configuration, registration); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func identityConfiguration(ctx context.Context, tx pgx.Tx, configuration string) error {
	var current string
	e := tx.QueryRow(ctx, `SELECT configuration FROM identity_runtime WHERE id=1 FOR SHARE`).Scan(&current)
	if e != nil {
		return e
	}
	if current == "" || current != configuration {
		return domain.ErrLogin
	}
	return nil
}

// CreateFlow stores no authorization code, nonce, verifier or raw browser credential.
func (s *IdentityStore) CreateFlow(ctx context.Context, f domain.Flow) error {
	if len(f.StateHash) != 64 || len(f.BrowserHash) != 64 || len(f.Configuration) != 64 {
		return domain.ErrInvalid
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(918274610250)`); e != nil {
		return e
	}
	if e = identityConfiguration(ctx, tx, f.Configuration); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `DELETE FROM identity_login_flows WHERE expires_at<=now()`); e != nil {
		return e
	}
	var n int
	if e = tx.QueryRow(ctx, `SELECT count(*)FROM identity_login_flows`).Scan(&n); e != nil {
		return e
	}
	if n >= 1000 {
		return domain.ErrCapacity
	}
	if _, e = tx.Exec(ctx, `INSERT INTO identity_login_flows(state_hash,browser_hash,configuration)VALUES($1,$2,$3)`, f.StateHash, f.BrowserHash, f.Configuration); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
func (s *IdentityStore) ConsumeFlow(ctx context.Context, f domain.Flow) error {
	tag, e := s.pool.Exec(ctx, `DELETE FROM identity_login_flows f USING identity_runtime r WHERE r.id=1 AND r.configuration=$3 AND f.state_hash=$1 AND f.browser_hash=$2 AND f.configuration=$3 AND f.expires_at>now()`, f.StateHash, f.BrowserHash, f.Configuration)
	if e != nil {
		return e
	}
	if tag.RowsAffected() != 1 {
		return domain.ErrLogin
	}
	return nil
}

const identityBindingColumns = `b.id::text,b.user_id::text,b.issuer,b.subject,b.enabled,b.version,b.created_at`

func scanIdentityBinding(row pgx.Row) (domain.Binding, error) {
	var b domain.Binding
	e := row.Scan(&b.ID, &b.UserID, &b.Issuer, &b.Subject, &b.Enabled, &b.Version, &b.CreatedAt)
	return b, e
}
func (s *IdentityStore) Resolve(ctx context.Context, i domain.Identity) (domain.Binding, error) {
	if !domain.Subject(i.Subject) {
		return domain.Binding{}, domain.ErrLogin
	}
	b, e := scanIdentityBinding(s.pool.QueryRow(ctx, `SELECT `+identityBindingColumns+` FROM identity_bindings b JOIN users u ON u.id=b.user_id AND u.enabled AND u.deleted_at IS NULL WHERE b.issuer=$1 AND b.subject=$2 AND b.enabled`, i.Issuer, i.Subject))
	if errors.Is(e, pgx.ErrNoRows) {
		return b, domain.ErrLogin
	}
	return b, e
}

// Bind refuses all identity retargeting, even when an older mapping is disabled.
func (s *IdentityStore) Bind(ctx context.Context, p access.Principal, issuer string, i domain.BindInput) (directory.Receipt, bool, error) {
	if i.Validate() != nil {
		return directory.Receipt{}, false, domain.ErrInvalid
	}
	if _, e := domain.URL(issuer); e != nil {
		return directory.Receipt{}, false, e
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return directory.Receipt{}, false, e
	}
	defer tx.Rollback(ctx)
	if e = directoryLock(ctx, tx); e != nil {
		return directory.Receipt{}, false, e
	}
	a, e := directoryAuthenticate(ctx, tx, p)
	if e != nil {
		return directory.Receipt{}, false, e
	}
	if !a.principal.Has(access.ReadDirectory) || !a.principal.Has(access.ManagePlatform) {
		return directory.Receipt{}, false, access.ErrDenied
	}
	raw, _ := json.Marshal(struct {
		Issuer string
		Input  domain.BindInput
	}{issuer, i})
	hash := directoryFingerprint(raw)
	previous, found, e := directoryExisting(ctx, tx, p.UserID, i.RequestID, hash)
	if e != nil {
		return directory.Receipt{}, false, e
	}
	if found {
		return previous, false, tx.Commit(ctx)
	}
	if !a.platform() {
		return directory.Receipt{}, false, access.ErrDenied
	}
	var enabled bool
	if e = tx.QueryRow(ctx, `SELECT enabled FROM users WHERE id=$1 AND deleted_at IS NULL FOR SHARE`, i.UserID).Scan(&enabled); e != nil || !enabled {
		if e != nil && !errors.Is(e, pgx.ErrNoRows) {
			return directory.Receipt{}, false, e
		}
		return directory.Receipt{}, false, domain.ErrConflict
	}
	var version int64
	e = tx.QueryRow(ctx, `INSERT INTO identity_bindings(id,user_id,issuer,subject)VALUES($1,$2,$3,$4)RETURNING version`, i.ID, i.UserID, issuer, i.Subject).Scan(&version)
	if e != nil {
		return directory.Receipt{}, false, identityWriteError(e)
	}
	r := directory.Receipt{RequestID: i.RequestID, Action: "bind_identity", Kind: "identity", EntityID: i.ID, ActorID: p.UserID, Version: version}
	r, e = directorySaveReceipt(ctx, tx, r, p.TokenID, "api", hash)
	if e != nil {
		return r, false, e
	}
	return r, true, tx.Commit(ctx)
}
func identityWriteError(e error) error {
	if errors.Is(directoryError(e), directory.ErrConflict) {
		return domain.ErrConflict
	}
	return e
}
func (s *IdentityStore) Bindings(ctx context.Context, p access.Principal, issuer string, c directory.Cursor) (directory.Page[domain.Binding], error) {
	c, e := c.Normalize()
	if e != nil {
		return directory.Page[domain.Binding]{}, domain.ErrInvalid
	}
	return directoryRead(ctx, s.pool, p, true, func(tx pgx.Tx, a directoryActor) (directory.Page[domain.Binding], error) {
		if !a.platform() {
			return directory.Page[domain.Binding]{}, access.ErrDenied
		}
		rows, e := tx.Query(ctx, `SELECT `+identityBindingColumns+` FROM identity_bindings b WHERE b.issuer=$1 AND($2='' OR b.id>NULLIF($2,'')::uuid)ORDER BY b.id LIMIT $3`, issuer, c.AfterID, c.Limit+1)
		if e != nil {
			return directory.Page[domain.Binding]{}, e
		}
		defer rows.Close()
		items := []domain.Binding{}
		for rows.Next() {
			v, e := scanIdentityBinding(rows)
			if e != nil {
				return directory.Page[domain.Binding]{}, e
			}
			items = append(items, v)
		}
		return directoryPage(items, c.Limit, func(b domain.Binding) string { return b.ID }), rows.Err()
	})
}
func (s *IdentityStore) ChangeBinding(ctx context.Context, p access.Principal, issuer, id string, c domain.BindingChange) (directory.Receipt, bool, error) {
	if !identifier.Valid(id) || !identifier.Valid(c.RequestID) || c.ExpectedVersion < 1 || c.ExpectedVersion > directory.MaxVersion || c.Enabled == nil {
		return directory.Receipt{}, false, domain.ErrInvalid
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return directory.Receipt{}, false, e
	}
	defer tx.Rollback(ctx)
	if e = directoryLock(ctx, tx); e != nil {
		return directory.Receipt{}, false, e
	}
	a, e := directoryAuthenticate(ctx, tx, p)
	if e != nil {
		return directory.Receipt{}, false, e
	}
	if !a.principal.Has(access.ReadDirectory) || !a.principal.Has(access.ManagePlatform) {
		return directory.Receipt{}, false, access.ErrDenied
	}
	raw, _ := json.Marshal(struct {
		Issuer, ID string
		Input      domain.BindingChange
	}{issuer, id, c})
	hash := directoryFingerprint(raw)
	previous, found, e := directoryExisting(ctx, tx, p.UserID, c.RequestID, hash)
	if e != nil {
		return directory.Receipt{}, false, e
	}
	if found {
		return previous, false, tx.Commit(ctx)
	}
	if !a.platform() {
		return directory.Receipt{}, false, access.ErrDenied
	}
	b, e := scanIdentityBinding(tx.QueryRow(ctx, `SELECT `+identityBindingColumns+` FROM identity_bindings b WHERE b.id=$1 AND b.issuer=$2 FOR UPDATE`, id, issuer))
	if errors.Is(e, pgx.ErrNoRows) {
		return directory.Receipt{}, false, domain.ErrConflict
	}
	if e != nil {
		return directory.Receipt{}, false, e
	}
	if directory.MatchVersion(c.ExpectedVersion, b.Version) != nil {
		return directory.Receipt{}, false, domain.ErrConflict
	}
	if e = closeIdentityBinding(ctx, tx, id); e != nil {
		return directory.Receipt{}, false, e
	}
	var version int64
	if e = tx.QueryRow(ctx, `UPDATE identity_bindings SET enabled=$2,version=nextval('directory_revision')WHERE id=$1 RETURNING version`, id, *c.Enabled).Scan(&version); e != nil {
		return directory.Receipt{}, false, e
	}
	action := directory.Action("disable_identity")
	if *c.Enabled {
		action = "enable_identity"
	}
	r := directory.Receipt{RequestID: c.RequestID, Action: action, Kind: "identity", EntityID: id, ActorID: p.UserID, Version: version}
	r, e = directorySaveReceipt(ctx, tx, r, p.TokenID, "api", hash)
	if e != nil {
		return r, false, e
	}
	return r, true, tx.Commit(ctx)
}
func closeIdentityBinding(ctx context.Context, tx pgx.Tx, id string) error {
	_, e := tx.Exec(ctx, `WITH RECURSIVE roots(id)AS((SELECT token_id FROM identity_sessions WHERE binding_id=$1 UNION SELECT token_id FROM identity_cli_grants WHERE binding_id=$1) UNION SELECT t.id FROM api_tokens t JOIN roots r ON t.parent_id=r.id)UPDATE api_tokens SET revoked_at=COALESCE(revoked_at,now())WHERE id IN(SELECT id FROM roots)`, id)
	if e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `UPDATE identity_sessions SET revoked_at=COALESCE(revoked_at,now())WHERE binding_id=$1`, id)
	return e
}

// OpenSession checks the exact mapping edition/current deployment immediately before committing its credential.
// Its returned expiry uses the database's stored precision, matching subsequent session reads.
func (s *IdentityStore) OpenSession(ctx context.Context, seed domain.SessionSeed) (domain.Session, error) {
	if !identifier.Valid(seed.ID) || len(seed.CookieHash) != 64 || len(seed.CSRFHash) != 64 || seed.Credential.UserID != seed.Binding.UserID || seed.Credential.CreatedAt.Before(time.Now().Add(-time.Minute)) || seed.Credential.ExpiresAt.After(time.Now().Add(8*time.Hour+time.Minute)) {
		return domain.Session{}, domain.ErrInvalid
	}
	caps := domain.InteractiveScopes()
	slices.Sort(caps)
	if !slices.Equal(caps, seed.Credential.Scopes) {
		return domain.Session{}, domain.ErrInvalid
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return domain.Session{}, e
	}
	defer tx.Rollback(ctx)
	if e = directoryLock(ctx, tx); e != nil {
		return domain.Session{}, e
	}
	if e = identityConfiguration(ctx, tx, seed.Configuration); e != nil {
		return domain.Session{}, e
	}
	b, e := scanIdentityBinding(tx.QueryRow(ctx, `SELECT `+identityBindingColumns+` FROM identity_bindings b JOIN users u ON u.id=b.user_id AND u.enabled AND u.deleted_at IS NULL WHERE b.id=$1 AND b.enabled FOR SHARE OF b,u`, seed.Binding.ID))
	if errors.Is(e, pgx.ErrNoRows) {
		return domain.Session{}, domain.ErrLogin
	}
	if e != nil {
		return domain.Session{}, e
	}
	if b != seed.Binding {
		return domain.Session{}, domain.ErrLogin
	}
	if username, err := directory.Username(seed.Username); err == nil {
		if _, e = tx.Exec(ctx, `INSERT INTO user_handles(user_id,username)VALUES($1,$2)ON CONFLICT DO NOTHING`, b.UserID, username); e != nil {
			return domain.Session{}, e
		}
	}
	if seed.OldCookieHash != "" {
		var old domain.Session
		e = tx.QueryRow(ctx, `SELECT id::text,user_id::text,token_id::text FROM identity_sessions WHERE cookie_hash=$1`, seed.OldCookieHash).Scan(&old.ID, &old.Principal.UserID, &old.Principal.TokenID)
		if e == nil {
			if e = revokeCredentialTree(ctx, tx, old.Principal.UserID, old.Principal.TokenID); e != nil {
				return domain.Session{}, e
			}
			if _, e = tx.Exec(ctx, `UPDATE identity_sessions SET revoked_at=COALESCE(revoked_at,now())WHERE id=$1`, old.ID); e != nil {
				return domain.Session{}, e
			}
		} else if !errors.Is(e, pgx.ErrNoRows) {
			return domain.Session{}, e
		}
	}
	// A fresh verified login can evict the oldest browser grants instead of accumulating unbounded live sessions.
	rows, e := tx.Query(ctx, `SELECT s.id::text,s.token_id::text FROM identity_sessions s JOIN api_tokens t ON t.id=s.token_id WHERE s.user_id=$1 AND s.revoked_at IS NULL AND s.expires_at>now() AND t.revoked_at IS NULL ORDER BY s.created_at DESC,s.id DESC OFFSET 9`, b.UserID)
	if e != nil {
		return domain.Session{}, e
	}
	type oldSession struct{ id, token string }
	old := []oldSession{}
	for rows.Next() {
		var v oldSession
		if e = rows.Scan(&v.id, &v.token); e != nil {
			rows.Close()
			return domain.Session{}, e
		}
		old = append(old, v)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return domain.Session{}, e
	}
	for _, v := range old {
		if e = revokeCredentialTree(ctx, tx, b.UserID, v.token); e != nil {
			return domain.Session{}, e
		}
		if _, e = tx.Exec(ctx, `UPDATE identity_sessions SET revoked_at=now()WHERE id=$1`, v.id); e != nil {
			return domain.Session{}, e
		}
	}
	if e = saveCredential(ctx, tx, seed.Credential); e != nil {
		return domain.Session{}, e
	}
	if _, e = tx.Exec(ctx, `UPDATE api_tokens SET auth_kind='browser'WHERE id=$1`, seed.Credential.ID); e != nil {
		return domain.Session{}, e
	}
	var expires time.Time
	e = tx.QueryRow(ctx, `INSERT INTO identity_sessions(id,user_id,binding_id,binding_version,token_id,cookie_hash,csrf_hash,configuration,expires_at)VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING expires_at`, seed.ID, b.UserID, b.ID, b.Version, seed.Credential.ID, seed.CookieHash, seed.CSRFHash, seed.Configuration, seed.Credential.ExpiresAt).Scan(&expires)
	if e != nil {
		return domain.Session{}, e
	}
	request, e := identifier.New()
	if e != nil {
		return domain.Session{}, e
	}
	r := directory.Receipt{RequestID: request, Action: "browser_login", Kind: "session", EntityID: seed.ID, ActorID: b.UserID}
	r.Version, e = directoryNextVersion(ctx, tx)
	if e != nil {
		return domain.Session{}, e
	}
	if _, e = directorySaveReceipt(ctx, tx, r, seed.Credential.ID, "oidc", directoryFingerprint([]byte("login:"+seed.ID))); e != nil {
		return domain.Session{}, e
	}
	session := domain.Session{ID: seed.ID, BindingID: b.ID, Principal: access.Principal{UserID: b.UserID, TokenID: seed.Credential.ID, Scopes: seed.Credential.Scopes}, CSRFHash: seed.CSRFHash, ExpiresAt: expires}
	return session, tx.Commit(ctx)
}

// Session is checked on every cookie request; no cache can outlive local disable/revocation or configuration changes.
func (s *IdentityStore) Session(ctx context.Context, cookie, configuration string) (domain.Session, error) {
	var out domain.Session
	var caps []string
	e := s.pool.QueryRow(ctx, `SELECT s.id::text,s.binding_id::text,s.user_id::text,s.token_id::text,t.scopes,s.csrf_hash,s.expires_at FROM identity_sessions s JOIN identity_runtime r ON r.id=1 AND r.configuration=s.configuration JOIN api_tokens t ON t.id=s.token_id AND t.user_id=s.user_id AND t.auth_kind='browser' AND t.revoked_at IS NULL AND t.expires_at>now() JOIN identity_bindings b ON b.id=s.binding_id AND b.user_id=s.user_id AND b.enabled AND b.version=s.binding_version JOIN users u ON u.id=s.user_id AND u.enabled AND u.deleted_at IS NULL WHERE s.cookie_hash=$1 AND s.configuration=$2 AND s.revoked_at IS NULL AND s.expires_at>now()`, cookie, configuration).Scan(&out.ID, &out.BindingID, &out.Principal.UserID, &out.Principal.TokenID, &caps, &out.CSRFHash, &out.ExpiresAt)
	if errors.Is(e, pgx.ErrNoRows) {
		return out, access.ErrUnauthenticated
	}
	if e != nil {
		return out, e
	}
	for _, cap := range caps {
		out.Principal.Scopes = append(out.Principal.Scopes, access.Permission(cap))
	}
	return out, nil
}
func (s *IdentityStore) Logout(ctx context.Context, cookie, configuration string) error {
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if e = directoryLock(ctx, tx); e != nil {
		return e
	}
	var id, user, token string
	e = tx.QueryRow(ctx, `SELECT id::text,user_id::text,token_id::text FROM identity_sessions WHERE cookie_hash=$1 AND configuration=$2 FOR UPDATE`, cookie, configuration).Scan(&id, &user, &token)
	if errors.Is(e, pgx.ErrNoRows) {
		return access.ErrUnauthenticated
	}
	if e != nil {
		return e
	}
	if e = revokeCredentialTree(ctx, tx, user, token); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `UPDATE identity_sessions SET revoked_at=COALESCE(revoked_at,now())WHERE id=$1`, id); e != nil {
		return e
	}
	request, e := identifier.New()
	if e != nil {
		return e
	}
	r := directory.Receipt{RequestID: request, Action: "browser_logout", Kind: "session", EntityID: id, ActorID: user}
	r.Version, e = directoryNextVersion(ctx, tx)
	if e != nil {
		return e
	}
	if _, e = directorySaveReceipt(ctx, tx, r, token, "api", directoryFingerprint([]byte("logout:"+request))); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
