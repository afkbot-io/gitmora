package postgres

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/jackc/pgx/v5"
	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/contextpack/domain"
	forge "workgraph.local/platform/internal/forge/domain"
	work "workgraph.local/platform/internal/work/domain"
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
			rows, err = tx.Query(ctx, `SELECT s.repository_id::text,s.session_id::text,s.latest_revision,v.digest,x.agent_name,COALESCE(c.segment,1),v.created_at
 FROM capture_sessions s JOIN repositories r ON r.id=s.repository_id JOIN capture_context_scopes x ON (x.repository_id,x.session_id)=(s.repository_id,s.session_id)
 JOIN capture_revisions v ON (v.repository_id,v.session_id,v.revision)=(s.repository_id,s.session_id,s.latest_revision)
 LEFT JOIN capture_continuations c ON (c.repository_id,c.session_id)=(s.repository_id,s.session_id)
 WHERE r.project_id=$1 AND ($2::text='' OR s.repository_id::text=$2) AND ($3::text='' OR (s.repository_id::text,s.session_id::text)>($3,$4)) ORDER BY s.repository_id::text,s.session_id::text LIMIT $5`, q.ProjectID, q.RepositoryID, after.RepositoryID, after.ID, q.Limit+1)
		case "task":
			rows, err = tx.Query(ctx, `SELECT t.task_id::text,t.country_id::text,o.model,o.digest,t.observed_at FROM work_tasks t JOIN work_connections c ON (c.project_id,c.country_id)=(t.project_id,t.country_id) AND c.enabled AND NOT c.blocked JOIN work_task_observations o ON (o.project_id,o.task_id,o.digest)=(t.project_id,t.task_id,t.digest) WHERE t.project_id=$1 AND t.available AND ($2::text='' OR t.task_id::text>$2) ORDER BY t.task_id::text LIMIT $3`, q.ProjectID, after.ID, q.Limit+1)
		case "merge_request":
			// This is provider observation discovery; no capture-derived relationship is inferred.
			rows, err = tx.Query(ctx, `SELECT r.id::text,m.iid,o.model,o.digest,o.observed_at FROM repositories r JOIN forge_connections f ON f.repository_id=r.id AND f.enabled AND NOT f.blocked AND f.last_synced_at>now()-interval '10 minutes' JOIN forge_merge_requests m ON m.repository_id=r.id AND m.available JOIN forge_mr_observations o ON (o.repository_id,o.iid,o.digest)=(m.repository_id,m.iid,m.digest) WHERE r.project_id=$1 AND o.model->>'project_id'=r.gitlab_project_id::text AND ($2::text='' OR r.id::text=$2) AND ($3::text='' OR (r.id::text,m.iid)>($3,$4::bigint)) ORDER BY r.id::text,m.iid LIMIT $5`, q.ProjectID, q.RepositoryID, after.RepositoryID, browseIID(after.IID), q.Limit+1)
		}
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			e := domain.BrowseEntry{Query: domain.Query{ProjectID: q.ProjectID}}
			switch q.Kind {
			case "session":
				if err = rows.Scan(&e.Query.RepositoryID, &e.Query.SessionID, &e.Revision, &e.Digest, &e.Agent, &e.Segment, &e.ObservedAt); err != nil {
					return err
				}
				e.Title = e.Agent
				e.Cursor = domain.BrowseCursor{RepositoryID: e.Query.RepositoryID, ID: e.Query.SessionID}
			case "task":
				var raw []byte
				var model work.Task
				if err = rows.Scan(&e.Query.TaskID, &e.Query.CountryID, &raw, &e.Digest, &e.ObservedAt); err != nil {
					return err
				}
				if json.Unmarshal(raw, &model) != nil {
					return domain.ErrInvalid
				}
				model, err = work.NormalizeTask(model)
				if err != nil || model.Digest() != e.Digest || model.ID != e.Query.TaskID || model.CountryID != e.Query.CountryID {
					return domain.ErrInvalid
				}
				e.Title = model.Title
				e.Number = strconv.FormatInt(model.Number, 10)
				e.State = model.Status
				e.Cursor = domain.BrowseCursor{ID: e.Query.TaskID}
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
