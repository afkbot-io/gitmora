package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	"workgraph.local/platform/internal/bootstrap"
)

// BootstrapStore initializes a fresh installation under an exclusive transaction lock.
type BootstrapStore struct{ pool *pgxpool.Pool }

// NewBootstrapStore constructs the privileged offline initialization adapter.
func NewBootstrapStore(pool *pgxpool.Pool) *BootstrapStore { return &BootstrapStore{pool: pool} }

// Initialize atomically creates the initial administrator and refuses nonempty installations.
func (s *BootstrapStore) Initialize(ctx context.Context, setup bootstrap.Setup) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock(918274610232)"); err != nil {
		return err
	}
	var occupied bool
	if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM users UNION ALL SELECT 1 FROM projects)").Scan(&occupied); err != nil {
		return err
	}
	if occupied {
		return bootstrap.ErrAlreadyInitialized
	}
	if _, err := tx.Exec(ctx, "INSERT INTO users(id,display_name) VALUES($1,$2)", setup.UserID, setup.Input.DisplayName); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "INSERT INTO projects(id,name) VALUES($1,$2)", setup.ProjectID, setup.Input.ProjectName); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "INSERT INTO memberships(project_id,user_id,role) VALUES($1,$2,'admin')", setup.ProjectID, setup.UserID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "INSERT INTO repositories(id,project_id,gitlab_url,gitlab_project_id,name) VALUES($1,$2,$3,$4,$5)", setup.RepositoryID, setup.ProjectID, setup.Input.GitLabURL, setup.Input.GitLabProjectID, setup.Input.RepositoryName); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "INSERT INTO directory_operators(user_id)VALUES($1)", setup.UserID); err != nil {
		return err
	}
	for _, entry := range []struct{ kind, id string }{{"user", setup.UserID}, {"project", setup.ProjectID}, {"repository", setup.RepositoryID}} {
		if err = directoryReserve(ctx, tx, entry.kind, entry.id); err != nil {
			return err
		}
	}
	if err := saveCredential(ctx, tx, setup.Credential); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
