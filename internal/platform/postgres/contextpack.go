package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"workgraph.local/platform/internal/access"
	capture "workgraph.local/platform/internal/capture/domain"
	"workgraph.local/platform/internal/contextpack/domain"
	forge "workgraph.local/platform/internal/forge/domain"
	"workgraph.local/platform/internal/redact"
	work "workgraph.local/platform/internal/work/domain"
)

// ContextStore is a read-only cross-context projection adapter. It cannot mutate source aggregates or call providers.
type ContextStore struct{ retrieval *RetrievalStore }

// NewContextStore shares the pool and existing retrieval authorization boundary.
func NewContextStore(pool *pgxpool.Pool) *ContextStore { return &ContextStore{NewRetrievalStore(pool)} }

// Read applies membership and independent capabilities before loading any source body or association.
func (s *ContextStore) Read(ctx context.Context, p access.Principal, q domain.Query) (domain.Pack, error) {
	result := domain.Pack{SchemaVersion: 1, Query: q, TaskState: "not_requested", Captures: domain.CapturePage{Items: []domain.Capture{}, Omitted: !p.Has(access.ReadCapture)}, MergeRequests: domain.MRPage{Items: []domain.MR{}, Omitted: !p.Has(access.ReadIntegration) || (q.MR == 0 && q.CommitSHA == "" && !p.Has(access.ReadCapture))}, Evidence: []domain.Evidence{}}
	err := s.retrieval.authorized(ctx, p, q.ProjectID, q.RepositoryID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, "SELECT transaction_timestamp()").Scan(&result.ReadAt); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE j.last_error_code IS NOT NULL) FROM retrieval_jobs j `+sourceJoins+` WHERE `+eligibleSources+` AND j.completed_at IS NULL`, q.ProjectID, p.Has(access.ReadCapture), p.Has(access.ReadIntegration), "", false).Scan(&result.PendingSources, &result.FailedSources); err != nil {
			return err
		}
		if q.TaskID != "" {
			result.TaskState = "not_permitted"
			if p.Has(access.ReadIntegration) {
				if err := readContextTask(ctx, tx, q, &result); err != nil {
					return err
				}
			}
		}
		captureQuery := q
		if q.MR > 0 && p.Has(access.ReadIntegration) {
			mr, found, err := readContextMR(ctx, tx, q.ProjectID, q.RepositoryID, q.MR, q.ExpectedHead)
			if err != nil {
				return err
			}
			if found {
				mr.Association = "explicit_provider_mr"
				result.MergeRequests.Items = append(result.MergeRequests.Items, mr)
				captureQuery.RepositoryID = mr.SourceRepositoryID
				captureQuery.CommitSHA = mr.Head
			}
		}
		if p.Has(access.ReadCapture) && (q.MR == 0 || captureQuery.RepositoryID != "" && captureQuery.CommitSHA != "") {
			if err := readContextCaptures(ctx, tx, captureQuery, &result.Captures); err != nil {
				return err
			}
		}
		if q.MR == 0 && p.Has(access.ReadIntegration) && (p.Has(access.ReadCapture) || q.CommitSHA != "") {
			refs, next, err := contextMRRefs(ctx, tx, q)
			if err != nil {
				return err
			}
			result.MergeRequests.Next = next
			result.MergeRequests.HasMore = next != nil
			for _, ref := range refs {
				mr, found, err := readContextMR(ctx, tx, q.ProjectID, ref.repo, ref.iid, "")
				if err != nil {
					return err
				}
				if found {
					mr.Association = "captured_stream_commits"
					if q.TaskID != "" {
						mr.Association = "capture_task_binding_and_confirmed_commit"
					}
					if q.CommitSHA != "" {
						mr.Association = "confirmed_commit_membership"
					}
					result.MergeRequests.Items = append(result.MergeRequests.Items, mr)
				}
			}
		}
		return nil
	})
	return result, err
}

func readContextTask(ctx context.Context, tx pgx.Tx, q domain.Query, result *domain.Pack) error {
	result.TaskState = "unavailable"
	var raw []byte
	var digest string
	var observed time.Time
	err := tx.QueryRow(ctx, `SELECT o.model,o.digest,t.observed_at FROM work_tasks t JOIN work_task_observations o ON (o.project_id,o.task_id,o.digest)=(t.project_id,t.task_id,t.digest) JOIN work_connections c ON c.project_id=t.project_id AND c.country_id=t.country_id WHERE t.project_id=$1 AND t.country_id::text=$2 AND t.task_id::text=$3 AND t.available AND c.enabled AND NOT c.blocked`, q.ProjectID, q.CountryID, q.TaskID).Scan(&raw, &digest, &observed)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	var task work.Task
	if json.Unmarshal(raw, &task) != nil {
		return domain.ErrInvalid
	}
	task, err = work.NormalizeTask(task)
	if err != nil || task.Digest() != digest || task.CountryID != q.CountryID || task.ID != q.TaskID {
		return domain.ErrInvalid
	}
	compact := domain.CompactTask(task, digest, q.ProjectID, observed)
	result.Task = &compact
	result.TaskState = "available_cached"
	return nil
}

// contextTree proves every traversed edge from an actual root, with exact parent bytes,
// immutable source scope (including nanoseconds) and originator. Forged declarations never expand a stream.
const contextTree = `WITH RECURSIVE scoped AS MATERIALIZED (
 SELECT s.repository_id,s.session_id,s.originator_id,s.latest_revision,x.started_at_text,x.agent_name,x.country_id,x.task_id,
 COALESCE(c.root_session_id,s.session_id) AS root_id,COALESCE(c.segment,1) AS segment,c.previous_session_id,c.previous_revision,c.previous_digest,c.reason
 FROM capture_sessions s JOIN repositories r ON r.id=s.repository_id JOIN capture_context_scopes x ON x.repository_id=s.repository_id AND x.session_id=s.session_id
 LEFT JOIN capture_continuations c ON c.repository_id=s.repository_id AND c.session_id=s.session_id
 WHERE r.project_id=$1 AND ($2::text='' OR s.repository_id::text=$2)
), seeds AS MATERIALIZED (
 SELECT s.* FROM scoped s WHERE ($3::text<>'' AND s.country_id::text=$3 AND s.task_id::text=$4)
 OR($5::text<>'' AND s.session_id::text=$5)
 OR($6::text<>'' AND EXISTS(SELECT 1 FROM capture_revision_commits rc JOIN commits c ON c.repository_id=rc.repository_id AND c.sha=rc.sha AND c.confirmed_at IS NOT NULL WHERE rc.repository_id=s.repository_id AND rc.session_id=s.session_id AND rc.sha=$6))
), tree AS (
 SELECT r.*,1 AS depth FROM scoped r WHERE r.previous_session_id IS NULL AND EXISTS(SELECT 1 FROM seeds s WHERE s.repository_id=r.repository_id AND s.root_id=r.session_id AND s.originator_id=r.originator_id AND s.started_at_text=r.started_at_text AND s.agent_name=r.agent_name AND s.country_id IS NOT DISTINCT FROM r.country_id AND s.task_id IS NOT DISTINCT FROM r.task_id)
 UNION ALL
 SELECT c.*,p.depth+1 FROM tree p JOIN scoped c ON c.repository_id=p.repository_id AND c.previous_session_id=p.session_id AND c.root_id=p.root_id AND c.segment=p.segment+1 AND c.originator_id=p.originator_id AND c.started_at_text=p.started_at_text AND c.agent_name=p.agent_name AND c.country_id IS NOT DISTINCT FROM p.country_id AND c.task_id IS NOT DISTINCT FROM p.task_id
 JOIN capture_revisions pr ON pr.repository_id=p.repository_id AND pr.session_id=p.session_id AND pr.revision=c.previous_revision AND pr.digest=c.previous_digest WHERE p.depth<256
), wanted AS (
 SELECT repository_id,session_id FROM seeds UNION
 SELECT t.repository_id,t.session_id FROM tree t WHERE EXISTS(SELECT 1 FROM tree member JOIN seeds s ON s.repository_id=member.repository_id AND s.session_id=member.session_id WHERE member.repository_id=t.repository_id AND member.root_id=t.root_id)
) `

func contextArgs(q domain.Query) []any {
	return []any{q.ProjectID, q.RepositoryID, q.CountryID, q.TaskID, q.SessionID, q.CommitSHA}
}
func readContextCaptures(ctx context.Context, tx pgx.Tx, q domain.Query, page *domain.CapturePage) error {
	after := domain.Cursor{}
	if q.After != nil {
		after = *q.After
	}
	args := append(contextArgs(q), after.RepositoryID, after.Segment, after.SessionID, q.Limit+1)
	rows, err := tx.Query(ctx, contextTree+`
 SELECT s.repository_id::text,s.session_id::text,s.latest_revision,r.digest,s.root_id::text,s.segment,s.agent_name,s.previous_session_id::text,COALESCE(s.previous_revision,0),COALESCE(s.previous_digest,''),COALESCE(s.reason,''),
 EXISTS(SELECT 1 FROM scoped p JOIN capture_revisions pr ON pr.repository_id=p.repository_id AND pr.session_id=p.session_id AND pr.revision=s.previous_revision AND pr.digest=s.previous_digest WHERE p.repository_id=s.repository_id AND p.session_id=s.previous_session_id AND p.root_id=s.root_id AND s.segment=p.segment+1 AND p.originator_id=s.originator_id AND p.started_at_text=s.started_at_text AND p.agent_name=s.agent_name AND p.country_id IS NOT DISTINCT FROM s.country_id AND p.task_id IS NOT DISTINCT FROM s.task_id),
 EXISTS(SELECT 1 FROM tree t WHERE t.repository_id=s.repository_id AND t.session_id=s.session_id),
 ARRAY(SELECT c.sha FROM capture_revision_commits rc JOIN commits c ON c.repository_id=rc.repository_id AND c.sha=rc.sha AND c.confirmed_at IS NOT NULL WHERE rc.repository_id=s.repository_id AND rc.session_id=s.session_id AND rc.revision=s.latest_revision ORDER BY c.sha LIMIT 16),
 (SELECT count(*) FROM capture_revision_commits rc JOIN commits c ON c.repository_id=rc.repository_id AND c.sha=rc.sha AND c.confirmed_at IS NOT NULL WHERE rc.repository_id=s.repository_id AND rc.session_id=s.session_id AND rc.revision=s.latest_revision)
 FROM wanted w JOIN scoped s ON s.repository_id=w.repository_id AND s.session_id=w.session_id JOIN capture_revisions r ON r.repository_id=s.repository_id AND r.session_id=s.session_id AND r.revision=s.latest_revision
 WHERE ($7::text='' OR (s.repository_id::text,s.segment,s.session_id::text)>($7,$8,$9)) ORDER BY s.repository_id::text,s.segment,s.session_id::text LIMIT $10`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var c domain.Capture
		var parent *string
		var parentRevision int64
		var parentDigest, reason string
		c.Source.Kind = "capture"
		c.Source.ProjectID = q.ProjectID
		if err := rows.Scan(&c.Source.RepositoryID, &c.Source.SessionID, &c.Source.Revision, &c.Source.Digest, &c.RootSessionID, &c.Segment, &c.Agent, &parent, &parentRevision, &parentDigest, &reason, &c.ParentReferenceVerified, &c.ChainVerified, &c.ConfirmedCommits, &c.ConfirmedCount); err != nil {
			return err
		}
		c.VerificationLimited = c.Segment > 256
		if parent != nil {
			c.Continuation = &capture.Continuation{RootSessionID: c.RootSessionID, PreviousSessionID: *parent, PreviousRevision: parentRevision, PreviousDigest: parentDigest, Segment: c.Segment, Reason: reason}
		}
		page.Items = append(page.Items, c)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(page.Items) > q.Limit {
		page.Items = page.Items[:q.Limit]
		last := page.Items[len(page.Items)-1]
		page.Next = &domain.Cursor{RepositoryID: last.Source.RepositoryID, Segment: last.Segment, SessionID: last.Source.SessionID}
	}
	return nil
}

type mrRef struct {
	repo string
	iid  int64
}

func contextMRRefs(ctx context.Context, tx pgx.Tx, q domain.Query) ([]mrRef, *domain.MRCursor, error) {
	refs := []mrRef{}
	after := domain.MRCursor{}
	if q.MRAfter != nil {
		after = *q.MRAfter
	}
	if q.TaskID != "" {
		rows, err := tx.Query(ctx, workMRMatches+` AND ($7::text='' OR (m.target_repository_id::text,m.iid)>($7,$8)) AND ($9::text='' OR m.source_repository_id::text=$9)`+strings.Replace(workMRMatchOrder, "LIMIT 100", "LIMIT 5", 1), q.ProjectID, q.CountryID, q.TaskID, "", int64(0), "", after.RepositoryID, after.IID, q.RepositoryID)
		if err != nil {
			return nil, nil, err
		}
		defer rows.Close()
		for rows.Next() {
			m, err := readLinkMatch(rows)
			if err != nil {
				return nil, nil, err
			}
			refs = append(refs, mrRef{m.Link.RepositoryID, m.Link.IID})
		}
		if err := rows.Err(); err != nil {
			return nil, nil, err
		}
	} else {
		prefix := contextTree + `, bound AS (
 SELECT DISTINCT r.id AS source_repository_id,r.gitlab_url,r.gitlab_project_id,r.provider,rc.sha FROM wanted w JOIN repositories r ON r.id=w.repository_id JOIN capture_revision_commits rc ON rc.repository_id=w.repository_id AND rc.session_id=w.session_id JOIN commits c ON c.repository_id=rc.repository_id AND c.sha=rc.sha AND c.confirmed_at IS NOT NULL
 )`
		args := contextArgs(q)
		cursorFilter := ` AND ($7::text='' OR (tr.id::text,o.iid)>($7,$8))`
		if q.CommitSHA != "" {
			prefix = `WITH bound AS (SELECT r.id AS source_repository_id,r.gitlab_url,r.gitlab_project_id,r.provider,c.sha FROM repositories r JOIN commits c ON c.repository_id=r.id AND c.confirmed_at IS NOT NULL WHERE r.project_id=$1 AND r.id::text=$2 AND c.sha=$3)`
			args = []any{q.ProjectID, q.RepositoryID, q.CommitSHA}
			cursorFilter = ` AND ($4::text='' OR (tr.id::text,o.iid)>($4,$5))`
		}
		args = append(args, after.RepositoryID, after.IID)
		rows, err := tx.Query(ctx, prefix+` SELECT DISTINCT tr.id::text,o.iid FROM bound b JOIN forge_connections sc ON sc.repository_id=b.source_repository_id AND sc.enabled AND NOT sc.blocked AND sc.last_synced_at>now()-interval '10 minutes'
 JOIN repositories tr ON tr.project_id=$1 AND tr.provider=b.provider AND (tr.provider='github' OR tr.gitlab_url=b.gitlab_url) JOIN forge_connections tc ON tc.repository_id=tr.id AND tc.enabled AND NOT tc.blocked AND tc.last_synced_at>now()-interval '10 minutes'
 JOIN forge_merge_requests m ON m.repository_id=tr.id AND m.available JOIN forge_mr_observations o ON (o.repository_id,o.iid,o.digest)=(m.repository_id,m.iid,m.digest) AND o.model->>'project_id'=tr.gitlab_project_id::text
 JOIN forge_mr_codes k ON (k.repository_id,k.iid,k.code_key)=(o.repository_id,o.iid,o.code_key) AND k.complete
 WHERE (EXISTS(SELECT 1 FROM forge_mr_commits mc WHERE (mc.repository_id,mc.iid,mc.code_key)=(o.repository_id,o.iid,o.code_key) AND mc.source_project_id=b.gitlab_project_id AND o.model->>'source_project_id'=b.gitlab_project_id::text AND mc.sha=b.sha)
 OR(b.source_repository_id=tr.id AND o.model->>'state'='merged' AND b.sha IN(o.model->>'merge',o.model->>'squash')))`+cursorFilter+` ORDER BY tr.id::text,o.iid LIMIT 5`, args...)
		if err != nil {
			return nil, nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var r mrRef
			if err := rows.Scan(&r.repo, &r.iid); err != nil {
				return nil, nil, err
			}
			refs = append(refs, r)
		}
		if err := rows.Err(); err != nil {
			return nil, nil, err
		}
	}
	var next *domain.MRCursor
	if len(refs) > 4 {
		refs = refs[:4]
		last := refs[len(refs)-1]
		next = &domain.MRCursor{RepositoryID: last.repo, IID: last.iid}
	}
	return refs, next, nil
}

func readContextMR(ctx context.Context, tx pgx.Tx, project, repo string, iid int64, expected string) (domain.MR, bool, error) {
	out := domain.MR{RepositoryID: repo, IID: iid, ResultSHAs: []string{}, Pipelines: []domain.Pipeline{}, Deployments: []forge.Deployment{}}
	var raw []byte
	var base string
	err := tx.QueryRow(ctx, `SELECT o.model,o.digest,o.code_key,o.observed_at,k.complete,r.gitlab_url FROM repositories r JOIN forge_connections f ON f.repository_id=r.id AND f.enabled AND NOT f.blocked AND f.last_synced_at>now()-interval '10 minutes' JOIN forge_merge_requests m ON m.repository_id=r.id AND m.available JOIN forge_mr_observations o ON (o.repository_id,o.iid,o.digest)=(m.repository_id,m.iid,m.digest) JOIN forge_mr_codes k ON (k.repository_id,k.iid,k.code_key)=(o.repository_id,o.iid,o.code_key) WHERE r.project_id=$1 AND r.id=$2 AND m.iid=$3 AND o.model->>'project_id'=r.gitlab_project_id::text`, project, repo, iid).Scan(&raw, &out.Observation, &out.CodeKey, &out.ObservedAt, &out.CommitsComplete, &base)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, false, nil
	}
	if err != nil {
		return out, false, err
	}
	var model forge.MergeRequest
	if json.Unmarshal(raw, &model) != nil || model.Digest() != out.Observation || model.CodeKey() != out.CodeKey {
		return out, false, domain.ErrInvalid
	}
	if _, e := work.NewMRLink(repo, base, model.ProjectID, iid, model.URL); e != nil {
		return out, false, nil
	}
	out.Title = model.Title
	out.Description = domain.Clip(model.Description, 500)
	out.URL = model.URL
	out.State = model.State
	out.Head = model.Head
	out.SourceProjectID = model.SourceProjectID
	out.ProviderUpdatedAt = model.UpdatedAt
	if expected != "" {
		matches := expected == model.Head
		out.ExpectedHeadMatches = &matches
	}
	if model.State == "merged" && out.CommitsComplete {
		for _, sha := range []string{model.Merge, model.Squash} {
			if sha != "" {
				out.ResultSHAs = append(out.ResultSHAs, sha)
			}
		}
	}
	err = tx.QueryRow(ctx, `SELECT r.id::text FROM repositories r JOIN forge_connections f ON f.repository_id=r.id AND f.enabled AND NOT f.blocked AND f.last_synced_at>now()-interval '10 minutes' WHERE r.project_id=$1 AND (r.gitlab_url=$2 OR(r.provider='github' AND $2 LIKE 'https://github.com/%')) AND r.gitlab_project_id=$3`, project, base, model.SourceProjectID).Scan(&out.SourceRepositoryID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return out, false, err
	}
	if out.SourceRepositoryID != "" && model.Head != "" {
		rows, err := tx.Query(ctx, `SELECT model FROM forge_pipelines WHERE repository_id=$1 AND sha=$2 AND model->>'project_id'=$3::bigint::text ORDER BY provider_updated_at DESC,id DESC LIMIT 9`, out.SourceRepositoryID, model.Head, model.SourceProjectID)
		if err != nil {
			return out, false, err
		}
		for rows.Next() {
			var raw []byte
			var pipeline forge.Pipeline
			if err := rows.Scan(&raw); err != nil {
				rows.Close()
				return out, false, err
			}
			if json.Unmarshal(raw, &pipeline) != nil {
				rows.Close()
				return out, false, domain.ErrInvalid
			}
			pipeline.URL = contextProviderURL(base, pipeline.URL)
			out.Pipelines = append(out.Pipelines, domain.PipelineView(pipeline, model, expected))
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return out, false, err
		}
		if len(out.Pipelines) > 8 {
			out.PipelinesMore = true
			out.Pipelines = out.Pipelines[:8]
		}
	}
	rows, err := tx.Query(ctx, `SELECT model FROM forge_deployments WHERE (repository_id::text=$1 AND sha=$2) OR(repository_id=$3 AND sha=ANY($4::text[])) ORDER BY provider_updated_at DESC,id DESC LIMIT 9`, out.SourceRepositoryID, model.Head, repo, out.ResultSHAs)
	if err != nil {
		return out, false, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var d forge.Deployment
		if err := rows.Scan(&raw); err != nil {
			return out, false, err
		}
		if json.Unmarshal(raw, &d) != nil {
			return out, false, domain.ErrInvalid
		}
		out.Deployments = append(out.Deployments, d)
	}
	if err := rows.Err(); err != nil {
		return out, false, err
	}
	if len(out.Deployments) > 8 {
		out.DeploymentsMore = true
		out.Deployments = out.Deployments[:8]
	}
	return out, true, nil
}

// contextProviderURL suppresses untrusted/credential-bearing provider links without hiding the typed CI observation.
func contextProviderURL(base, raw string) string {
	u, err := url.Parse(raw)
	b, e := url.Parse(base)
	if err != nil || e != nil || u.User != nil || u.Scheme != b.Scheme || u.Host != b.Host || u.RawQuery != "" || u.Fragment != "" || !strings.HasPrefix(u.Path, strings.TrimSuffix(b.Path, "/")+"/") || redact.Text(raw) != raw {
		return ""
	}
	return raw
}
