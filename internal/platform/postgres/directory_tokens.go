package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/directory/domain"
)

// revokeCredentialTree requires directoryLock before it is called: issuance and revocation cannot race a descendant snapshot.
func revokeCredentialTree(ctx context.Context, tx pgx.Tx, user, id string) error {
	var found string
	e := tx.QueryRow(ctx, `SELECT id::text FROM api_tokens WHERE id=$1 AND user_id=$2 FOR UPDATE`, id, user).Scan(&found)
	if errors.Is(e, pgx.ErrNoRows) {
		return access.ErrDenied
	}
	if e != nil {
		return e
	}
	_, e = tx.Exec(ctx, `WITH RECURSIVE descendants(id)AS(SELECT id FROM api_tokens WHERE id=$1 AND user_id=$2 UNION SELECT t.id FROM api_tokens t JOIN descendants d ON t.parent_id=d.id WHERE t.user_id=$2)UPDATE api_tokens SET revoked_at=COALESCE(revoked_at,now())WHERE id IN(SELECT id FROM descendants)`, id, user)
	return e
}

const directoryTokenColumns = `id::text,label,scopes,created_at,expires_at,revoked_at,COALESCE(parent_id::text,''),auth_kind`

func directoryScanToken(row pgx.Row) (domain.Token, error) {
	var t domain.Token
	var caps []string
	e := row.Scan(&t.ID, &t.Label, &caps, &t.CreatedAt, &t.ExpiresAt, &t.RevokedAt, &t.ParentID, &t.Kind)
	for _, cap := range caps {
		t.Scopes = append(t.Scopes, access.Permission(cap))
	}
	return t, directoryNoRows(e)
}
func (s *DirectoryStore) Tokens(ctx context.Context, p access.Principal, c domain.Cursor) (domain.Page[domain.Token], error) {
	c, e := c.Normalize()
	if e != nil {
		return domain.Page[domain.Token]{}, e
	}
	return directoryRead(ctx, s.pool, p, false, func(tx pgx.Tx, a directoryActor) (domain.Page[domain.Token], error) {
		if !a.principal.Has(access.ManageCredentials) {
			return domain.Page[domain.Token]{}, access.ErrDenied
		}
		rows, e := tx.Query(ctx, `SELECT `+directoryTokenColumns+` FROM api_tokens WHERE user_id=$1 AND($2='' OR id>NULLIF($2,'')::uuid)ORDER BY id LIMIT $3`, p.UserID, c.AfterID, c.Limit+1)
		if e != nil {
			return domain.Page[domain.Token]{}, e
		}
		defer rows.Close()
		items := []domain.Token{}
		for rows.Next() {
			v, e := directoryScanToken(rows)
			if e != nil {
				return domain.Page[domain.Token]{}, e
			}
			items = append(items, v)
		}
		return directoryPage(items, c.Limit, func(v domain.Token) string { return v.ID }), rows.Err()
	})
}

// IssueToken persists a hash-only child credential once. Replays return current metadata without re-exposing its secret.
func (s *DirectoryStore) IssueToken(ctx context.Context, p access.Principal, input domain.TokenInput, record access.CredentialRecord) (domain.Token, bool, error) {
	input, e := input.Normalize()
	if e != nil {
		return domain.Token{}, false, e
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return domain.Token{}, false, e
	}
	defer tx.Rollback(ctx)
	if e = directoryLock(ctx, tx); e != nil {
		return domain.Token{}, false, e
	}
	a, e := directoryAuthenticate(ctx, tx, p)
	if e != nil {
		return domain.Token{}, false, e
	}
	if !a.principal.Has(access.ManageCredentials) {
		return domain.Token{}, false, access.ErrDenied
	}
	raw, _ := json.Marshal(input)
	hash := directoryFingerprint(raw)
	previous, found, e := directoryExisting(ctx, tx, p.UserID, input.RequestID, hash)
	if e != nil {
		return domain.Token{}, false, e
	}
	if found {
		token, e := directoryScanToken(tx.QueryRow(ctx, `SELECT `+directoryTokenColumns+` FROM api_tokens WHERE id=$1 AND user_id=$2`, previous.EntityID, p.UserID))
		if e != nil {
			return token, false, e
		}
		return token, false, tx.Commit(ctx)
	}
	if record.UserID != p.UserID || record.Label != input.Label || !slices.Equal(record.Scopes, input.Scopes) || record.CreatedAt.Before(time.Now().Add(-time.Minute)) || record.CreatedAt.After(time.Now().Add(5*time.Second)) || !record.ExpiresAt.Equal(record.CreatedAt.Add(time.Duration(input.TTLSeconds)*time.Second)) {
		return domain.Token{}, false, domain.ErrInvalid
	}
	if record.ExpiresAt.After(a.expires) {
		return domain.Token{}, false, domain.ErrDelegation
	}
	for _, cap := range input.Scopes {
		if !a.principal.Has(cap) {
			return domain.Token{}, false, domain.ErrDelegation
		}
	}
	var depth, live int
	e = tx.QueryRow(ctx, `WITH RECURSIVE ancestors(id,parent_id,depth)AS(SELECT id,parent_id,1 FROM api_tokens WHERE id=$1 UNION ALL SELECT t.id,t.parent_id,a.depth+1 FROM api_tokens t JOIN ancestors a ON t.id=a.parent_id WHERE a.depth<9)SELECT count(*)FROM ancestors`, p.TokenID).Scan(&depth)
	if e != nil {
		return domain.Token{}, false, e
	}
	if depth >= 8 {
		return domain.Token{}, false, domain.ErrDelegation
	}
	if e = tx.QueryRow(ctx, `SELECT count(*)FROM api_tokens WHERE user_id=$1 AND revoked_at IS NULL AND expires_at>now()`, p.UserID).Scan(&live); e != nil {
		return domain.Token{}, false, e
	}
	if live >= 100 {
		return domain.Token{}, false, domain.ErrConflict
	}
	if e = saveCredential(ctx, tx, record); e != nil {
		return domain.Token{}, false, e
	}
	if _, e = tx.Exec(ctx, `UPDATE api_tokens SET parent_id=$2 WHERE id=$1`, record.ID, p.TokenID); e != nil {
		return domain.Token{}, false, e
	}
	r := domain.Receipt{RequestID: input.RequestID, Action: "issue_token", Kind: "token", EntityID: record.ID, ActorID: p.UserID}
	r.Version, e = directoryNextVersion(ctx, tx)
	if e != nil {
		return domain.Token{}, false, e
	}
	if _, e = directorySaveReceipt(ctx, tx, r, p.TokenID, "api", hash); e != nil {
		return domain.Token{}, false, e
	}
	token, e := directoryScanToken(tx.QueryRow(ctx, `SELECT `+directoryTokenColumns+` FROM api_tokens WHERE id=$1`, record.ID))
	if e != nil {
		return token, false, e
	}
	return token, true, tx.Commit(ctx)
}
