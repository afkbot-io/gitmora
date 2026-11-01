package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/contextpack/domain"
	retrieval "workgraph.local/platform/internal/retrieval/domain"
)

// Resolve matches only trusted provider observations inside the caller's project.
// The submitted URL is never requested, and an identically named repository in another project cannot match.
func (s *ContextStore) Resolve(ctx context.Context, p access.Principal, q domain.ResolveQuery) (domain.Resolved, error) {
	result := domain.Resolved{}
	if !p.Has(access.ReadIntegration) {
		return result, access.ErrDenied
	}
	var err error
	q, err = domain.NormalizeResolve(q)
	if err != nil {
		return result, err
	}
	err = s.retrieval.authorized(ctx, p, q.ProjectID, "", func(tx pgx.Tx) error {
		var repo string
		var iid int64
		err := tx.QueryRow(ctx, `SELECT r.id::text,m.iid FROM repositories r
 JOIN forge_connections c ON c.repository_id=r.id AND c.enabled AND NOT c.blocked AND c.last_synced_at>now()-interval '10 minutes'
 JOIN forge_merge_requests m ON m.repository_id=r.id AND m.available
 JOIN forge_mr_observations o ON (o.repository_id,o.iid,o.digest)=(m.repository_id,m.iid,m.digest)
 WHERE r.project_id=$1 AND o.model->>'project_id'=r.gitlab_project_id::text
 AND ((r.provider='github' AND lower(o.model->>'url')=lower($2)) OR(r.provider='' AND o.model->>'url'=$2))
 ORDER BY r.id,m.iid LIMIT 1`, q.ProjectID, q.URL).Scan(&repo, &iid)
		if errors.Is(err, pgx.ErrNoRows) {
			return retrieval.ErrNotFound
		}
		if err != nil {
			return err
		}
		mr, found, err := readContextMR(ctx, tx, q.ProjectID, repo, iid, "")
		if err != nil {
			return err
		}
		if !found {
			return retrieval.ErrNotFound
		}
		result = domain.Resolved{Query: domain.Query{ProjectID: q.ProjectID, RepositoryID: repo, MR: iid, ExpectedHead: mr.Head}, Title: mr.Title, URL: mr.URL}
		return nil
	})
	return result, err
}
