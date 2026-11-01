package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/identifier"
	"workgraph.local/platform/internal/retrieval/domain"
)

func (s *RetrievalStore) authorized(ctx context.Context, p access.Principal, project, repository string, fn func(pgx.Tx) error) error {
	if !identifier.Valid(p.UserID) || !identifier.Valid(project) || (!p.Has(access.ReadCapture) && !p.Has(access.ReadIntegration)) {
		return access.ErrDenied
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var role string
	err = tx.QueryRow(ctx, `SELECT m.role FROM memberships m JOIN users u ON u.id=m.user_id WHERE m.project_id=$1 AND m.user_id=$2 AND u.enabled FOR SHARE OF m,u`, project, p.UserID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return access.ErrDenied
	}
	if err != nil {
		return err
	}
	if !access.Allows(role, access.ReadCapture) {
		return access.ErrDenied
	}
	if repository != "" {
		var id string
		err = tx.QueryRow(ctx, `SELECT id::text FROM repositories WHERE id=$1 AND project_id=$2 FOR SHARE`, repository, project).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return access.ErrDenied
		}
		if err != nil {
			return err
		}
	}
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// eligibleSources is a security filter, not a post-ranking mask. Unavailable task sources and
// wrong capabilities are excluded before lexical matching, ranking, LIMIT or source text return.
const baseSourceAccess = `j.project_id=$1 AND ((j.source_kind='capture' AND $2 AND catalog.project_id=j.project_id) OR (j.source_kind='task' AND $3 AND wc.enabled AND NOT wc.blocked AND wt.available) OR(j.source_kind='code' AND $3 AND catalog.project_id=j.project_id AND fc.enabled AND NOT fc.blocked AND NOT fc.code_blocked AND fc.code_checked_at>now()-interval '10 minutes' AND cr.provider_available AND cc.confirmed_at IS NOT NULL AND cp.status='stored' AND cf.provider_base_url=catalog.gitlab_url AND cf.provider_project_id=catalog.gitlab_project_id))`
const baseSourceCurrent = `(j.source_kind='code' OR (j.source_kind='capture' AND cs.latest_revision=j.revision) OR (j.source_kind='task' AND wt.digest=j.source_digest))`
const baseSourceJoins = `LEFT JOIN forge_connections fc ON fc.repository_id=j.repository_id
 LEFT JOIN commits cc ON cc.repository_id=j.repository_id AND cc.sha=j.code_sha
 LEFT JOIN forge_code_runs cr ON cr.repository_id=j.repository_id AND cr.sha=j.code_sha
 LEFT JOIN forge_code_paths cp ON cp.repository_id=j.repository_id AND cp.sha=j.code_sha AND cp.path=j.code_path
 LEFT JOIN forge_code_snapshots cf ON cf.repository_id=j.repository_id AND cf.sha=j.code_sha AND cf.path=j.code_path AND cf.digest=j.source_digest
 LEFT JOIN repositories catalog ON catalog.id=j.repository_id
 LEFT JOIN capture_sessions cs ON cs.repository_id=j.repository_id AND cs.session_id=j.session_id
 LEFT JOIN work_tasks wt ON wt.project_id=j.project_id AND wt.task_id=j.task_id AND wt.country_id=j.country_id
 LEFT JOIN work_connections wc ON wc.project_id=j.project_id AND wc.country_id=j.country_id`

const sourceJoins = baseSourceJoins

const confirmedReferenceAccess = `(d.kind<>'code_reference' OR EXISTS(SELECT 1 FROM capture_revision_commits rc JOIN commits c ON c.repository_id=rc.repository_id AND c.sha=rc.sha WHERE rc.repository_id=j.repository_id AND rc.session_id=j.session_id AND rc.revision=j.revision AND c.confirmed_at IS NOT NULL))`

var eligibleSources = `(` + baseSourceAccess + `) AND ($4::text='' OR j.repository_id::text=$4) AND ($5 OR ` + baseSourceCurrent + `)`

// Parameters 1..5 are the shared authorized source scope; matching/ranking use 6..12.
const sourceIdentityProjection = `j.source_kind,j.project_id,j.repository_id,j.session_id,j.revision,j.country_id::text AS source_country_id,j.task_id::text AS source_task_id,j.source_digest,j.code_sha,j.code_path,j.code_blob_id`

// Keep only identity, authorized source metadata and score in the materialized candidates;
// fetch wide source text only for the final bounded selection.
// Identical language alternatives have the same matches and score, but repeat ranking work.
// Materialize the single query row once, including when PostgreSQL chooses a generic plan.
var queryPrefix = `WITH forms AS (
 SELECT plainto_tsquery('simple',$10) AS simple_terms,plainto_tsquery('english',$10) AS english_terms,plainto_tsquery('russian',$10) AS russian_terms
 ), terms AS MATERIALIZED (
 SELECT simple_terms || CASE WHEN english_terms=simple_terms THEN ''::tsquery ELSE english_terms END
 || CASE WHEN russian_terms=simple_terms OR russian_terms=english_terms THEN ''::tsquery ELSE russian_terms END AS query FROM forms
 ),
 allowed AS MATERIALIZED (
 SELECT d.id,` + sourceIdentityProjection + `,
 CASE WHEN j.source_kind='capture' THEN cs.latest_revision=j.revision WHEN j.source_kind='code' THEN true ELSE wt.digest=j.source_digest END AS current,
 CASE WHEN j.source_kind='task' AND wt.digest=j.source_digest THEN wt.observed_at ELSE j.source_observed_at END AS observed_at,
 CASE WHEN $10::text='' THEN CASE d.kind WHEN 'task' THEN 1.0 WHEN 'document' THEN 0.8 WHEN 'session' THEN 0.4 ELSE 0.2 END
 ELSE ts_rank_cd(d.lexical,terms.query,32)+(CASE WHEN position(lower($10) IN lower(d.title_terms))>0 THEN 1.0 ELSE 0.0 END) END::double precision AS score
 FROM retrieval_chunks d JOIN retrieval_jobs j ON j.id=d.job_id ` + sourceJoins + ` CROSS JOIN terms
 WHERE ` + eligibleSources + ` AND j.completed_at IS NOT NULL
 AND ($6::text='' OR (d.country_id::text=$6 AND d.task_id::text=$7))
 AND (COALESCE(cardinality($9::text[]),0)=0 OR d.kind=ANY($9::text[]))
 AND ($12::text='' OR d.id=$12)
 AND ($13::text='' OR j.code_path=$13)
 AND ($14::text='' OR (j.source_kind='capture' AND j.session_id::text=$14))
 AND ($10::text='' OR d.lexical@@terms.query)
 AND ` + confirmedReferenceAccess + `
 AND ($8::text='' OR j.code_sha=$8 OR EXISTS(SELECT 1 FROM capture_revision_commits rc JOIN commits c ON c.repository_id=rc.repository_id AND c.sha=rc.sha WHERE rc.repository_id=j.repository_id AND rc.session_id=j.session_id AND rc.revision=j.revision AND rc.sha=$8 AND c.confirmed_at IS NOT NULL))
 ), selected AS (SELECT * FROM allowed ORDER BY score DESC,observed_at DESC,id LIMIT $11)
 SELECT s.id,d.kind,d.part,d.start_rune,d.end_rune,d.title,d.content,d.content_digest,COALESCE(d.country_id::text,''),COALESCE(d.task_id::text,''),
 s.source_kind,s.project_id::text,COALESCE(s.repository_id::text,''),COALESCE(s.session_id::text,''),COALESCE(s.revision,0),COALESCE(s.source_country_id,''),COALESCE(s.source_task_id,''),s.source_digest,COALESCE(s.code_sha,''),COALESCE(s.code_path,''),COALESCE(s.code_blob_id,''),d.line_start,d.line_end,
 s.current,s.observed_at,d.indexed_at,s.score,
 CASE WHEN s.source_kind='code' THEN ARRAY[s.code_sha] ELSE ARRAY(SELECT c.sha FROM capture_revision_commits rc JOIN commits c ON c.repository_id=rc.repository_id AND c.sha=rc.sha WHERE rc.repository_id=s.repository_id AND rc.session_id=s.session_id AND rc.revision=s.revision AND c.confirmed_at IS NOT NULL ORDER BY c.sha LIMIT 16) END,
 CASE WHEN s.source_kind='code' THEN 1 ELSE (SELECT count(*) FROM capture_revision_commits rc JOIN commits c ON c.repository_id=rc.repository_id AND c.sha=rc.sha WHERE rc.repository_id=s.repository_id AND rc.session_id=s.session_id AND rc.revision=s.revision AND c.confirmed_at IS NOT NULL) END
 FROM selected s JOIN retrieval_chunks d ON d.id=s.id ORDER BY s.score DESC,s.observed_at DESC,s.id`

// readChunks returns only the already-authorized bounded candidate set. Source aliases keep task
// binding fields distinct from source identity; capture citations never acquire a task's digest.
func readChunks(ctx context.Context, tx pgx.Tx, p access.Principal, q domain.Query, id string) ([]domain.Hit, error) {
	rows, err := tx.Query(ctx, queryPrefix, q.ProjectID, p.Has(access.ReadCapture), p.Has(access.ReadIntegration), q.RepositoryID, q.History, q.CountryID, q.TaskID, q.CommitSHA, q.Kinds, q.Terms, q.Limit+1, id, q.FilePath, q.SessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.Hit{}
	for rows.Next() {
		var hit domain.Hit
		dest := []any{&hit.ID, &hit.Kind, &hit.Part, &hit.Start, &hit.End, &hit.Title, &hit.Content, &hit.ContentDigest, &hit.CountryID, &hit.TaskID}
		dest = append(dest, sourceDest(&hit.Source)...)
		dest = append(dest, &hit.LineStart, &hit.LineEnd, &hit.Current, &hit.ObservedAt, &hit.IndexedAt, &hit.Score, &hit.ConfirmedCommits, &hit.ConfirmedCount)
		if err = rows.Scan(dest...); err != nil {
			return nil, err
		}
		if hit.Source.Validate() != nil || domain.Hash([]byte(hit.Content)) != hit.ContentDigest || len([]rune(hit.Content)) != hit.End-hit.Start {
			return nil, errors.New("invalid retrieval projection")
		}
		result = append(result, hit)
	}
	return result, rows.Err()
}

// Search prefilters project/capabilities/source availability, then ranks and bounds source text.
func (s *RetrievalStore) Search(ctx context.Context, p access.Principal, q domain.Query) (domain.Result, error) {
	result := domain.Result{Items: []domain.Hit{}}
	err := s.authorized(ctx, p, q.ProjectID, q.RepositoryID, func(tx pgx.Tx) error {
		items, err := readChunks(ctx, tx, p, q, "")
		if err != nil {
			return err
		}
		if len(items) > q.Limit {
			result.HasMore = true
			items = items[:q.Limit]
		}
		result.Items = items
		return tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE j.last_error_code IS NOT NULL) FROM retrieval_jobs j `+sourceJoins+` WHERE `+eligibleSources+` AND j.completed_at IS NULL`, q.ProjectID, p.Has(access.ReadCapture), p.Has(access.ReadIntegration), q.RepositoryID, q.History).Scan(&result.PendingSources, &result.FailedSources)
	})
	return result, err
}

// Get rechecks capability, membership and live source availability for every exact citation.
func (s *RetrievalStore) Get(ctx context.Context, p access.Principal, id string) (domain.Hit, error) {
	var result domain.Hit
	if !domain.Digest(id) {
		return result, domain.ErrInvalid
	}
	var project string
	err := s.pool.QueryRow(ctx, `SELECT j.project_id::text FROM retrieval_chunks d JOIN retrieval_jobs j ON j.id=d.job_id JOIN memberships m ON m.project_id=j.project_id AND m.user_id=$2 JOIN users u ON u.id=m.user_id AND u.enabled WHERE d.id=$1`, id, p.UserID).Scan(&project)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, domain.ErrNotFound
	}
	if err != nil {
		return result, err
	}
	err = s.authorized(ctx, p, project, "", func(tx pgx.Tx) error {
		items, err := readChunks(ctx, tx, p, domain.Query{ProjectID: project, History: true, Limit: 1}, id)
		if err != nil {
			return err
		}
		if len(items) == 0 {
			return domain.ErrNotFound
		}
		result = items[0]
		return nil
	})
	return result, err
}
