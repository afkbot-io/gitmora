package postgres

import (
	"context"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	directory "workgraph.local/platform/internal/directory/domain"
	"workgraph.local/platform/internal/identifier"
	"workgraph.local/platform/internal/identity/domain"
)

// Register binds a newly verified subject under the current signup policy. Claims never match an existing user.
func (s *IdentityStore) Register(ctx context.Context, i domain.Identity, configuration string) (domain.Binding, error) {
	if len(configuration) != 64 {
		return domain.Binding{}, domain.ErrLogin
	}
	return s.provision(ctx, i, configuration, "oidc")
}

// CreateAccountOffline is restricted to the database-owner CLI; it does not require public registration.
func (s *IdentityStore) CreateAccountOffline(ctx context.Context, i domain.Identity) (domain.Binding, error) {
	return s.provision(ctx, i, "", "offline")
}

func (s *IdentityStore) provision(ctx context.Context, i domain.Identity, configuration, origin string) (domain.Binding, error) {
	if _, e := domain.URL(i.Issuer); e != nil || !domain.Subject(i.Subject) {
		return domain.Binding{}, domain.ErrInvalid
	}
	name := strings.TrimSpace(i.DisplayName)
	if name == "" || len(name) > 512 || !utf8.ValidString(name) || strings.ContainsFunc(name, unicode.IsControl) {
		return domain.Binding{}, domain.ErrInvalid
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return domain.Binding{}, e
	}
	defer tx.Rollback(ctx)
	if e = directoryLock(ctx, tx); e != nil {
		return domain.Binding{}, e
	}
	if configuration != "" {
		if e = identityConfiguration(ctx, tx, configuration); e != nil {
			return domain.Binding{}, e
		}
		var enabled bool
		if e = tx.QueryRow(ctx, `SELECT registration_enabled FROM identity_runtime WHERE id=1 FOR SHARE`).Scan(&enabled); e != nil {
			return domain.Binding{}, e
		}
		if !enabled {
			return domain.Binding{}, domain.ErrLogin
		}
	}
	// Disabled/retired bindings are tombstones, never a reason to create another account.
	b, e := scanIdentityBinding(tx.QueryRow(ctx, `SELECT `+identityBindingColumns+` FROM identity_bindings b WHERE b.issuer=$1 AND b.subject=$2`, i.Issuer, i.Subject))
	if e == nil {
		var active bool
		if e = tx.QueryRow(ctx, `SELECT enabled AND deleted_at IS NULL FROM users WHERE id=$1`, b.UserID).Scan(&active); e != nil {
			return b, e
		}
		if !b.Enabled || !active {
			return domain.Binding{}, domain.ErrLogin
		}
		return b, tx.Commit(ctx)
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return b, e
	}
	user, e := identifier.New()
	if e != nil {
		return b, e
	}
	binding, e := identifier.New()
	if e != nil {
		return b, e
	}
	request, e := identifier.New()
	if e != nil {
		return b, e
	}
	if e = directoryReserve(ctx, tx, "user", user); e != nil {
		return b, e
	}
	var version int64
	if e = tx.QueryRow(ctx, `INSERT INTO users(id,display_name)VALUES($1,$2)RETURNING version`, user, name).Scan(&version); e != nil {
		return b, e
	}
	b, e = scanIdentityBinding(tx.QueryRow(ctx, `INSERT INTO identity_bindings AS b(id,user_id,issuer,subject)VALUES($1,$2,$3,$4)RETURNING `+identityBindingColumns, binding, user, i.Issuer, i.Subject))
	if e != nil {
		return b, e
	}
	r := directory.Receipt{RequestID: request, Action: directory.CreateUser, Kind: "user", EntityID: user, ActorID: user, Version: version}
	if _, e = directorySaveReceipt(ctx, tx, r, "", origin, directoryFingerprint([]byte(i.Issuer+"\x00"+i.Subject))); e != nil {
		return b, e
	}
	if username, e := directory.Username(i.Username); e == nil {
		if _, e = tx.Exec(ctx, `INSERT INTO user_handles(user_id,username)VALUES($1,$2)ON CONFLICT DO NOTHING`, b.UserID, username); e != nil {
			return b, e
		}
	}
	return b, tx.Commit(ctx)
}
