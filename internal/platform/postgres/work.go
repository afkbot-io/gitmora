package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"workgraph.local/platform/internal/access"
	capture "workgraph.local/platform/internal/capture/domain"
	"workgraph.local/platform/internal/identifier"
	"workgraph.local/platform/internal/work/application"
	"workgraph.local/platform/internal/work/domain"
)

// WorkStore owns project-scoped Tasktopia credentials, source projections and publication journals.
type WorkStore struct{ pool *pgxpool.Pool }

// NewWorkStore uses the shared PostgreSQL pool without taking its ownership.
func NewWorkStore(pool *pgxpool.Pool) *WorkStore { return &WorkStore{pool} }

func (s *WorkStore) authorized(ctx context.Context, user, project string, permission access.Permission, fn func(pgx.Tx) error) error {
	if !identifier.Valid(user) || !identifier.Valid(project) {
		return access.ErrDenied
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var role string
	err = tx.QueryRow(ctx, `SELECT m.role FROM memberships m JOIN users u ON u.id=m.user_id WHERE m.project_id=$1 AND m.user_id=$2 AND u.enabled FOR SHARE OF m,u`, project, user).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return access.ErrDenied
	}
	if err != nil {
		return err
	}
	if !access.Allows(role, permission) {
		return access.ErrDenied
	}
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// AuthorizeAdmin prevents sending a candidate service token before local authorization.
func (s *WorkStore) AuthorizeAdmin(ctx context.Context, u, p string) error {
	return s.authorized(ctx, u, p, access.ManageIntegration, func(pgx.Tx) error { return nil })
}

// Connect rechecks administrator authority and preserves the immutable external scope.
func (s *WorkStore) Connect(ctx context.Context, u, p string, c domain.Config, envelope []byte) (application.Status, error) {
	var result application.Status
	if c.Validate() != nil || len(envelope) < 32 || len(envelope) > 16640 {
		return result, domain.ErrInvalid
	}
	err := s.authorized(ctx, u, p, access.ManageIntegration, func(tx pgx.Tx) error {
		// The connection's external identity is immutable, including while disabled.
		tag, err := tx.Exec(ctx, `INSERT INTO work_connections(project_id,endpoint,country_id,secret_envelope,updated_by,auto_link_merge_requests) VALUES($1,$2,$3,$4,$5,$6)
   ON CONFLICT(project_id) DO UPDATE SET generation=work_connections.generation+1,secret_envelope=excluded.secret_envelope,enabled=true,blocked=false,last_error_code=NULL,updated_by=excluded.updated_by,updated_at=now(),auto_link_merge_requests=excluded.auto_link_merge_requests,discovery_at=now()
   WHERE work_connections.endpoint=excluded.endpoint AND work_connections.country_id=excluded.country_id`, p, c.Endpoint, c.CountryID, envelope, u, c.AutoLinkMRs)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return domain.ErrConflict
		}
		if _, err = tx.Exec(ctx, `UPDATE work_jobs SET state='pending',available_at=now(),lease_owner=NULL,lease_until=NULL,last_error_code=NULL,attempts=0 WHERE project_id=$1 AND state<>'complete' AND (kind<>'link' OR COALESCE(last_error_code,'')<>'link_invalid')`, p); err != nil {
			return err
		}
		result, err = workStatus(ctx, tx, p)
		return err
	})
	return result, err
}
func workStatus(ctx context.Context, tx pgx.Tx, p string) (application.Status, error) {
	result := application.Status{ProjectID: p}
	err := tx.QueryRow(ctx, `SELECT endpoint,country_id::text,enabled,generation,blocked,COALESCE(last_error_code,''),auto_link_merge_requests FROM work_connections WHERE project_id=$1`, p).Scan(&result.Endpoint, &result.CountryID, &result.Enabled, &result.Generation, &result.Blocked, &result.ErrorCode, &result.AutoLinkMRs)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, domain.ErrNotFound
	}
	return result, err
}

// Status returns non-secret connection health under membership locks.
func (s *WorkStore) Status(ctx context.Context, u, p string) (application.Status, error) {
	var result application.Status
	err := s.authorized(ctx, u, p, access.ReadIntegration, func(tx pgx.Tx) error { var e error; result, e = workStatus(ctx, tx, p); return e })
	return result, err
}

// Disconnect destroys the envelope and invalidates outstanding worker ownership.
func (s *WorkStore) Disconnect(ctx context.Context, u, p string) error {
	return s.authorized(ctx, u, p, access.ManageIntegration, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE work_connections SET enabled=false,secret_envelope=NULL,generation=generation+1,updated_by=$2,updated_at=now() WHERE project_id=$1`, p, u)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrNotFound
		}
		_, err = tx.Exec(ctx, `UPDATE work_jobs SET lease_owner=NULL,lease_until=NULL WHERE project_id=$1`, p)
		return err
	})
}

// Refresh schedules an explicit country-scoped read. A request arriving during a read is retained.
func (s *WorkStore) Refresh(ctx context.Context, u, p, t string) error {
	if !identifier.Valid(t) {
		return domain.ErrInvalid
	}
	return s.authorized(ctx, u, p, access.ManageIntegration, func(tx pgx.Tx) error {
		status, err := workStatus(ctx, tx, p)
		if err != nil {
			return err
		}
		if !status.Enabled {
			return domain.ErrNotFound
		}
		id, err := identifier.New()
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO work_jobs(id,project_id,country_id,task_id,kind,request_key) VALUES($1,$2,$3,$4,'refresh',$5)
   ON CONFLICT(project_id,request_key) DO UPDATE SET requested_seq=work_jobs.requested_seq+1,available_at=now(),state='pending'`, id, p, status.CountryID, t, "task:"+t)
		return err
	})
}

// Task reads the available current projection while preserving immutable observation history.
func (s *WorkStore) Task(ctx context.Context, u, p, t string) (application.TaskView, error) {
	var result application.TaskView
	if !identifier.Valid(t) {
		return result, domain.ErrInvalid
	}
	err := s.authorized(ctx, u, p, access.ReadIntegration, func(tx pgx.Tx) error {
		var raw []byte
		err := tx.QueryRow(ctx, `SELECT o.model,t.digest,t.available,t.observed_at FROM work_tasks t JOIN work_task_observations o ON (o.project_id,o.task_id,o.digest)=(t.project_id,t.task_id,t.digest) JOIN work_connections c ON c.project_id=t.project_id AND c.country_id=t.country_id WHERE t.project_id=$1 AND t.task_id=$2 AND c.enabled AND NOT c.blocked`, p, t).Scan(&raw, &result.Digest, &result.Available, &result.ObservedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNotFound
		}
		if err != nil {
			return err
		}
		if !result.Available {
			return domain.ErrNotFound
		}
		return json.Unmarshal(raw, &result.Task)
	})
	return result, err
}

// QueuePublication reads exact server-owned evidence, verifies country scope and writes its immutable report atomically.
func (s *WorkStore) QueuePublication(ctx context.Context, u, p string, input application.PublishInput) (application.Publication, error) {
	var result application.Publication
	if !identifier.Valid(input.RepositoryID) || !identifier.Valid(input.SessionID) || input.Revision < 1 || input.Revision > 1_000_000_000 {
		return result, domain.ErrInvalid
	}
	err := s.authorized(ctx, u, p, access.ManageIntegration, func(tx pgx.Tx) error {
		status, err := workStatus(ctx, tx, p)
		if err != nil {
			return err
		}
		if !status.Enabled || status.Blocked {
			return domain.ErrConflict
		}
		key := fmt.Sprintf("capture:%s:%s:%d", input.RepositoryID, input.SessionID, input.Revision)
		var existing string
		err = tx.QueryRow(ctx, `SELECT id::text FROM work_jobs WHERE project_id=$1 AND request_key=$2`, p, key).Scan(&existing)
		if err == nil {
			result, err = workPublication(ctx, tx, p, existing)
			return err
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		var raw []byte
		var digest string
		err = tx.QueryRow(ctx, `SELECT cr.canonical,cr.digest FROM capture_revisions cr JOIN repositories r ON r.id=cr.repository_id WHERE r.project_id=$1 AND cr.repository_id=$2 AND cr.session_id=$3 AND cr.revision=$4 FOR SHARE OF r,cr`, p, input.RepositoryID, input.SessionID, input.Revision).Scan(&raw, &digest)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNotFound
		}
		if err != nil {
			return err
		}
		var snapshot capture.Snapshot
		if json.Unmarshal(raw, &snapshot) != nil {
			return domain.ErrInvalid
		}
		if snapshot.Task == nil || snapshot.Task.CountryID != status.CountryID {
			return domain.ErrConflict
		}
		rows, err := tx.Query(ctx, `SELECT c.sha FROM capture_revision_commits rc JOIN commits c ON c.repository_id=rc.repository_id AND c.sha=rc.sha WHERE rc.repository_id=$1 AND rc.session_id=$2 AND rc.revision=$3 AND c.confirmed_at IS NOT NULL ORDER BY c.sha`, input.RepositoryID, input.SessionID, input.Revision)
		if err != nil {
			return err
		}
		confirmed := []string{}
		for rows.Next() {
			var sha string
			if err = rows.Scan(&sha); err != nil {
				rows.Close()
				return err
			}
			confirmed = append(confirmed, sha)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(confirmed) == 0 {
			return domain.ErrConflict
		}
		id, err := identifier.New()
		if err != nil {
			return err
		}
		document, err := domain.NewEvidenceDocument(domain.Evidence{ID: id, Capture: domain.CaptureEvidence{RepositoryID: snapshot.RepositoryID, SessionID: snapshot.SessionID, Revision: snapshot.Revision, CountryID: snapshot.Task.CountryID, TaskID: snapshot.Task.TaskID, Digest: digest, Commits: snapshot.Commits}, Confirmed: confirmed})
		if err != nil {
			return err
		}
		body, err := json.Marshal(document)
		if err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `INSERT INTO work_jobs(id,project_id,country_id,task_id,kind,request_key,document) VALUES($1,$2,$3,$4,'publication',$5,$6) ON CONFLICT(project_id,request_key) DO UPDATE SET request_key=excluded.request_key RETURNING id::text`, id, p, status.CountryID, snapshot.Task.TaskID, key, body).Scan(&id)
		if err != nil {
			return err
		}
		result, err = workPublication(ctx, tx, p, id)
		return err
	})
	return result, err
}
func workPublication(ctx context.Context, tx pgx.Tx, p, id string) (application.Publication, error) {
	result := application.Publication{ID: id, ProjectID: p}
	var raw []byte
	err := tx.QueryRow(ctx, `SELECT task_id::text,state,COALESCE(last_error_code,''),document,completed_at FROM work_jobs WHERE project_id=$1 AND id=$2 AND kind='publication'`, p, id).Scan(&result.TaskID, &result.State, &result.ErrorCode, &raw, &result.CompletedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, domain.ErrNotFound
	}
	if err == nil {
		err = json.Unmarshal(raw, &result.Document)
	}
	return result, err
}

// Publication retrieves only a matching project journal entry under current membership.
func (s *WorkStore) Publication(ctx context.Context, u, p, id string) (application.Publication, error) {
	var result application.Publication
	if !identifier.Valid(id) {
		return result, domain.ErrInvalid
	}
	err := s.authorized(ctx, u, p, access.ReadIntegration, func(tx pgx.Tx) error { var e error; result, e = workPublication(ctx, tx, p, id); return e })
	return result, err
}

// Claim leases one operation; SKIP LOCKED permits concurrent independent worker processes.
func (s *WorkStore) Claim(ctx context.Context) (application.Lease, bool, error) {
	var lease application.Lease
	if err := s.discover(ctx); err != nil {
		return lease, false, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return lease, false, err
	}
	defer tx.Rollback(ctx)
	var raw []byte
	// Lock connection before job, matching rotation/disable/completion lock order.
	err = tx.QueryRow(ctx, `SELECT c.project_id::text,c.country_id::text,c.endpoint,c.generation,c.secret_envelope FROM work_connections c
 WHERE c.enabled AND NOT c.blocked AND EXISTS(SELECT 1 FROM work_jobs j WHERE j.project_id=c.project_id AND j.state='pending' AND (j.kind<>'link' OR c.auto_link_merge_requests) AND j.available_at<=now() AND (j.lease_until IS NULL OR j.lease_until<=now()))
 ORDER BY (SELECT min(j.available_at) FROM work_jobs j WHERE j.project_id=c.project_id AND j.state='pending' AND (j.lease_until IS NULL OR j.lease_until<=now())) LIMIT 1 FOR SHARE OF c SKIP LOCKED`).Scan(&lease.ProjectID, &lease.CountryID, &lease.Endpoint, &lease.Generation, &lease.Envelope)
	if errors.Is(err, pgx.ErrNoRows) {
		return lease, false, nil
	}
	if err != nil {
		return lease, false, err
	}
	var linkRaw []byte
	err = tx.QueryRow(ctx, `SELECT j.id::text,j.task_id::text,j.kind,j.document,j.mr_link FROM work_jobs j JOIN work_connections c ON c.project_id=j.project_id WHERE j.project_id=$1 AND j.state='pending' AND (j.kind<>'link' OR c.auto_link_merge_requests) AND j.available_at<=now() AND (j.lease_until IS NULL OR j.lease_until<=now()) ORDER BY j.available_at,j.created_at,j.id LIMIT 1 FOR UPDATE OF j SKIP LOCKED`, lease.ProjectID).Scan(&lease.ID, &lease.TaskID, &lease.Kind, &raw, &linkRaw)
	if errors.Is(err, pgx.ErrNoRows) {
		return lease, false, nil
	}
	if err != nil {
		return lease, false, err
	}
	if raw != nil {
		if err = json.Unmarshal(raw, &lease.Document); err != nil {
			return lease, false, err
		}
	}
	if lease.Kind == "link" {
		if json.Unmarshal(linkRaw, &lease.Link) != nil || lease.Link.Validate() != nil {
			return lease, false, domain.ErrInvalid
		}
		match, found, e := currentLinkMatch(ctx, tx, lease)
		if e != nil {
			return lease, false, e
		}
		if !found {
			if _, e = tx.Exec(ctx, `UPDATE work_jobs SET state='blocked',last_error_code='association_unavailable',lease_owner=NULL,lease_until=NULL WHERE id=$1`, lease.ID); e != nil {
				return lease, false, e
			}
			return lease, false, tx.Commit(ctx)
		}
		if e = saveLinkEvidence(ctx, tx, lease.ID, match.Evidence); e != nil {
			return lease, false, e
		}
	}
	lease.Owner, err = identifier.New()
	if err != nil {
		return lease, false, err
	}
	err = tx.QueryRow(ctx, `UPDATE work_jobs SET lease_owner=$2,lease_until=now()+interval '60 seconds',read_seq=requested_seq,read_started_at=clock_timestamp() WHERE id=$1 RETURNING read_started_at`, lease.ID, lease.Owner).Scan(&lease.ReadStartedAt)
	if err != nil {
		return lease, false, err
	}
	err = tx.Commit(ctx)
	return lease, err == nil, err
}

// Finish atomically updates source availability, journal outcome and bounded retry scheduling.
func (s *WorkStore) Finish(ctx context.Context, lease application.Lease, out application.Outcome) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var valid bool
	// Completion updates connection health; take the final lock mode immediately to avoid
	// two workers deadlocking while upgrading independent shared connection locks.
	err = tx.QueryRow(ctx, `SELECT enabled AND NOT blocked AND generation=$2 AND country_id::text=$3 AND endpoint=$4 AND ($5<>'link' OR auto_link_merge_requests) FROM work_connections WHERE project_id=$1 FOR UPDATE`, lease.ProjectID, lease.Generation, lease.CountryID, lease.Endpoint, lease.Kind).Scan(&valid)
	if err != nil || !valid {
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		return domain.ErrLease
	}
	var attempts int
	var requested, read int64
	var taskID, kind string
	var started time.Time
	var linkRaw, docRaw []byte
	err = tx.QueryRow(ctx, `SELECT attempts,requested_seq,read_seq,task_id::text,kind,read_started_at,mr_link,document FROM work_jobs WHERE id=$1 AND project_id=$2 AND lease_owner=$3 AND lease_until>now() FOR UPDATE`, lease.ID, lease.ProjectID, lease.Owner).Scan(&attempts, &requested, &read, &taskID, &kind, &started, &linkRaw, &docRaw)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrLease
	}
	if err != nil {
		return err
	}
	var link domain.MRLink
	var doc domain.EvidenceDocument
	if linkRaw != nil && json.Unmarshal(linkRaw, &link) != nil || docRaw != nil && json.Unmarshal(docRaw, &doc) != nil {
		return domain.ErrInvalid
	}
	if taskID != lease.TaskID || kind != lease.Kind || !started.Equal(lease.ReadStartedAt) || link != lease.Link || doc != lease.Document {
		return domain.ErrLease
	}
	if out.Code == "" && (kind == "publication" || kind == "link") && (out.Task == nil || kind == "link" && !out.Task.HasLink(link.URL)) {
		return domain.ErrInvalid
	}
	if out.Task != nil {
		task, e := domain.NormalizeTask(*out.Task)
		if e != nil || task.ID != lease.TaskID || task.CountryID != lease.CountryID {
			return domain.ErrInvalid
		}
		raw, e := json.Marshal(task)
		if e != nil {
			return e
		}
		_, err = tx.Exec(ctx, `INSERT INTO work_task_observations(project_id,country_id,task_id,model,digest) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, lease.ProjectID, lease.CountryID, lease.TaskID, raw, task.Digest())
		if err != nil {
			return err
		}
		if err = enqueueTaskIndex(ctx, tx, lease.ProjectID, lease.CountryID, lease.TaskID, task.Digest()); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO work_tasks(project_id,country_id,task_id,digest,read_started_at) VALUES($1,$2,$3,$4,$5) ON CONFLICT(project_id,task_id) DO UPDATE SET digest=excluded.digest,read_started_at=excluded.read_started_at,available=true,observed_at=now() WHERE work_tasks.read_started_at<=excluded.read_started_at`, lease.ProjectID, lease.CountryID, lease.TaskID, task.Digest(), lease.ReadStartedAt)
		if err != nil {
			return err
		}
	}
	if out.Unavailable {
		if _, err = tx.Exec(ctx, `UPDATE work_tasks SET available=false,observed_at=now(),read_started_at=$3 WHERE project_id=$1 AND task_id=$2 AND read_started_at<=$3`, lease.ProjectID, lease.TaskID, lease.ReadStartedAt); err != nil {
			return err
		}
	}
	state := "pending"
	delay := 5 * time.Minute
	if out.Code == "" {
		attempts = 0
		if lease.Kind == "publication" || lease.Kind == "link" {
			state = "complete"
		}
		if requested > read {
			delay = 0
		}
	} else {
		attempts++
		shift := attempts
		if shift > 8 {
			shift = 8
		}
		delay = time.Duration(1<<shift) * 5 * time.Second
		if delay > 15*time.Minute {
			delay = 15 * time.Minute
		}
		if out.RetryAfter > delay {
			delay = out.RetryAfter
		}
		if delay > time.Hour {
			delay = time.Hour
		}
		if out.Blocked {
			state = "blocked"
		}
	}
	_, err = tx.Exec(ctx, `UPDATE work_jobs SET state=$2,available_at=now()+$3*interval '1 second',attempts=$4,last_error_code=NULLIF($5,''),lease_owner=NULL,lease_until=NULL,completed_at=CASE WHEN $2='complete' THEN now() ELSE NULL END WHERE id=$1`, lease.ID, state, delay.Seconds(), attempts, out.Code)
	if err != nil {
		return err
	}
	// Authorization failures hide every cached task until explicit credential repair.
	blockedConnection := out.Code == "provider_unauthorized" || out.Code == "country_access_denied" || out.Code == "provider_endpoint_not_allowed" || out.Code == "credential_unavailable"
	_, err = tx.Exec(ctx, `UPDATE work_connections SET blocked=blocked OR $2,last_error_code=CASE WHEN blocked THEN last_error_code ELSE NULLIF($3,'') END WHERE project_id=$1`, lease.ProjectID, blockedConnection, out.Code)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

var _ application.Store = (*WorkStore)(nil)
