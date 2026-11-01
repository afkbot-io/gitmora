package postgres

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/contextpack/domain"
	forge "workgraph.local/platform/internal/forge/domain"
)

// Browse applies current membership, source capability and availability before identity ordering and LIMIT.
func (s *ContextStore) Browse(ctx context.Context, p access.Principal, q domain.BrowseQuery) (domain.BrowsePage, error) {
	q, err := domain.NormalizeBrowse(q)
	if err != nil {
		return domain.BrowsePage{}, err
	}
	if !domain.BrowseAllowed(p, q.Kind) {
		return domain.BrowsePage{}, access.ErrDenied
	}
	result := domain.BrowsePage{SchemaVersion: 1, Query: q, Items: []domain.BrowseEntry{}}
	err = s.retrieval.authorized(ctx, p, q.ProjectID, q.RepositoryID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, "SELECT transaction_timestamp()").Scan(&result.ReadAt); err != nil {
			return err
		}
		var rows pgx.Rows
		var err error
		after := domain.BrowseCursor{}
		if q.After != nil {
			after = *q.After
		}
		switch q.Kind {
		case "session":
			statement := `SELECT s.repository_id,s.session_id,s.latest_revision,v.digest,x.agent_name,COALESCE(c.segment,1) AS segment,v.created_at,COALESCE(NULLIF(x.title,''),x.agent_name)
 FROM capture_sessions s JOIN repositories r ON r.id=s.repository_id JOIN capture_context_scopes x ON (x.repository_id,x.session_id)=(s.repository_id,s.session_id)
 JOIN capture_revisions v ON (v.repository_id,v.session_id,v.revision)=(s.repository_id,s.session_id,s.latest_revision)
 LEFT JOIN capture_continuations c ON (c.repository_id,c.session_id)=(s.repository_id,s.session_id)
 WHERE r.project_id=$1 AND ($2::text='' OR s.repository_id::text=$2) AND ($3::text='' OR (s.repository_id::text,s.session_id::text)>($3,$4)) ORDER BY s.repository_id::text,s.session_id::text LIMIT $5`
			args := []any{q.ProjectID, q.RepositoryID, after.RepositoryID, after.ID, q.Limit + 1}
			if q.Order == "recent" {
				statement = `SELECT s.repository_id,s.session_id,s.latest_revision,v.digest,x.agent_name,COALESCE(c.segment,1) AS segment,v.created_at,COALESCE(NULLIF(x.title,''),x.agent_name)
 FROM capture_sessions s JOIN repositories r ON r.id=s.repository_id JOIN capture_context_scopes x ON (x.repository_id,x.session_id)=(s.repository_id,s.session_id)
 JOIN capture_revisions v ON (v.repository_id,v.session_id,v.revision)=(s.repository_id,s.session_id,s.latest_revision)
 LEFT JOIN capture_continuations c ON (c.repository_id,c.session_id)=(s.repository_id,s.session_id)
 WHERE r.project_id=$1 AND ($2::text='' OR s.repository_id::text=$2) AND ($3::text='' OR (v.created_at,s.repository_id::text,s.session_id::text)<(NULLIF($3::text,'')::timestamptz,$4,$5)) ORDER BY v.created_at DESC,s.repository_id::text DESC,s.session_id::text DESC LIMIT $6`
				at := ""
				if after.ObservedAt != nil {
					at = after.ObservedAt.UTC().Format(time.RFC3339Nano)
				}
				args = []any{q.ProjectID, q.RepositoryID, at, after.RepositoryID, after.ID, q.Limit + 1}
			}
			rows, err = tx.Query(ctx, statement, args...)
		case "merge_request":
			// This is provider observation discovery; no capture-derived relationship is inferred.
			statement := `SELECT r.id::text,m.iid,o.model,o.digest,o.observed_at FROM repositories r JOIN forge_connections f ON f.repository_id=r.id AND f.enabled AND NOT f.blocked AND f.last_synced_at>now()-interval '10 minutes' JOIN forge_merge_requests m ON m.repository_id=r.id AND m.available JOIN forge_mr_observations o ON (o.repository_id,o.iid,o.digest)=(m.repository_id,m.iid,m.digest) WHERE r.project_id=$1 AND o.model->>'project_id'=r.gitlab_project_id::text AND ($2::text='' OR r.id::text=$2) AND ($3::text='' OR (r.id::text,m.iid)>($3,$4::bigint)) ORDER BY r.id::text,m.iid LIMIT $5`
			args := []any{q.ProjectID, q.RepositoryID, after.RepositoryID, browseIID(after.IID), q.Limit + 1}
			if q.Order == "recent" {
				statement = `SELECT r.id::text,m.iid,o.model,o.digest,o.observed_at FROM repositories r JOIN forge_connections f ON f.repository_id=r.id AND f.enabled AND NOT f.blocked AND f.last_synced_at>now()-interval '10 minutes' JOIN forge_merge_requests m ON m.repository_id=r.id AND m.available JOIN forge_mr_observations o ON (o.repository_id,o.iid,o.digest)=(m.repository_id,m.iid,m.digest) WHERE r.project_id=$1 AND o.model->>'project_id'=r.gitlab_project_id::text AND ($2::text='' OR r.id::text=$2) AND ($3::text='' OR (o.observed_at,r.id::text,m.iid)<(NULLIF($3::text,'')::timestamptz,$4,$5::bigint)) ORDER BY o.observed_at DESC,r.id::text DESC,m.iid DESC LIMIT $6`
				at := ""
				if after.ObservedAt != nil {
					at = after.ObservedAt.UTC().Format(time.RFC3339Nano)
				}
				args = []any{q.ProjectID, q.RepositoryID, at, after.RepositoryID, browseIID(after.IID), q.Limit + 1}
			}
			rows, err = tx.Query(ctx, statement, args...)
		}
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			e := domain.BrowseEntry{Query: domain.Query{ProjectID: q.ProjectID}}
			switch q.Kind {
			case "session":
				if err = rows.Scan(&e.Query.RepositoryID, &e.Query.SessionID, &e.Revision, &e.Digest, &e.Agent, &e.Segment, &e.ObservedAt, &e.Title); err != nil {
					return err
				}
				e.Cursor = domain.BrowseCursor{RepositoryID: e.Query.RepositoryID, ID: e.Query.SessionID}
			case "merge_request":
				var raw []byte
				var model forge.MergeRequest
				if err = rows.Scan(&e.Query.RepositoryID, &e.Query.MR, &raw, &e.Digest, &e.ObservedAt); err != nil {
					return err
				}
				if json.Unmarshal(raw, &model) != nil || model.Digest() != e.Digest || model.IID != e.Query.MR {
					return domain.ErrInvalid
				}
				e.Title = model.Title
				e.State = model.State
				e.Query.ExpectedHead = model.Head
				e.Cursor = domain.BrowseCursor{RepositoryID: e.Query.RepositoryID, IID: strconv.FormatInt(model.IID, 10)}
			}
			if q.Order == "recent" {
				at := e.ObservedAt
				e.Cursor.ObservedAt = &at
			}
			result.Items = append(result.Items, e)
		}
		if err = rows.Err(); err != nil {
			return err
		}
		if len(result.Items) > q.Limit {
			result.Items = result.Items[:q.Limit]
			cursor := result.Items[len(result.Items)-1].Cursor
			result.Next = &cursor
		}
		return nil
	})
	return result, err
}

func browseIID(value string) int64 { n, _ := strconv.ParseInt(value, 10, 64); return n }
