package postgres

import (
	"context"

	"workgraph.local/platform/internal/bootstrap"
)

// InitializeAdmin serializes with legacy bootstrap and commits an operator without fake provider/catalog records.
func (s *BootstrapStore) InitializeAdmin(ctx context.Context, setup bootstrap.AdminSetup) error {
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(918274610232)`); e != nil {
		return e
	}
	var occupied bool
	if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users UNION ALL SELECT 1 FROM projects)`).Scan(&occupied); e != nil {
		return e
	}
	if occupied {
		return bootstrap.ErrAlreadyInitialized
	}
	if _, e = tx.Exec(ctx, `INSERT INTO users(id,display_name)VALUES($1,$2)`, setup.UserID, setup.DisplayName); e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO directory_operators(user_id)VALUES($1)`, setup.UserID); e != nil {
		return e
	}
	if e = directoryReserve(ctx, tx, "user", setup.UserID); e != nil {
		return e
	}
	if e = saveCredential(ctx, tx, setup.Credential); e != nil {
		return e
	}
	return tx.Commit(ctx)
}
