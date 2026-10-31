package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/directory/domain"
	"workgraph.local/platform/internal/identifier"
)

func directoryFingerprint(raw []byte) string {
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}
func decodeDirectoryReceipt(raw []byte, r *domain.Receipt) error { return json.Unmarshal(raw, r) }
func directoryError(e error) error {
	var pg *pgconn.PgError
	if errors.As(e, &pg) && (pg.Code == "23505" || pg.Code == "23503") {
		return domain.ErrConflict
	}
	return e
}
func directoryTransient(e error) bool {
	var pg *pgconn.PgError
	return errors.As(e, &pg) && (pg.Code == "40P01" || pg.Code == "40001")
}

func directoryExisting(ctx context.Context, tx pgx.Tx, user, request, fingerprint string) (domain.Receipt, bool, error) {
	var raw []byte
	var hash string
	e := tx.QueryRow(ctx, `SELECT fingerprint,receipt FROM directory_commands WHERE actor_id=$1 AND request_id=$2`, user, request).Scan(&hash, &raw)
	if errors.Is(e, pgx.ErrNoRows) {
		return domain.Receipt{}, false, nil
	}
	if e != nil {
		return domain.Receipt{}, false, e
	}
	if hash != fingerprint {
		return domain.Receipt{}, false, domain.ErrConflict
	}
	var r domain.Receipt
	e = decodeDirectoryReceipt(raw, &r)
	return r, true, e
}
func directoryReserve(ctx context.Context, tx pgx.Tx, kind, id string) error {
	var retired bool
	e := tx.QueryRow(ctx, `SELECT retired_at IS NOT NULL FROM directory_identifiers WHERE kind=$1 AND id=$2`, kind, id).Scan(&retired)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return e
	}
	if retired {
		return domain.ErrRetired
	}
	_, e = tx.Exec(ctx, `INSERT INTO directory_identifiers(kind,id)VALUES($1,$2)ON CONFLICT DO NOTHING`, kind, id)
	return e
}
func directoryRetire(ctx context.Context, tx pgx.Tx, kind, id string) error {
	_, e := tx.Exec(ctx, `INSERT INTO directory_identifiers(kind,id,retired_at)VALUES($1,$2,now())ON CONFLICT(kind,id)DO UPDATE SET retired_at=EXCLUDED.retired_at`, kind, id)
	return e
}
func directoryTouchProject(ctx context.Context, tx pgx.Tx, id string) (int64, error) {
	var v int64
	e := tx.QueryRow(ctx, `UPDATE projects SET version=nextval('directory_revision') WHERE id=$1 RETURNING version`, id).Scan(&v)
	return v, e
}
func directoryNextVersion(ctx context.Context, tx pgx.Tx) (int64, error) {
	var v int64
	e := tx.QueryRow(ctx, `SELECT nextval('directory_revision')`).Scan(&v)
	return v, e
}
func directorySaveReceipt(ctx context.Context, tx pgx.Tx, r domain.Receipt, token, origin, hash string) (domain.Receipt, error) {
	e := tx.QueryRow(ctx, `SELECT nextval('directory_revision'),now()`).Scan(&r.Sequence, &r.At)
	if e != nil {
		return r, e
	}
	r.At = r.At.UTC()
	raw, _ := json.Marshal(r)
	_, e = tx.Exec(ctx, `INSERT INTO directory_events(seq,actor_id,token_id,project_id,entity_id,origin,receipt)VALUES($1,$2,NULLIF($3,'')::uuid,NULLIF($4,'')::uuid,$5,$6,$7)`, r.Sequence, r.ActorID, token, r.ProjectID, r.EntityID, origin, raw)
	if e != nil {
		return r, e
	}
	_, e = tx.Exec(ctx, `INSERT INTO directory_commands(actor_id,request_id,fingerprint,receipt)VALUES($1,$2,$3,$4)`, r.ActorID, r.RequestID, hash, raw)
	return r, e
}

// Apply retries only database deadlock/serialization aborts. The request ledger and state change always commit together.
func (s *DirectoryStore) Apply(ctx context.Context, p access.Principal, input domain.Command) (domain.Receipt, bool, error) {
	c, e := domain.Normalize(input)
	if e != nil {
		return domain.Receipt{}, false, e
	}
	for attempt := 0; attempt < 3; attempt++ {
		r, created, err := s.apply(ctx, p, c)
		if !directoryTransient(err) {
			return r, created, directoryError(err)
		}
		e = err
	}
	return domain.Receipt{}, false, e
}
func (s *DirectoryStore) apply(ctx context.Context, p access.Principal, c domain.Command) (domain.Receipt, bool, error) {
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return domain.Receipt{}, false, e
	}
	defer tx.Rollback(ctx)
	if e = directoryLock(ctx, tx); e != nil {
		return domain.Receipt{}, false, e
	}
	a, e := directoryAuthenticate(ctx, tx, p)
	if e != nil {
		return domain.Receipt{}, false, e
	}
	if e = directoryCommandCapability(a, c); e != nil {
		return domain.Receipt{}, false, e
	}
	hash := directoryFingerprint(c.Canonical())
	previous, found, e := directoryExisting(ctx, tx, p.UserID, c.RequestID, hash)
	if e != nil {
		return domain.Receipt{}, false, e
	}
	if found {
		return previous, false, tx.Commit(ctx)
	}
	if c.Platform() {
		if !a.platform() {
			return domain.Receipt{}, false, access.ErrDenied
		}
	} else if c.Action != domain.RevokeToken {
		if e = directoryProjectAuthority(ctx, tx, a, c.ScopeProject(), true); e != nil {
			return domain.Receipt{}, false, e
		}
	}
	r := domain.Receipt{RequestID: c.RequestID, Action: c.Action, Kind: c.Kind(), EntityID: c.EntityID, ProjectID: c.ScopeProject(), ActorID: p.UserID, Deleted: c.Deleted()}
	switch c.Kind() {
	case "project":
		e = directoryChangeProject(ctx, tx, a, c, &r)
	case "repository":
		e = directoryChangeRepository(ctx, tx, c, &r)
	case "membership":
		e = directoryChangeMember(ctx, tx, c, &r)
	case "user":
		e = directoryChangeUser(ctx, tx, c, &r)
	case "token":
		r.ProjectID = ""
		e = revokeCredentialTree(ctx, tx, p.UserID, c.EntityID)
		if e == nil {
			r.Version, e = directoryNextVersion(ctx, tx)
		}
	}
	if e != nil {
		return domain.Receipt{}, false, e
	}
	r, e = directorySaveReceipt(ctx, tx, r, p.TokenID, "api", hash)
	if e != nil {
		return domain.Receipt{}, false, e
	}
	return r, true, tx.Commit(ctx)
}

func directoryChangeProject(ctx context.Context, tx pgx.Tx, a directoryActor, c domain.Command, r *domain.Receipt) error {
	if c.Action == domain.CreateProject {
		if e := directoryReserve(ctx, tx, "project", c.EntityID); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx, `INSERT INTO projects(id,name)VALUES($1,$2)RETURNING version`, c.EntityID, *c.Name).Scan(&r.Version); e != nil {
			return e
		}
		_, e := tx.Exec(ctx, `INSERT INTO memberships(project_id,user_id,role)VALUES($1,$2,'admin')`, c.EntityID, a.principal.UserID)
		r.ProjectVersion = r.Version
		return e
	}
	var version int64
	e := tx.QueryRow(ctx, `SELECT version FROM projects WHERE id=$1 FOR UPDATE`, c.EntityID).Scan(&version)
	if e != nil {
		return directoryNoRows(e)
	}
	if e = domain.MatchVersion(c.ExpectedVersion, version); e != nil {
		return e
	}
	if c.Action == domain.RenameProject {
		e = tx.QueryRow(ctx, `UPDATE projects SET name=$2,version=nextval('directory_revision')WHERE id=$1 RETURNING version`, c.EntityID, *c.Name).Scan(&r.Version)
		r.ProjectVersion = r.Version
		return e
	}
	// Record child tombstones before a transactional local cascade; external GitLab/Tasktopia data is never deleted.
	_, e = tx.Exec(ctx, `INSERT INTO directory_identifiers(kind,id,retired_at)SELECT 'repository',id,now() FROM repositories WHERE project_id=$1 ON CONFLICT(kind,id)DO UPDATE SET retired_at=EXCLUDED.retired_at`, c.EntityID)
	if e != nil {
		return e
	}
	if _, e = tx.Exec(ctx, `DELETE FROM projects WHERE id=$1`, c.EntityID); e != nil {
		return e
	}
	if e = directoryRetire(ctx, tx, "project", c.EntityID); e != nil {
		return e
	}
	r.Version, e = directoryNextVersion(ctx, tx)
	r.ProjectVersion = r.Version
	return e
}

func directoryChangeRepository(ctx context.Context, tx pgx.Tx, c domain.Command, r *domain.Receipt) error {
	if c.Action == domain.CreateRepository {
		if e := directoryReserve(ctx, tx, "repository", c.EntityID); e != nil {
			return e
		}
		if e := tx.QueryRow(ctx, `INSERT INTO repositories(id,project_id,name,gitlab_url,gitlab_project_id)VALUES($1,$2,$3,$4,$5)RETURNING version`, c.EntityID, c.ProjectID, *c.Name, c.GitLabURL, c.GitLabProjectID).Scan(&r.Version); e != nil {
			return e
		}
	} else {
		var version int64
		e := tx.QueryRow(ctx, `SELECT version FROM repositories WHERE id=$1 AND project_id=$2 FOR UPDATE`, c.EntityID, c.ProjectID).Scan(&version)
		if e != nil {
			return directoryNoRows(e)
		}
		if e = domain.MatchVersion(c.ExpectedVersion, version); e != nil {
			return e
		}
		if c.Action == domain.RenameRepository {
			e = tx.QueryRow(ctx, `UPDATE repositories SET name=$2,version=nextval('directory_revision')WHERE id=$1 RETURNING version`, c.EntityID, *c.Name).Scan(&r.Version)
		} else {
			_, e = tx.Exec(ctx, `DELETE FROM repositories WHERE id=$1`, c.EntityID)
			if e == nil {
				e = directoryRetire(ctx, tx, "repository", c.EntityID)
			}
			if e == nil {
				r.Version, e = directoryNextVersion(ctx, tx)
			}
		}
		if e != nil {
			return e
		}
	}
	var e error
	r.ProjectVersion, e = directoryTouchProject(ctx, tx, c.ProjectID)
	return e
}

func directoryAdminCount(ctx context.Context, tx pgx.Tx, project string) (int, error) {
	var n int
	e := tx.QueryRow(ctx, `SELECT count(*) FROM memberships m JOIN users u ON u.id=m.user_id AND u.enabled WHERE m.project_id=$1 AND m.role='admin'`, project).Scan(&n)
	return n, e
}
func directoryChangeMember(ctx context.Context, tx pgx.Tx, c domain.Command, r *domain.Receipt) error {
	var enabled bool
	e := tx.QueryRow(ctx, `SELECT enabled FROM users WHERE id=$1 AND deleted_at IS NULL FOR SHARE`, c.EntityID).Scan(&enabled)
	if e != nil {
		return directoryNoRows(e)
	}
	if !enabled && c.Action == domain.SetMember {
		return domain.ErrConflict
	}
	var role string
	var version int64
	e = tx.QueryRow(ctx, `SELECT role,version FROM memberships WHERE project_id=$1 AND user_id=$2 FOR UPDATE`, c.ProjectID, c.EntityID).Scan(&role, &version)
	if errors.Is(e, pgx.ErrNoRows) {
		if c.ExpectedVersion != 0 || c.Action == domain.RemoveMember {
			return domain.ErrConflict
		}
		e = tx.QueryRow(ctx, `INSERT INTO memberships(project_id,user_id,role)VALUES($1,$2,$3)RETURNING version`, c.ProjectID, c.EntityID, c.Role).Scan(&r.Version)
	} else if e == nil {
		if e = domain.MatchVersion(c.ExpectedVersion, version); e != nil {
			return e
		}
		next := c.Role
		if c.Action == domain.RemoveMember {
			next = ""
		}
		n, err := directoryAdminCount(ctx, tx, c.ProjectID)
		if err != nil {
			return err
		}
		if e = domain.CheckAdminRemoval(role, enabled, next, n); e != nil {
			return e
		}
		if c.Action == domain.RemoveMember {
			_, e = tx.Exec(ctx, `DELETE FROM memberships WHERE project_id=$1 AND user_id=$2`, c.ProjectID, c.EntityID)
			if e == nil {
				r.Version, e = directoryNextVersion(ctx, tx)
			}
		} else {
			e = tx.QueryRow(ctx, `UPDATE memberships SET role=$3,version=nextval('directory_revision')WHERE project_id=$1 AND user_id=$2 RETURNING version`, c.ProjectID, c.EntityID, c.Role).Scan(&r.Version)
		}
	}
	if e != nil {
		return e
	}
	r.ProjectVersion, e = directoryTouchProject(ctx, tx, c.ProjectID)
	return e
}

func directoryProtectUser(ctx context.Context, tx pgx.Tx, u domain.User) error {
	var operators int
	e := tx.QueryRow(ctx, `SELECT count(*) FROM directory_operators o JOIN users u ON u.id=o.user_id AND u.enabled`).Scan(&operators)
	if e != nil {
		return e
	}
	if e = domain.CheckOperatorRemoval(u.Operator, u.Enabled, false, operators); e != nil {
		return e
	}
	if !u.Enabled {
		return nil
	}
	rows, e := tx.Query(ctx, `SELECT m.project_id::text FROM memberships m WHERE m.user_id=$1 AND m.role='admin'`, u.ID)
	if e != nil {
		return e
	}
	projects := []string{}
	for rows.Next() {
		var id string
		if e = rows.Scan(&id); e != nil {
			rows.Close()
			return e
		}
		projects = append(projects, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return e
	}
	for _, id := range projects {
		n, err := directoryAdminCount(ctx, tx, id)
		if err != nil {
			return err
		}
		if e = domain.CheckAdminRemoval("admin", true, "", n); e != nil {
			return e
		}
	}
	return nil
}
func directoryChangeUser(ctx context.Context, tx pgx.Tx, c domain.Command, r *domain.Receipt) error {
	r.ProjectID = ""
	if c.Action == domain.CreateUser {
		if e := directoryReserve(ctx, tx, "user", c.EntityID); e != nil {
			return e
		}
		return tx.QueryRow(ctx, `INSERT INTO users(id,display_name)VALUES($1,$2)RETURNING version`, c.EntityID, *c.Name).Scan(&r.Version)
	}
	u, e := directoryUser(ctx, tx, c.EntityID, true)
	if e != nil {
		return e
	}
	if u.DeletedAt != nil {
		return domain.ErrRetired
	}
	if e = domain.MatchVersion(c.ExpectedVersion, u.Version); e != nil {
		return e
	}
	if c.Action == domain.SetOperator {
		if !u.Enabled {
			return domain.ErrConflict
		}
		if !*c.Operator {
			var n int
			if e = tx.QueryRow(ctx, `SELECT count(*) FROM directory_operators o JOIN users u ON u.id=o.user_id AND u.enabled`).Scan(&n); e != nil {
				return e
			}
			if e = domain.CheckOperatorRemoval(u.Operator, u.Enabled, false, n); e != nil {
				return e
			}
			_, e = tx.Exec(ctx, `DELETE FROM directory_operators WHERE user_id=$1`, u.ID)
		} else {
			_, e = tx.Exec(ctx, `INSERT INTO directory_operators(user_id)VALUES($1)ON CONFLICT DO NOTHING`, u.ID)
		}
		if e != nil {
			return e
		}
		return tx.QueryRow(ctx, `UPDATE users SET version=nextval('directory_revision')WHERE id=$1 RETURNING version`, u.ID).Scan(&r.Version)
	}
	disabling := c.Action == domain.DeleteUser || c.Enabled != nil && !*c.Enabled
	if disabling {
		if e = directoryProtectUser(ctx, tx, u); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, `UPDATE api_tokens SET revoked_at=COALESCE(revoked_at,now())WHERE user_id=$1`, u.ID); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, `DELETE FROM directory_operators WHERE user_id=$1`, u.ID); e != nil {
			return e
		}
	}
	if c.Action == domain.DeleteUser {
		if _, e = tx.Exec(ctx, `UPDATE projects SET version=nextval('directory_revision')WHERE id IN(SELECT project_id FROM memberships WHERE user_id=$1)`, u.ID); e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, `DELETE FROM memberships WHERE user_id=$1`, u.ID); e != nil {
			return e
		}
		if e = directoryRetire(ctx, tx, "user", u.ID); e != nil {
			return e
		}
		return tx.QueryRow(ctx, `UPDATE users SET display_name='Deleted user',enabled=false,deleted_at=now(),version=nextval('directory_revision')WHERE id=$1 RETURNING version`, u.ID).Scan(&r.Version)
	}
	name, enabled := u.DisplayName, u.Enabled
	if c.Name != nil {
		name = *c.Name
	}
	if c.Enabled != nil {
		enabled = *c.Enabled
	}
	return tx.QueryRow(ctx, `UPDATE users SET display_name=$2,enabled=$3,version=nextval('directory_revision')WHERE id=$1 RETURNING version`, u.ID, name, enabled).Scan(&r.Version)
}

// GrantOperatorOffline is a database-owner recovery operation. It grants no token scope or project membership.
func (s *DirectoryStore) GrantOperatorOffline(ctx context.Context, user string) error {
	if !identifier.Valid(user) {
		return domain.ErrInvalid
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	if e = directoryLock(ctx, tx); e != nil {
		return e
	}
	u, e := directoryUser(ctx, tx, user, true)
	if e != nil {
		return e
	}
	if !u.Enabled || u.DeletedAt != nil {
		return domain.ErrRetired
	}
	if u.Operator {
		return tx.Commit(ctx)
	}
	if _, e = tx.Exec(ctx, `INSERT INTO directory_operators(user_id)VALUES($1)`, user); e != nil {
		return e
	}
	r := domain.Receipt{Action: domain.SetOperator, Kind: "user", EntityID: user, ActorID: user}
	r.RequestID, e = identifier.New()
	if e != nil {
		return e
	}
	e = tx.QueryRow(ctx, `UPDATE users SET version=nextval('directory_revision')WHERE id=$1 RETURNING version`, user).Scan(&r.Version)
	if e != nil {
		return e
	}
	_, e = directorySaveReceipt(ctx, tx, r, "", "offline", directoryFingerprint([]byte("offline-operator:"+r.RequestID)))
	if e != nil {
		return e
	}
	return tx.Commit(ctx)
}
