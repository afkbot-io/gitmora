package postgres

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/directory/domain"
	"workgraph.local/platform/internal/identifier"
)

// directoryLock serializes local identity/catalog writes and delegation/revocation decisions at team scale.
// Content ingestion/read workers do not acquire it and retain their existing row fences.
func directoryLock(ctx context.Context, tx pgx.Tx) error {
	_, e := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(918274610240)`)
	return e
}

type directoryActor struct {
	principal access.Principal
	operator  bool
	expires   time.Time
}

func directoryAuthenticate(ctx context.Context, tx pgx.Tx, p access.Principal) (directoryActor, error) {
	if !identifier.Valid(p.UserID) || !identifier.Valid(p.TokenID) {
		return directoryActor{}, access.ErrDenied
	}
	var caps []string
	out := directoryActor{principal: access.Principal{UserID: p.UserID, TokenID: p.TokenID, Scopes: []access.Permission{}}}
	e := tx.QueryRow(ctx, `SELECT t.scopes,t.expires_at,EXISTS(SELECT 1 FROM directory_operators o WHERE o.user_id=u.id) FROM api_tokens t JOIN users u ON u.id=t.user_id WHERE t.id=$1 AND t.user_id=$2 AND t.revoked_at IS NULL AND t.expires_at>now() AND u.enabled AND u.deleted_at IS NULL FOR SHARE OF t,u`, p.TokenID, p.UserID).Scan(&caps, &out.expires, &out.operator)
	if errors.Is(e, pgx.ErrNoRows) {
		return out, access.ErrDenied
	}
	if e != nil {
		return out, e
	}
	for _, cap := range p.Scopes {
		if access.SupportedPermission(cap) && slices.Contains(caps, string(cap)) {
			out.principal.Scopes = append(out.principal.Scopes, cap)
		}
	}
	if len(out.principal.Scopes) == 0 {
		return out, access.ErrDenied
	}
	return out, nil
}
func (a directoryActor) platform() bool { return a.operator && a.principal.Has(access.ManagePlatform) }
func directoryProjectAuthority(ctx context.Context, tx pgx.Tx, a directoryActor, project string, manage bool) error {
	if !identifier.Valid(project) || !a.principal.Has(access.ReadDirectory) {
		return access.ErrDenied
	}
	if a.platform() {
		var exists bool
		e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE id=$1)`, project).Scan(&exists)
		if e != nil {
			return e
		}
		if !exists {
			return domain.ErrNotFound
		}
		return nil
	}
	if manage && !a.principal.Has(access.ManageDirectory) {
		return access.ErrDenied
	}
	var role string
	e := tx.QueryRow(ctx, `SELECT role FROM memberships WHERE project_id=$1 AND user_id=$2 FOR SHARE`, project, a.principal.UserID).Scan(&role)
	if errors.Is(e, pgx.ErrNoRows) {
		return access.ErrDenied
	}
	if e != nil {
		return e
	}
	cap := access.ReadDirectory
	if manage {
		cap = access.ManageDirectory
	}
	if !access.Allows(role, cap) {
		return access.ErrDenied
	}
	return nil
}
func directoryCommandCapability(a directoryActor, c domain.Command) error {
	if c.Action == domain.RevokeToken {
		if a.principal.Has(access.ManageCredentials) {
			return nil
		}
		return access.ErrDenied
	}
	if !a.principal.Has(access.ReadDirectory) {
		return access.ErrDenied
	}
	if c.Platform() {
		if !a.principal.Has(access.ManagePlatform) {
			return access.ErrDenied
		}
		return nil
	}
	if !a.principal.Has(access.ManageDirectory) && !a.principal.Has(access.ManagePlatform) {
		return access.ErrDenied
	}
	return nil
}
