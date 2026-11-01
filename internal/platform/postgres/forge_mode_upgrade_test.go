package postgres_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"workgraph.local/platform/internal/platform/postgres"
)

func TestForgeAPIOnlyMigrationPreservesExistingWebhookPolicy(t *testing.T) {
	pool := databaseSetup(t, false)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `CREATE TABLE schema_migrations(name text PRIMARY KEY,checksum text NOT NULL,applied_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		t.Fatal(err)
	}
	names, err := os.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		if name.Name() >= "0020" {
			continue
		}
		path := filepath.Join("migrations", name.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, string(raw)); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(raw)
		if _, err := pool.Exec(ctx, `INSERT INTO schema_migrations(name,checksum) VALUES($1,$2)`, filepath.ToSlash(path), fmt.Sprintf("%x", sum)); err != nil {
			t.Fatal(err)
		}
	}
	seed(t, pool)
	if _, err := pool.Exec(ctx, `INSERT INTO forge_connections(repository_id,webhook_mode,secret_envelope,updated_by,generation)VALUES($1,'signature',decode(repeat('ab',32),'hex'),$2,9)`, repository, subject); err != nil {
		t.Fatal(err)
	}
	var before, after string
	if err := pool.QueryRow(ctx, "SELECT to_jsonb(c)::text FROM forge_connections c WHERE repository_id=$1", repository).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err := postgres.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := postgres.VerifySchema(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, "SELECT (to_jsonb(c)-'anonymous')::text FROM forge_connections c WHERE repository_id=$1", repository).Scan(&after); err != nil || before != after {
		t.Fatal("upgrade changed existing webhook authority or reconciliation state")
	}
	var anonymous bool
	if err := pool.QueryRow(ctx, "SELECT anonymous FROM forge_connections WHERE repository_id=$1", repository).Scan(&anonymous); err != nil || anonymous {
		t.Fatal("upgrade changed old token authentication", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE forge_connections SET webhook_mode='disabled' WHERE repository_id=$1", repository); err != nil {
		t.Fatal("upgrade did not admit explicit API-only policy", err)
	}
	if _, err := pool.Exec(ctx, "UPDATE forge_connections SET webhook_mode='unknown' WHERE repository_id=$1", repository); err == nil {
		t.Fatal("upgrade weakened unknown-mode constraint")
	}
}
