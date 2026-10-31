package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"workgraph.local/platform/internal/access"
)

// CredentialStore stores digests and authenticates against current user/token state.
type CredentialStore struct{ pool *pgxpool.Pool }

// NewCredentialStore constructs the scoped credential persistence adapter.
func NewCredentialStore(pool *pgxpool.Pool) *CredentialStore { return &CredentialStore{pool: pool} }

// Save persists an issued credential. This privileged storage operation is only for trusted
// issuance use cases; callers must enforce issuance authority before invoking it.
func (s *CredentialStore) Save(ctx context.Context, record access.CredentialRecord) error {
	return saveCredential(ctx, s.pool, record)
}

type credentialWriter interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func saveCredential(ctx context.Context, writer credentialWriter, record access.CredentialRecord) error {
	scopes := make([]string, len(record.Scopes))
	for i, scope := range record.Scopes {
		scopes[i] = string(scope)
	}
	_, err := writer.Exec(ctx, `INSERT INTO api_tokens(id,user_id,label,secret_hash,scopes,created_at,expires_at)
		VALUES($1,$2,$3,$4,$5,$6,$7)`, record.ID, record.UserID, record.Label, record.Hash, scopes, record.CreatedAt, record.ExpiresAt)
	return err
}

// Authenticate rejects malformed, expired, revoked or disabled identities on every request.
func (s *CredentialStore) Authenticate(ctx context.Context, raw string) (access.Principal, error) {
	hash := access.CredentialHash(raw)
	if hash == "" {
		return access.Principal{}, access.ErrUnauthenticated
	}
	var principal access.Principal
	var scopes []string
	err := s.pool.QueryRow(ctx, `SELECT t.id::text,t.user_id::text,t.scopes FROM api_tokens t
		JOIN users u ON u.id=t.user_id
		WHERE t.secret_hash=$1 AND t.auth_kind='api' AND t.revoked_at IS NULL AND t.expires_at>now() AND u.enabled`, hash).Scan(&principal.TokenID, &principal.UserID, &scopes)
	if errors.Is(err, pgx.ErrNoRows) {
		return access.Principal{}, access.ErrUnauthenticated
	}
	if err != nil {
		return access.Principal{}, err
	}
	for _, scope := range scopes {
		principal.Scopes = append(principal.Scopes, access.Permission(scope))
	}
	return principal, nil
}

// Revoke disables a credential owned by the specified user. In-flight accepted requests may
// finish; subsequent authentication immediately observes revocation without an auth cache.
func (s *CredentialStore) Revoke(ctx context.Context, userID, tokenID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = directoryLock(ctx, tx); err != nil {
		return err
	}
	if err = revokeCredentialTree(ctx, tx, userID, tokenID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
