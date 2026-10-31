package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"workgraph.local/platform/internal/bootstrap"
	"workgraph.local/platform/internal/platform/postgres"
)

func TestBootstrapIsAtomicAndCannotClaimExistingInstallation(t *testing.T) {
	pool := database(t)
	ctx := context.Background()
	service := bootstrap.NewService(postgres.NewBootstrapStore(pool), time.Now)
	input := bootstrap.Input{DisplayName: "Administrator", ProjectName: "Team", RepositoryName: "Application", GitLabURL: "https://gitlab.internal/", GitLabProjectID: 42}
	if _, err := pool.Exec(ctx, "ALTER TABLE api_tokens ADD CONSTRAINT simulated_failure CHECK(false) NOT VALID"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Initialize(ctx, input); err == nil {
		t.Fatal("bootstrap succeeded without a usable token")
	}
	for _, table := range []string{"users", "projects", "memberships", "repositories", "api_tokens"} {
		if count(t, pool, table) != 0 {
			t.Errorf("partial setup in %s", table)
		}
	}
	if _, err := pool.Exec(ctx, "ALTER TABLE api_tokens DROP CONSTRAINT simulated_failure"); err != nil {
		t.Fatal(err)
	}
	result, err := service.Initialize(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.NewCredentialStore(pool).Authenticate(ctx, result.Credential.Secret); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Initialize(ctx, input); !errors.Is(err, bootstrap.ErrAlreadyInitialized) {
		t.Fatalf("second setup: %v", err)
	}
	if count(t, pool, "users") != 1 || count(t, pool, "api_tokens") != 1 {
		t.Fatal("second setup changed installation")
	}
}
