package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/identifier"
	"workgraph.local/platform/internal/work/application"
	"workgraph.local/platform/internal/work/domain"
)

// wakeTaskDiscovery is a local transaction effect; it never performs remote task writes.
func wakeTaskDiscovery(ctx context.Context, tx pgx.Tx, repository string) error {
	_, err := tx.Exec(ctx, `UPDATE work_connections c SET discovery_at=LEAST(c.discovery_at,now()) FROM repositories r WHERE r.id=$1 AND r.project_id=c.project_id`, repository)
	return err
}

// workMRMatches deliberately checks full source-project scope, current guarded membership and
// current repository authority before a task/MR association can cause an outbound operation.
const workMRMatches = `WITH bound AS (
 SELECT b.task_id,b.repository_id AS source_repository_id,b.session_id,rc.revision,cr.digest AS capture_digest,rc.sha,sr.gitlab_url,sr.provider,sr.gitlab_project_id AS source_project_id
 FROM work_capture_bindings b JOIN repositories sr ON sr.id=b.repository_id
 JOIN capture_revision_commits rc ON rc.repository_id=b.repository_id AND rc.session_id=b.session_id
 JOIN capture_revisions cr ON cr.repository_id=rc.repository_id AND cr.session_id=rc.session_id AND cr.revision=rc.revision
 JOIN commits c ON c.repository_id=rc.repository_id AND c.sha=rc.sha AND c.confirmed_at IS NOT NULL
 JOIN forge_connections sc ON sc.repository_id=sr.id AND sc.enabled AND NOT sc.blocked AND sc.last_synced_at>now()-interval '10 minutes'
 WHERE sr.project_id=$1 AND b.country_id=$2 AND ($3::text='' OR b.task_id::text=$3)
), matches AS (
 SELECT b.*,tr.id AS target_repository_id,tr.gitlab_url AS base_url,tr.gitlab_project_id AS provider_id,o.iid,o.model->>'url' AS url,o.digest AS mr_digest,o.code_key
 FROM bound b JOIN repositories tr ON tr.project_id=$1 AND tr.provider=b.provider AND (tr.provider='github' OR tr.gitlab_url=b.gitlab_url)
 JOIN forge_connections fc ON fc.repository_id=tr.id AND fc.enabled AND NOT fc.blocked AND fc.last_synced_at>now()-interval '10 minutes'
 JOIN forge_merge_requests m ON m.repository_id=tr.id AND m.available
 JOIN forge_mr_observations o ON o.repository_id=m.repository_id AND o.iid=m.iid AND o.digest=m.digest AND o.model->>'project_id'=tr.gitlab_project_id::text
 JOIN forge_mr_codes k ON k.repository_id=o.repository_id AND k.iid=o.iid AND k.code_key=o.code_key AND k.complete
 WHERE ($4::text='' OR tr.id::text=$4) AND ($5::bigint=0 OR o.iid=$5) AND ($6::text='' OR o.model->>'url'=$6) AND COALESCE(o.model->>'url','')<>''
 AND (EXISTS(SELECT 1 FROM forge_mr_commits mc WHERE mc.repository_id=o.repository_id AND mc.iid=o.iid AND mc.code_key=o.code_key AND mc.source_project_id=b.source_project_id AND o.model->>'source_project_id'=b.source_project_id::text AND mc.sha=b.sha)
 OR(b.source_repository_id=tr.id AND o.model->>'state'='merged' AND b.sha IN(o.model->>'merge',o.model->>'squash')))
)
 SELECT DISTINCT ON(m.task_id,m.target_repository_id,m.iid,m.url) m.task_id::text,m.target_repository_id::text,m.base_url,m.provider_id,m.iid,m.url,m.source_repository_id::text,m.session_id::text,m.revision,m.capture_digest,m.sha,m.mr_digest,m.code_key
 FROM matches m WHERE true `
const workMRMatchOrder = ` ORDER BY m.task_id,m.target_repository_id,m.iid,m.url,m.revision DESC,m.source_repository_id,m.session_id,m.sha LIMIT 100`

type linkMatch struct {
	TaskID   string
	Link     domain.MRLink
	Evidence domain.LinkEvidence
}
type rowScanner interface{ Scan(...any) error }

func readLinkMatch(row rowScanner) (linkMatch, error) {
	var m linkMatch
	err := row.Scan(&m.TaskID, &m.Link.RepositoryID, &m.Link.BaseURL, &m.Link.ProjectID, &m.Link.IID, &m.Link.URL, &m.Evidence.RepositoryID, &m.Evidence.SessionID, &m.Evidence.Revision, &m.Evidence.CaptureDigest, &m.Evidence.CommitSHA, &m.Evidence.MRObservation, &m.Evidence.MRCodeKey)
	m.Link.Title = fmt.Sprintf("GitLab MR !%d", m.Link.IID)
	return m, err
}
func linkRequestKey(task string, link domain.MRLink) string {
	sum := sha256.Sum256([]byte(link.URL))
	return fmt.Sprintf("mr-link:%s:%s:%d:%s", task, link.RepositoryID, link.IID, hex.EncodeToString(sum[:]))
}
func saveLinkEvidence(ctx context.Context, tx pgx.Tx, id string, e domain.LinkEvidence) error {
	if e.Validate() != nil {
		return domain.ErrInvalid
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO work_link_evidence(job_id,digest,model) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, id, e.Digest(), raw)
	return err
}

// discover handles one due project in a short connection-first transaction. Periodic repair
// closes capture/connection/metadata arrival races without scanning canonical conversation JSON.
func (s *WorkStore) discover(ctx context.Context) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var project, country string
	var auto bool
	err = tx.QueryRow(ctx, `SELECT project_id::text,country_id::text,auto_link_merge_requests FROM work_connections WHERE enabled AND NOT blocked AND discovery_at<=now() ORDER BY discovery_at,project_id LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&project, &country, &auto)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO work_jobs(id,project_id,country_id,task_id,kind,request_key)
 SELECT gen_random_uuid(),$1,$2,b.task_id,'refresh','task:'||b.task_id::text
 FROM work_capture_bindings b JOIN repositories r ON r.id=b.repository_id
 WHERE r.project_id=$1 AND b.country_id=$2 AND NOT EXISTS(SELECT 1 FROM work_jobs j WHERE j.project_id=$1 AND j.request_key='task:'||b.task_id::text)
 GROUP BY b.task_id ORDER BY b.task_id LIMIT 100 ON CONFLICT DO NOTHING`, project, country)
	if err != nil {
		return err
	}
	more := tag.RowsAffected() >= 100
	if auto {
		rows, e := tx.Query(ctx, workMRMatches+` AND NOT EXISTS(SELECT 1 FROM work_jobs j WHERE j.project_id=$1 AND j.request_key='mr-link:'||m.task_id::text||':'||m.target_repository_id::text||':'||m.iid::text||':'||encode(sha256(convert_to(m.url,'UTF8')),'hex') AND (j.state IN('pending','complete') OR COALESCE(j.last_error_code,'')<>'association_unavailable'))`+workMRMatchOrder, project, country, "", "", int64(0), "")
		if e != nil {
			return e
		}
		matches := []linkMatch{}
		for rows.Next() {
			m, e := readLinkMatch(rows)
			if e != nil {
				rows.Close()
				return e
			}
			matches = append(matches, m)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		more = more || len(matches) >= 100
		for _, m := range matches {
			id, e := identifier.New()
			if e != nil {
				return e
			}
			key := linkRequestKey(m.TaskID, m.Link)
			state, code := "pending", ""
			if m.Link.Validate() != nil {
				state, code = "blocked", "link_invalid"
				m.Link.URL = ""
			}
			raw, e := json.Marshal(m.Link)
			if e != nil {
				return e
			}
			err = tx.QueryRow(ctx, `INSERT INTO work_jobs(id,project_id,country_id,task_id,kind,request_key,mr_link,state,last_error_code) VALUES($1,$2,$3,$4,'link',$5,$6,$7,NULLIF($8,''))
 ON CONFLICT(project_id,request_key) DO UPDATE SET state=excluded.state,last_error_code=excluded.last_error_code,available_at=now(),lease_owner=NULL,lease_until=NULL WHERE work_jobs.state='blocked' AND work_jobs.last_error_code='association_unavailable' RETURNING id::text`, id, project, country, m.TaskID, key, raw, state, code).Scan(&id)
			if errors.Is(err, pgx.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			if err = saveLinkEvidence(ctx, tx, id, m.Evidence); err != nil {
				return err
			}
		}
	}
	_, err = tx.Exec(ctx, `UPDATE work_connections SET discovery_at=CASE WHEN $2 THEN now() ELSE now()+interval '1 minute' END WHERE project_id=$1`, project, more)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func currentLinkMatch(ctx context.Context, tx pgx.Tx, l application.Lease) (linkMatch, bool, error) {
	m, err := readLinkMatch(tx.QueryRow(ctx, workMRMatches+workMRMatchOrder, l.ProjectID, l.CountryID, l.TaskID, l.Link.RepositoryID, l.Link.IID, l.Link.URL))
	if errors.Is(err, pgx.ErrNoRows) {
		return m, false, nil
	}
	if err == nil && (m.Link != l.Link || m.Evidence.Validate() != nil) {
		return m, false, nil
	}
	return m, err == nil, err
}

// Links returns compact delivery history with an immutable association observation, never capture content.
func (s *WorkStore) Links(ctx context.Context, user, project, task string) (application.LinkPage, error) {
	result := application.LinkPage{Items: []application.LinkPublication{}}
	if !identifier.Valid(task) {
		return result, domain.ErrInvalid
	}
	err := s.authorized(ctx, user, project, access.ReadIntegration, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT j.id::text,j.task_id::text,j.state,COALESCE(j.last_error_code,''),j.mr_link,j.completed_at,(SELECT e.model FROM work_link_evidence e WHERE e.job_id=j.id ORDER BY e.observed_at DESC,e.digest LIMIT 1) FROM work_jobs j WHERE j.project_id=$1 AND j.task_id=$2 AND j.kind='link' ORDER BY j.created_at DESC,j.id LIMIT 51`, project, task)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var item application.LinkPublication
			var raw, evidence []byte
			if err = rows.Scan(&item.ID, &item.TaskID, &item.State, &item.ErrorCode, &raw, &item.CompletedAt, &evidence); err != nil {
				return err
			}
			if json.Unmarshal(raw, &item.Link) != nil {
				return domain.ErrInvalid
			}
			if evidence != nil {
				item.Evidence = &domain.LinkEvidence{}
				if json.Unmarshal(evidence, item.Evidence) != nil || item.Evidence.Validate() != nil {
					return domain.ErrInvalid
				}
			}
			result.Items = append(result.Items, item)
		}
		if len(result.Items) > 50 {
			result.HasMore = true
			result.Items = result.Items[:50]
		}
		return rows.Err()
	})
	return result, err
}
