package postgres

import (
	"context"
	"encoding/json"
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

// eligibleSources is a security filter, not a post-ranking mask. Unavailable sources and
// wrong capabilities are excluded before ranking, LIMIT or source text return.
const baseSourceAccess = `j.project_id=$1 AND ((j.source_kind='capture' AND $2 AND catalog.project_id=j.project_id) OR(j.source_kind='code' AND $3 AND catalog.project_id=j.project_id AND fc.enabled AND NOT fc.blocked AND NOT fc.code_blocked AND fc.code_checked_at>now()-interval '10 minutes' AND cr.provider_available AND cc.confirmed_at IS NOT NULL AND cp.status='stored' AND cf.provider_base_url=catalog.gitlab_url AND cf.provider_project_id=catalog.gitlab_project_id))`
const baseSourceCurrent = `(j.source_kind='code' OR (j.source_kind='capture' AND cs.latest_revision=j.revision))`
const baseSourceJoins = `LEFT JOIN forge_connections fc ON fc.repository_id=j.repository_id
 LEFT JOIN commits cc ON cc.repository_id=j.repository_id AND cc.sha=j.code_sha
 LEFT JOIN forge_code_runs cr ON cr.repository_id=j.repository_id AND cr.sha=j.code_sha
 LEFT JOIN forge_code_paths cp ON cp.repository_id=j.repository_id AND cp.sha=j.code_sha AND cp.path=j.code_path
 LEFT JOIN forge_code_snapshots cf ON cf.repository_id=j.repository_id AND cf.sha=j.code_sha AND cf.path=j.code_path AND cf.digest=j.source_digest
 LEFT JOIN repositories catalog ON catalog.id=j.repository_id
 LEFT JOIN capture_sessions cs ON cs.repository_id=j.repository_id AND cs.session_id=j.session_id`

const sourceJoins = baseSourceJoins

const confirmedReferenceAccess = `(d.kind<>'code_reference' OR EXISTS(SELECT 1 FROM capture_revision_commits rc JOIN commits c ON c.repository_id=rc.repository_id AND c.sha=rc.sha WHERE rc.repository_id=j.repository_id AND rc.session_id=j.session_id AND rc.revision=j.revision AND c.confirmed_at IS NOT NULL))`

var eligibleSources = `(` + baseSourceAccess + `) AND ($4::text='' OR j.repository_id::text=$4) AND ($5 OR ` + baseSourceCurrent + `)`

// Parameters 1..5 are the shared authorized source scope; matching/ranking use 6..12.
const sourceIdentityProjection = `j.source_kind,j.project_id,j.repository_id,j.session_id,j.revision,j.source_digest,j.code_sha,j.code_path,j.code_blob_id`

// Keep only identity, authorized source metadata and score in the materialized candidates;
// fetch wide source text only for the final bounded selection.
// Identical language alternatives have the same matches and score, but repeat ranking work.
// Materialize the single query row once, including when PostgreSQL chooses a generic plan.
var queryPrefix = retrievalQuery("plain", false)
var lexicalQueryPrefix = retrievalQuery("lexical", false)
var pathQueryPrefix = retrievalQuery("path", false)
var commitQueryPrefix = retrievalQuery("plain", true)
var commitLexicalQueryPrefix = retrievalQuery("lexical", true)
var commitPathQueryPrefix = retrievalQuery("path", true)

// Resolve commit membership once per source before examining its message ranges. A
// correlated source-membership probe per chunk grows with unrelated chat history.
const commitCandidates = `, commit_sources AS MATERIALIZED (
 SELECT j.id,j.repository_id,j.session_id,j.revision,j.source_kind FROM retrieval_jobs j
 WHERE j.project_id=$1 AND ($4::text='' OR j.repository_id::text=$4)
 AND ((j.source_kind='code' AND j.code_sha=$6) OR (j.source_kind='capture' AND EXISTS(
 SELECT 1 FROM capture_revision_commits rc JOIN commits c ON (c.repository_id,c.sha)=(rc.repository_id,rc.sha)
 WHERE (rc.repository_id,rc.session_id,rc.revision)=(j.repository_id,j.session_id,j.revision) AND rc.sha=$6 AND c.confirmed_at IS NOT NULL)))
 ), commit_matches AS MATERIALIZED (
 SELECT d.id FROM commit_sources source JOIN retrieval_chunks d ON d.job_id=source.id
 WHERE source.source_kind='code' OR (d.metadata->>'relation'='git_ai_reported' AND d.metadata->>'reported_commit'=$6)
 OR EXISTS(SELECT 1 FROM capture_commit_ranges message_range
 WHERE (message_range.repository_id,message_range.session_id,message_range.revision)=(source.repository_id,source.session_id,source.revision) AND message_range.sha=$6
 AND CASE WHEN d.part ~ '^messages/[0-9]{1,4}$' THEN substring(d.part FROM 10)::integer>=message_range.start_message AND substring(d.part FROM 10)::integer<message_range.end_message ELSE false END)
 )`

// GIN selects candidate identities before authorization and ranking. No source text is
// loaded until the final page. One best chunk per source part keeps long records from
// occupying a result page; exact-ID expansion still addresses every original chunk.
// Bitmap OR keeps each matching row once without sorting a UNION of duplicate IDs.
// A single lexeme has no inter-word proximity to evaluate, so its rank uses frequency.
func retrievalQuery(mode string, commitScoped bool) string {
	candidates := ""
	chunks := "retrieval_chunks d"
	if mode == "lexical" {
		candidates = ", matched AS MATERIALIZED (SELECT id FROM retrieval_chunks WHERE lexical@@(SELECT query FROM terms) OR (part_lexical IS NOT NULL AND part_lexical@@(SELECT query FROM terms)))"
		chunks = "matched candidates JOIN retrieval_chunks d ON d.id=candidates.id"
	} else if mode == "path" {
		candidates = ", paths AS MATERIALIZED (SELECT job_id,part FROM retrieval_chunks WHERE file_paths@>ARRAY[$11::text])"
		chunks = "paths candidates JOIN retrieval_chunks d ON (d.job_id,d.part)=(candidates.job_id,candidates.part)"
	}
	if commitScoped {
		candidates = commitCandidates + candidates
		chunks += " JOIN commit_matches commit_hits ON commit_hits.id=d.id"
	}
	return `WITH forms AS (
 SELECT plainto_tsquery('simple',$8) AS simple_terms,plainto_tsquery('english',$8) AS english_terms,plainto_tsquery('russian',$8) AS russian_terms
 ), terms AS MATERIALIZED (
 SELECT simple_terms || CASE WHEN english_terms=simple_terms THEN ''::tsquery ELSE english_terms END
 || CASE WHEN russian_terms=simple_terms OR russian_terms=english_terms THEN ''::tsquery ELSE russian_terms END AS query FROM forms
 )` + candidates + `,
 allowed AS MATERIALIZED (
 SELECT d.id,d.job_id,d.part,` + sourceIdentityProjection + `,
 CASE WHEN j.source_kind='capture' THEN cs.latest_revision=j.revision ELSE true END AS current,
 j.source_observed_at AS observed_at,
 CASE WHEN $8::text='' THEN CASE d.kind WHEN 'session' THEN 0.4 ELSE 0.2 END
 ELSE (CASE WHEN numnode(terms.query)=1 THEN ts_rank(d.lexical,terms.query,32) ELSE ts_rank_cd(d.lexical,terms.query,32) END)+(CASE WHEN position(lower($8) IN lower(d.title_terms))>0 THEN 1.0 ELSE 0.0 END) END::double precision AS score
 FROM ` + chunks + ` JOIN retrieval_jobs j ON j.id=d.job_id ` + sourceJoins + ` CROSS JOIN terms
 WHERE ` + eligibleSources + ` AND j.completed_at IS NOT NULL
 AND d.indexed_at<=COALESCE($16::timestamptz,transaction_timestamp())
 AND (COALESCE(cardinality($7::text[]),0)=0 OR d.kind=ANY($7::text[]))
 AND ($10::text='' OR d.id=$10)
 AND ($11::text='' OR EXISTS(SELECT 1 FROM retrieval_chunks paths WHERE paths.job_id=d.job_id AND paths.part=d.part AND cardinality(paths.file_paths)>0 AND paths.file_paths@>ARRAY[$11::text]))
 AND ($12::text='' OR (j.source_kind='capture' AND j.session_id::text=$12))
 AND ($13::text='' OR lower(d.metadata->>'agent')=$13)
 AND ($14::text='' OR lower(d.metadata->>'model')=$14 OR lower((d.metadata->>'provider')||'/'||(d.metadata->>'model'))=$14)
 AND ($15::text='' OR d.metadata->>'role'=$15)
 AND ` + confirmedReferenceAccess + `
 AND ($6::text='' OR j.code_sha=$6 OR EXISTS(
 SELECT 1 FROM capture_revision_commits rc JOIN commits c ON c.repository_id=rc.repository_id AND c.sha=rc.sha
 WHERE rc.repository_id=j.repository_id AND rc.session_id=j.session_id AND rc.revision=j.revision AND rc.sha=$6 AND c.confirmed_at IS NOT NULL
 AND ((d.metadata->>'relation'='git_ai_reported' AND d.metadata->>'reported_commit'=$6) OR EXISTS(SELECT 1 FROM capture_commit_ranges message_range
 WHERE (message_range.repository_id,message_range.session_id,message_range.revision,message_range.sha)=(rc.repository_id,rc.session_id,rc.revision,rc.sha)
 AND CASE WHEN d.part ~ '^messages/[0-9]{1,4}$' THEN substring(d.part FROM 10)::integer>=message_range.start_message AND substring(d.part FROM 10)::integer<message_range.end_message ELSE false END))))
 ), ranked AS (
 SELECT *,row_number() OVER(PARTITION BY job_id,part ORDER BY score DESC,id) AS position FROM allowed
 ), selected AS (
 SELECT * FROM ranked WHERE position=1
 AND (NOT $17::boolean OR score<$18::double precision OR(score=$18 AND(observed_at<$19::timestamptz OR(observed_at=$19 AND id>$20::text))))
 ORDER BY score DESC,observed_at DESC,id LIMIT $9
 )
 SELECT s.id,d.kind,d.part,d.start_rune,d.end_rune,d.title,d.content,d.content_digest,
 s.source_kind,s.project_id::text,COALESCE(s.repository_id::text,''),COALESCE(s.session_id::text,''),COALESCE(s.revision,0),s.source_digest,COALESCE(s.code_sha,''),COALESCE(s.code_path,''),COALESCE(s.code_blob_id,''),d.line_start,d.line_end,
 s.current,s.observed_at,d.indexed_at,s.score,
 CASE WHEN s.source_kind='code' THEN ARRAY[s.code_sha] ELSE ARRAY(SELECT c.sha FROM capture_revision_commits rc JOIN commits c ON c.repository_id=rc.repository_id AND c.sha=rc.sha WHERE rc.repository_id=s.repository_id AND rc.session_id=s.session_id AND rc.revision=s.revision AND c.confirmed_at IS NOT NULL ORDER BY c.sha LIMIT 16) END,
 CASE WHEN s.source_kind='code' THEN 1 ELSE (SELECT count(*) FROM capture_revision_commits rc JOIN commits c ON c.repository_id=rc.repository_id AND c.sha=rc.sha WHERE rc.repository_id=s.repository_id AND rc.session_id=s.session_id AND rc.revision=s.revision AND c.confirmed_at IS NOT NULL) END,
 d.metadata,CASE WHEN $11::text<>'' THEN ARRAY[$11::text] ELSE COALESCE(paths.file_paths[1:8],'{}'::text[]) END,COALESCE(cardinality(paths.file_paths),0)
 FROM selected s JOIN retrieval_chunks d ON d.id=s.id
 LEFT JOIN retrieval_chunks paths ON paths.job_id=d.job_id AND paths.part=d.part AND cardinality(paths.file_paths)>0
 ORDER BY s.score DESC,s.observed_at DESC,s.id`
}

// readChunks returns only the authorized bounded candidates with their exact source identity.
func readChunks(ctx context.Context, tx pgx.Tx, p access.Principal, q domain.Query, id string) ([]domain.Hit, error) {
	statement := queryPrefix
	if q.Terms != "" {
		statement = lexicalQueryPrefix
	} else if q.FilePath != "" {
		statement = pathQueryPrefix
	}
	if q.CommitSHA != "" {
		statement = commitQueryPrefix
		if q.Terms != "" {
			statement = commitLexicalQueryPrefix
		} else if q.FilePath != "" {
			statement = commitPathQueryPrefix
		}
	}
	after := domain.SearchCursor{}
	if q.After != nil {
		after = *q.After
	}
	var cutoff any
	if !q.ReadAt.IsZero() {
		cutoff = q.ReadAt
	}
	rows, err := tx.Query(ctx, statement, q.ProjectID, p.Has(access.ReadCapture), p.Has(access.ReadIntegration), q.RepositoryID, q.History, q.CommitSHA, q.Kinds, q.Terms, q.Limit+1, id, q.FilePath, q.SessionID, q.Agent, q.Model, q.Role, cutoff, q.After != nil, after.Score, after.ObservedAt, after.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []domain.Hit{}
	for rows.Next() {
		var hit domain.Hit
		var metadata []byte
		dest := []any{&hit.ID, &hit.Kind, &hit.Part, &hit.Start, &hit.End, &hit.Title, &hit.Content, &hit.ContentDigest}
		dest = append(dest, sourceDest(&hit.Source)...)
		dest = append(dest, &hit.LineStart, &hit.LineEnd, &hit.Current, &hit.ObservedAt, &hit.IndexedAt, &hit.Score, &hit.ConfirmedCommits, &hit.ConfirmedCount)
		dest = append(dest, &metadata, &hit.FilePaths, &hit.FilePathCount)
		if err = rows.Scan(dest...); err != nil {
			return nil, err
		}
		if json.Unmarshal(metadata, &hit.Metadata) != nil || hit.Metadata.Validate() != nil || hit.Source.Validate() != nil || domain.Hash([]byte(hit.Content)) != hit.ContentDigest || len([]rune(hit.Content)) != hit.End-hit.Start {
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
		if q.After != nil {
			q.ReadAt = q.After.IndexedBefore
		} else if err := tx.QueryRow(ctx, "SELECT transaction_timestamp()").Scan(&q.ReadAt); err != nil {
			return err
		}

		if q.CommitPrefix != "" && q.After != nil && q.After.Literal {
			q.Terms = domain.Terms(q.PrefixText)
		} else if q.CommitPrefix != "" && q.After != nil && q.After.ResolvedCommitSHA != "" {
			q.CommitSHA = q.After.ResolvedCommitSHA
			result.ResolvedCommitSHA = q.CommitSHA
		} else if q.CommitPrefix != "" {
			rows, err := tx.Query(ctx, `SELECT DISTINCT sha FROM (SELECT c.sha FROM retrieval_jobs j `+sourceJoins+` JOIN capture_revision_commits rc ON j.source_kind='capture' AND (rc.repository_id,rc.session_id,rc.revision)=(j.repository_id,j.session_id,j.revision) JOIN commits c ON (c.repository_id,c.sha)=(rc.repository_id,rc.sha) AND c.confirmed_at IS NOT NULL WHERE `+eligibleSources+` AND starts_with(c.sha,$6) UNION ALL SELECT j.code_sha AS sha FROM retrieval_jobs j `+sourceJoins+` WHERE `+eligibleSources+` AND j.source_kind='code' AND starts_with(j.code_sha,$6)) scoped ORDER BY sha LIMIT 2`, q.ProjectID, p.Has(access.ReadCapture), p.Has(access.ReadIntegration), q.RepositoryID, q.History, q.CommitPrefix)
			if err != nil {
				return err
			}
			matches := []string{}
			for rows.Next() {
				var sha string
				if err = rows.Scan(&sha); err != nil {
					rows.Close()
					return err
				}
				matches = append(matches, sha)
			}
			rows.Close()
			if err = rows.Err(); err != nil {
				return err
			}
			if len(matches) > 1 {
				return domain.ErrAmbiguous
			}
			if len(matches) == 0 {
				if q.PrefixText == "" {
					return domain.ErrNotFound
				}
				q.Terms = domain.Terms(q.PrefixText)
			} else {
				q.CommitSHA = matches[0]
				result.ResolvedCommitSHA = q.CommitSHA
			}
		}
		items, err := readChunks(ctx, tx, p, q, "")
		if err != nil {
			return err
		}
		if len(items) > q.Limit {
			result.HasMore = true
			items = items[:q.Limit]
			last := items[len(items)-1]
			result.Next = &domain.SearchCursor{QueryDigest: q.Fingerprint(), IndexedBefore: q.ReadAt, Score: last.Score, ObservedAt: last.ObservedAt, ID: last.ID, ResolvedCommitSHA: result.ResolvedCommitSHA, Literal: q.CommitPrefix != "" && q.CommitSHA == ""}
		}
		result.Items = items
		return tx.QueryRow(ctx, `SELECT count(*),count(*) FILTER(WHERE j.last_error_code IS NOT NULL) FROM retrieval_jobs j `+sourceJoins+` WHERE `+eligibleSources+` AND (j.completed_at IS NULL OR j.projection_version<2)`, q.ProjectID, p.Has(access.ReadCapture), p.Has(access.ReadIntegration), q.RepositoryID, q.History).Scan(&result.PendingSources, &result.FailedSources)
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
