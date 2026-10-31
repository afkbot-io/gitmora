// Package postgres implements transactional persistence adapters using PostgreSQL.
package postgres

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrations embed.FS

// VerifySchema checks that every embedded migration is present and unchanged. A database
// connection alone is insufficient readiness when the application schema is missing or stale.
func VerifySchema(ctx context.Context, pool *pgxpool.Pool) error {
	return verifySchema(ctx, pool)
}

type schemaQuerier interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func verifySchema(ctx context.Context, source schemaQuerier) error {
	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	expected := make(map[string]string, len(names))
	for _, name := range names {
		content, err := migrations.ReadFile(name)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(content)
		expected[name] = hex.EncodeToString(sum[:])
	}
	rows, err := source.Query(ctx, "SELECT name,checksum FROM schema_migrations")
	if err != nil {
		return fmt.Errorf("read migration history: %w", err)
	}
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var name, checksum string
		if err := rows.Scan(&name, &checksum); err != nil {
			return err
		}
		value, ok := expected[name]
		if !ok || value != checksum {
			return errors.New("migration history does not match this application")
		}
		seen++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if seen != len(expected) {
		return errors.New("database migrations are incomplete")
	}
	return nil
}

// SchemaFingerprint binds the ordered embedded migration names and bytes for exact-version recovery.
func SchemaFingerprint() (string, error) {
	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return "", err
	}
	sort.Strings(names)
	hash := sha256.New()
	_, _ = hash.Write([]byte("gitmora-schema-v1\x00"))
	for _, name := range names {
		content, err := migrations.ReadFile(name)
		if err != nil {
			return "", err
		}
		sum := sha256.Sum256(content)
		_, _ = fmt.Fprintf(hash, "%s\x00%x\n", name, sum)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// Migrate applies ordered embedded SQL atomically, serializes concurrent migrators and
// rejects modified or unknown applied migrations. A failed migration leaves no partial schema.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin migrations: %w", err)
	}
	defer tx.Rollback(ctx) // PostgreSQL ignores rollback after a successful commit.
	if _, err = tx.Exec(ctx, "SELECT pg_advisory_xact_lock(918274610231)"); err != nil {
		return fmt.Errorf("lock migrations: %w", err)
	}
	if _, err = tx.Exec(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations(name text PRIMARY KEY, checksum text NOT NULL, applied_at timestamptz NOT NULL DEFAULT now())"); err != nil {
		return fmt.Errorf("migration ledger: %w", err)
	}
	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	known := make(map[string]string, len(names))
	content := make(map[string][]byte, len(names))
	for _, name := range names {
		bytes, err := migrations.ReadFile(name)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(bytes)
		known[name] = hex.EncodeToString(sum[:])
		content[name] = bytes
	}
	rows, err := tx.Query(ctx, "SELECT name,checksum FROM schema_migrations")
	if err != nil {
		return err
	}
	applied := make(map[string]bool)
	for rows.Next() {
		var name, checksum string
		if err := rows.Scan(&name, &checksum); err != nil {
			rows.Close()
			return err
		}
		expected, exists := known[name]
		if !exists || expected != checksum {
			rows.Close()
			return fmt.Errorf("migration drift: %s", name)
		}
		applied[name] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, name := range names {
		if applied[name] {
			continue
		}
		if _, err := tx.Exec(ctx, string(content[name])); err != nil {
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations(name,checksum) VALUES($1,$2)", name, known[name]); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit migrations: %w", err)
	}
	return nil
}
