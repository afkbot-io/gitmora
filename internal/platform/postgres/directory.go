package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/directory/domain"
	"workgraph.local/platform/internal/identifier"
)

// DirectoryStore owns local user/catalog metadata. Installation authority never changes content-store membership rules.
type DirectoryStore struct{ pool *pgxpool.Pool }

func NewDirectoryStore(pool *pgxpool.Pool) *DirectoryStore { return &DirectoryStore{pool} }

func directoryRead[T any](ctx context.Context, pool *pgxpool.Pool, p access.Principal, read bool, fn func(pgx.Tx, directoryActor) (T, error)) (T, error) {
	var zero T
	tx, e := pool.Begin(ctx)
	if e != nil {
		return zero, e
	}
	defer tx.Rollback(ctx)
	a, e := directoryAuthenticate(ctx, tx, p)
	if e != nil {
		return zero, e
	}
	if read && !a.principal.Has(access.ReadDirectory) {
		return zero, access.ErrDenied
	}
	v, e := fn(tx, a)
	if e != nil {
		return zero, e
	}
	return v, tx.Commit(ctx)
}
func directoryPage[T any](items []T, limit int, key func(T) string) domain.Page[T] {
	out := domain.Page[T]{Items: items}
	if len(items) > limit {
		out.NextID = key(items[limit-1])
		out.Items = items[:limit]
	}
	return out
}
func directoryNoRows(e error) error {
	if errors.Is(e, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	return e
}

const directoryUserColumns = `u.id::text,u.display_name,COALESCE((SELECT username FROM user_handles h WHERE h.user_id=u.id),''),u.enabled,EXISTS(SELECT 1 FROM directory_operators o WHERE o.user_id=u.id),u.version,u.created_at,u.deleted_at`

func directoryUser(ctx context.Context, tx pgx.Tx, id string, lock bool) (domain.User, error) {
	var u domain.User
	suffix := ""
	if lock {
		suffix = " FOR UPDATE OF u"
	}
	e := tx.QueryRow(ctx, `SELECT `+directoryUserColumns+` FROM users u WHERE u.id=$1`+suffix, id).Scan(&u.ID, &u.DisplayName, &u.Username, &u.Enabled, &u.Operator, &u.Version, &u.CreatedAt, &u.DeletedAt)
	return u, directoryNoRows(e)
}

// Me is available to any current credential and returns no secret hash or recoverable secret.
func (s *DirectoryStore) Me(ctx context.Context, p access.Principal) (domain.Me, error) {
	return directoryRead(ctx, s.pool, p, false, func(tx pgx.Tx, a directoryActor) (domain.Me, error) {
		u, e := directoryUser(ctx, tx, p.UserID, false)
		return domain.Me{User: u, TokenID: p.TokenID, Scopes: a.principal.Scopes, ExpiresAt: a.expires}, e
	})
}

// Projects applies membership/global metadata authority before pagination.
func (s *DirectoryStore) Projects(ctx context.Context, p access.Principal, c domain.Cursor) (domain.Page[domain.Project], error) {
	c, e := c.Normalize()
	if e != nil {
		return domain.Page[domain.Project]{}, e
	}
	return directoryRead(ctx, s.pool, p, true, func(tx pgx.Tx, a directoryActor) (domain.Page[domain.Project], error) {
		rows, e := tx.Query(ctx, `SELECT p.id::text,p.name,p.version,COALESCE(m.role,''),p.created_at FROM projects p LEFT JOIN memberships m ON m.project_id=p.id AND m.user_id=$1 WHERE($2 OR m.user_id IS NOT NULL)AND($3='' OR p.id>NULLIF($3,'')::uuid)ORDER BY p.id LIMIT $4`, p.UserID, a.platform(), c.AfterID, c.Limit+1)
		if e != nil {
			return domain.Page[domain.Project]{}, e
		}
		defer rows.Close()
		items := []domain.Project{}
		for rows.Next() {
			var v domain.Project
			if e = rows.Scan(&v.ID, &v.Name, &v.Version, &v.Role, &v.CreatedAt); e != nil {
				return domain.Page[domain.Project]{}, e
			}
			items = append(items, v)
		}
		return directoryPage(items, c.Limit, func(v domain.Project) string { return v.ID }), rows.Err()
	})
}
func (s *DirectoryStore) Project(ctx context.Context, p access.Principal, id string) (domain.Project, error) {
	return directoryRead(ctx, s.pool, p, true, func(tx pgx.Tx, a directoryActor) (domain.Project, error) {
		if e := directoryProjectAuthority(ctx, tx, a, id, false); e != nil {
			return domain.Project{}, e
		}
		var v domain.Project
		e := tx.QueryRow(ctx, `SELECT p.id::text,p.name,p.version,COALESCE(m.role,''),p.created_at FROM projects p LEFT JOIN memberships m ON m.project_id=p.id AND m.user_id=$2 WHERE p.id=$1`, id, p.UserID).Scan(&v.ID, &v.Name, &v.Version, &v.Role, &v.CreatedAt)
		return v, directoryNoRows(e)
	})
}

const directoryRepoColumns = `r.id::text,r.project_id::text,r.name,r.gitlab_url,r.gitlab_project_id,r.version,r.created_at,r.provider`

func exposeRepositoryProvider(v domain.Repository) domain.Repository {
	if v.Provider == "github" {
		v.ProviderURL, v.ProviderRepositoryID = v.GitLabURL, v.GitLabProjectID
		v.GitLabURL, v.GitLabProjectID = "", 0
	}
	return v
}

// AuthorizeRepositoryCreate shares the exact current catalog authority with repository writes.
func (s *DirectoryStore) AuthorizeRepositoryCreate(ctx context.Context, p access.Principal, project string) error {
	_, err := directoryRead(ctx, s.pool, p, true, func(tx pgx.Tx, a directoryActor) (bool, error) {
		return true, directoryProjectAuthority(ctx, tx, a, project, true)
	})
	return err
}

func (s *DirectoryStore) Repositories(ctx context.Context, p access.Principal, project string, c domain.Cursor) (domain.Page[domain.Repository], error) {
	c, e := c.Normalize()
	if e != nil {
		return domain.Page[domain.Repository]{}, e
	}
	return directoryRead(ctx, s.pool, p, true, func(tx pgx.Tx, a directoryActor) (domain.Page[domain.Repository], error) {
		if e := directoryProjectAuthority(ctx, tx, a, project, false); e != nil {
			return domain.Page[domain.Repository]{}, e
		}
		rows, e := tx.Query(ctx, `SELECT `+directoryRepoColumns+` FROM repositories r WHERE r.project_id=$1 AND($2='' OR r.id>NULLIF($2,'')::uuid)ORDER BY r.id LIMIT $3`, project, c.AfterID, c.Limit+1)
		if e != nil {
			return domain.Page[domain.Repository]{}, e
		}
		defer rows.Close()
		items := []domain.Repository{}
		for rows.Next() {
			var v domain.Repository
			if e = rows.Scan(&v.ID, &v.ProjectID, &v.Name, &v.GitLabURL, &v.GitLabProjectID, &v.Version, &v.CreatedAt, &v.Provider); e != nil {
				return domain.Page[domain.Repository]{}, e
			}
			items = append(items, exposeRepositoryProvider(v))
		}
		return directoryPage(items, c.Limit, func(v domain.Repository) string { return v.ID }), rows.Err()
	})
}
func (s *DirectoryStore) Repository(ctx context.Context, p access.Principal, id string) (domain.Repository, error) {
	if !identifier.Valid(id) {
		return domain.Repository{}, domain.ErrInvalid
	}
	return directoryRead(ctx, s.pool, p, true, func(tx pgx.Tx, a directoryActor) (domain.Repository, error) {
		var v domain.Repository
		e := tx.QueryRow(ctx, `SELECT `+directoryRepoColumns+` FROM repositories r WHERE r.id=$1 AND($2 OR EXISTS(SELECT 1 FROM memberships m WHERE m.project_id=r.project_id AND m.user_id=$3))`, id, a.platform(), p.UserID).Scan(&v.ID, &v.ProjectID, &v.Name, &v.GitLabURL, &v.GitLabProjectID, &v.Version, &v.CreatedAt, &v.Provider)
		if e != nil {
			return v, directoryNoRows(e)
		}
		if e = directoryProjectAuthority(ctx, tx, a, v.ProjectID, false); e != nil {
			return domain.Repository{}, e
		}
		return exposeRepositoryProvider(v), nil
	})
}
func (s *DirectoryStore) Members(ctx context.Context, p access.Principal, project string, c domain.Cursor) (domain.Page[domain.Member], error) {
	c, e := c.Normalize()
	if e != nil {
		return domain.Page[domain.Member]{}, e
	}
	return directoryRead(ctx, s.pool, p, true, func(tx pgx.Tx, a directoryActor) (domain.Page[domain.Member], error) {
		if e := directoryProjectAuthority(ctx, tx, a, project, false); e != nil {
			return domain.Page[domain.Member]{}, e
		}
		rows, e := tx.Query(ctx, `SELECT m.user_id::text,u.display_name,COALESCE((SELECT username FROM user_handles h WHERE h.user_id=u.id),''),u.enabled,m.role,m.version FROM memberships m JOIN users u ON u.id=m.user_id WHERE m.project_id=$1 AND($2='' OR m.user_id>NULLIF($2,'')::uuid)ORDER BY m.user_id LIMIT $3`, project, c.AfterID, c.Limit+1)
		if e != nil {
			return domain.Page[domain.Member]{}, e
		}
		defer rows.Close()
		items := []domain.Member{}
		for rows.Next() {
			var v domain.Member
			if e = rows.Scan(&v.UserID, &v.DisplayName, &v.Username, &v.Enabled, &v.Role, &v.Version); e != nil {
				return domain.Page[domain.Member]{}, e
			}
			items = append(items, v)
		}
		return directoryPage(items, c.Limit, func(v domain.Member) string { return v.UserID }), rows.Err()
	})
}
func (s *DirectoryStore) Users(ctx context.Context, p access.Principal, c domain.Cursor) (domain.Page[domain.User], error) {
	c, e := c.Normalize()
	if e != nil {
		return domain.Page[domain.User]{}, e
	}
	return directoryRead(ctx, s.pool, p, true, func(tx pgx.Tx, a directoryActor) (domain.Page[domain.User], error) {
		if !a.platform() {
			return domain.Page[domain.User]{}, access.ErrDenied
		}
		rows, e := tx.Query(ctx, `SELECT `+directoryUserColumns+` FROM users u WHERE($1='' OR u.id>NULLIF($1,'')::uuid)ORDER BY u.id LIMIT $2`, c.AfterID, c.Limit+1)
		if e != nil {
			return domain.Page[domain.User]{}, e
		}
		defer rows.Close()
		items := []domain.User{}
		for rows.Next() {
			var v domain.User
			if e = rows.Scan(&v.ID, &v.DisplayName, &v.Username, &v.Enabled, &v.Operator, &v.Version, &v.CreatedAt, &v.DeletedAt); e != nil {
				return domain.Page[domain.User]{}, e
			}
			items = append(items, v)
		}
		return directoryPage(items, c.Limit, func(v domain.User) string { return v.ID }), rows.Err()
	})
}
func (s *DirectoryStore) User(ctx context.Context, p access.Principal, id string) (domain.User, error) {
	if !identifier.Valid(id) {
		return domain.User{}, domain.ErrInvalid
	}
	return directoryRead(ctx, s.pool, p, true, func(tx pgx.Tx, a directoryActor) (domain.User, error) {
		if !a.platform() {
			return domain.User{}, access.ErrDenied
		}
		return directoryUser(ctx, tx, id, false)
	})
}

// Events exposes content-free metadata receipts with a monotonic cursor, surviving catalog deletion.
func (s *DirectoryStore) Events(ctx context.Context, p access.Principal, q domain.EventQuery) (domain.EventPage, error) {
	if q.ProjectID != "" && !identifier.Valid(q.ProjectID) || q.AfterSequence < 0 || q.AfterSequence > domain.MaxVersion || q.Limit < 0 || q.Limit > 100 {
		return domain.EventPage{}, domain.ErrInvalid
	}
	if q.Limit == 0 {
		q.Limit = 30
	}
	return directoryRead(ctx, s.pool, p, true, func(tx pgx.Tx, a directoryActor) (domain.EventPage, error) {
		if q.ProjectID == "" {
			if !a.platform() {
				return domain.EventPage{}, access.ErrDenied
			}
		} else if !a.platform() {
			if e := directoryProjectAuthority(ctx, tx, a, q.ProjectID, true); e != nil {
				return domain.EventPage{}, e
			}
		}
		rows, e := tx.Query(ctx, `SELECT receipt,origin,COALESCE(token_id::text,'') FROM directory_events WHERE($1='' OR project_id=NULLIF($1,'')::uuid)AND seq>$2 ORDER BY seq LIMIT $3`, q.ProjectID, q.AfterSequence, q.Limit+1)
		if e != nil {
			return domain.EventPage{}, e
		}
		defer rows.Close()
		out := domain.EventPage{Items: []domain.Event{}}
		for rows.Next() {
			var v domain.Event
			var raw []byte
			if e = rows.Scan(&raw, &v.Origin, &v.TokenID); e != nil {
				return out, e
			}
			if e = decodeDirectoryReceipt(raw, &v.Receipt); e != nil {
				return out, e
			}
			out.Items = append(out.Items, v)
		}
		if len(out.Items) > q.Limit {
			out.Items = out.Items[:q.Limit]
			out.NextSequence = out.Items[q.Limit-1].Sequence
		}
		return out, rows.Err()
	})
}
