package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/forge/application"
	"workgraph.local/platform/internal/forge/domain"
	"workgraph.local/platform/internal/identifier"
)

func (s *ForgeStore) credentialProject(ctx context.Context, subject, project string, fn func(pgx.Tx) error) error {
	if !identifier.Valid(subject) || !identifier.Valid(project) {
		return access.ErrDenied
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var role string
	err = tx.QueryRow(ctx, `SELECT m.role FROM memberships m JOIN users u ON u.id=m.user_id JOIN projects p ON p.id=m.project_id
 WHERE m.project_id=$1 AND m.user_id=$2 AND u.enabled FOR SHARE OF m,u,p`, project, subject).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && !access.Allows(role, access.ManageIntegration) {
		return access.ErrDenied
	}
	if err != nil {
		return err
	}
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Credentials pages labels without decrypting or exposing any stored secret.
func (s *ForgeStore) Credentials(ctx context.Context, subject, project, after string) (application.CredentialPage, error) {
	page := application.CredentialPage{Items: []application.CredentialOption{}}
	err := s.credentialProject(ctx, subject, project, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT r.id::text,c.generation,r.name,r.provider,r.gitlab_url FROM repositories r JOIN forge_connections c ON c.repository_id=r.id
 WHERE r.project_id=$1 AND c.enabled AND NOT c.anonymous AND NOT c.blocked AND ($2='' OR r.id>NULLIF($2,'')::uuid) ORDER BY r.id LIMIT 101`, project, after)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var option application.CredentialOption
			var repo domain.Repository
			if err = rows.Scan(&option.RepositoryID, &option.Generation, &option.Name, &repo.Provider, &repo.BaseURL); err != nil {
				return err
			}
			option.Provider = "gitlab"
			if repo.Provider == "github" {
				option.Provider = "github"
			}
			option.Endpoint = repo.CredentialEndpoint()
			if len(page.Items) == 100 {
				page.NextID = page.Items[99].RepositoryID
				break
			}
			page.Items = append(page.Items, option)
		}
		return rows.Err()
	})
	return page, err
}

func readReusableCredential(ctx context.Context, tx pgx.Tx, project string, ref application.CredentialRef) (application.Connection, error) {
	var connection application.Connection
	var enabled, anonymous, blocked bool
	err := tx.QueryRow(ctx, `SELECT r.id::text,r.gitlab_url,r.gitlab_project_id,r.provider,c.generation,c.secret_envelope,c.enabled,c.anonymous,c.blocked
 FROM repositories r JOIN forge_connections c ON c.repository_id=r.id WHERE r.id=$1 AND r.project_id=$2 FOR SHARE OF r,c`, ref.RepositoryID, project).
		Scan(&connection.Repository.ID, &connection.Repository.BaseURL, &connection.Repository.ProjectID, &connection.Repository.Provider, &connection.Generation, &connection.Envelope, &enabled, &anonymous, &blocked)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && (!enabled || anonymous || blocked || connection.Generation != ref.Generation) {
		return application.Connection{}, domain.ErrCredentialUnavailable
	}
	return connection, err
}

// Credential opens only an administrator-authorized connection in the selected project.
func (s *ForgeStore) Credential(ctx context.Context, subject, project string, ref application.CredentialRef) (application.Connection, error) {
	var connection application.Connection
	if !ref.Valid() {
		return connection, domain.ErrInvalid
	}
	err := s.credentialProject(ctx, subject, project, func(tx pgx.Tx) error {
		var err error
		connection, err = readReusableCredential(ctx, tx, project, ref)
		return err
	})
	return connection, err
}

// CredentialForTarget derives the project from the authorized target, never a caller-supplied project ID.
func (s *ForgeStore) CredentialForTarget(ctx context.Context, subject, target string, ref application.CredentialRef) (application.Connection, error) {
	var connection application.Connection
	if !ref.Valid() {
		return connection, domain.ErrInvalid
	}
	err := s.authorized(ctx, subject, target, access.ManageIntegration, func(tx pgx.Tx, _ domain.Repository) error {
		var project string
		if err := tx.QueryRow(ctx, `SELECT project_id::text FROM repositories WHERE id=$1`, target).Scan(&project); err != nil {
			return err
		}
		var err error
		connection, err = readReusableCredential(ctx, tx, project, ref)
		return err
	})
	return connection, err
}

// fenceCredential runs in the target write transaction, after the provider probe.
func fenceCredential(ctx context.Context, tx pgx.Tx, target domain.Repository, source application.Connection) error {
	// Catalog deletion locks repository rows before cascading to connection rows.
	rows, err := tx.Query(ctx, `SELECT id FROM repositories WHERE id=ANY($1::uuid[]) ORDER BY id FOR SHARE`, []string{target.ID, source.Repository.ID})
	if err != nil {
		return err
	}
	for rows.Next() {
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	// Lock connections in one order even when two administrators reuse A->B and B->A.
	rows, err = tx.Query(ctx, `SELECT repository_id FROM forge_connections WHERE repository_id=ANY($1::uuid[]) ORDER BY repository_id FOR UPDATE`, []string{target.ID, source.Repository.ID})
	if err != nil {
		return err
	}
	for rows.Next() {
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	var project string
	if err = tx.QueryRow(ctx, `SELECT project_id::text FROM repositories WHERE id=$1`, target.ID).Scan(&project); err != nil {
		return err
	}
	current, err := readReusableCredential(ctx, tx, project, application.CredentialRef{RepositoryID: source.Repository.ID, Generation: source.Generation})
	if err != nil {
		return err
	}
	if current.Repository != source.Repository {
		return domain.ErrCredentialUnavailable
	}
	if current.Repository.Provider != target.Provider || current.Repository.CredentialEndpoint() != target.CredentialEndpoint() {
		return domain.ErrCredentialEndpoint
	}
	return nil
}
