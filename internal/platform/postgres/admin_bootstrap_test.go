package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/bootstrap"
	"workgraph.local/platform/internal/platform/postgres"
)

func TestFreshInstallationAdministratorDoesNotCreateAFakeProjectOrRepository(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	store := postgres.NewBootstrapStore(pool)
	r, e := bootstrap.NewAdminService(store, time.Now).Initialize(ctx, "First operator", 24*time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	p, e := postgres.NewCredentialStore(pool).Authenticate(ctx, r.Credential.Secret)
	if e != nil || p.UserID != r.UserID || !p.Has(access.ManagePlatform) {
		t.Fatal("initial credential not durable", e)
	}
	var projects, repositories, operators int
	if e = pool.QueryRow(ctx, `SELECT(SELECT count(*)FROM projects),(SELECT count(*)FROM repositories),(SELECT count(*)FROM directory_operators WHERE user_id=$1)`, r.UserID).Scan(&projects, &repositories, &operators); e != nil || projects != 0 || repositories != 0 || operators != 1 {
		t.Fatal("initialization invented provider/catalog state", e)
	}
	if _, e = bootstrap.NewAdminService(store, time.Now).Initialize(ctx, "Another", time.Hour); !errors.Is(e, bootstrap.ErrAlreadyInitialized) {
		t.Fatal("second bootstrap claimed populated installation", e)
	}
}
