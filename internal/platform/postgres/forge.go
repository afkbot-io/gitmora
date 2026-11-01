package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/forge/application"
	"workgraph.local/platform/internal/forge/domain"
	"workgraph.local/platform/internal/identifier"
)

// ForgeStore persists encrypted connections, minimal inbox records and fenced reconciliation leases.
type ForgeStore struct{ pool *pgxpool.Pool }

// NewForgeStore uses the application's existing PostgreSQL pool.
func NewForgeStore(pool *pgxpool.Pool) *ForgeStore { return &ForgeStore{pool} }

func (s *ForgeStore) authorized(ctx context.Context, subject, repository string, permission access.Permission, fn func(pgx.Tx, domain.Repository) error) error {
	if !identifier.Valid(subject) || !identifier.Valid(repository) {
		return access.ErrDenied
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var role string
	repo := domain.Repository{ID: repository}
	err = tx.QueryRow(ctx, `SELECT m.role,r.gitlab_url,r.gitlab_project_id,r.provider FROM memberships m JOIN repositories r ON r.project_id=m.project_id JOIN users u ON u.id=m.user_id
 WHERE r.id=$1 AND m.user_id=$2 AND u.enabled FOR SHARE OF m,r,u`, repository, subject).Scan(&role, &repo.BaseURL, &repo.ProjectID, &repo.Provider)
	if errors.Is(err, pgx.ErrNoRows) {
		return access.ErrDenied
	}
	if err != nil {
		return err
	}
	if !access.Allows(role, permission) {
		return access.ErrDenied
	}
	if err := fn(tx, repo); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RepositoryForAdmin authorizes before any provider credentials are sent over the network.
func (s *ForgeStore) RepositoryForAdmin(ctx context.Context, subject, repository string) (domain.Repository, error) {
	var result domain.Repository
	err := s.authorized(ctx, subject, repository, access.ManageIntegration, func(_ pgx.Tx, repo domain.Repository) error { result = repo; return nil })
	return result, err
}

// Connect rechecks administrator authority and catalog identity after provider verification.
func (s *ForgeStore) Connect(ctx context.Context, subject string, repo domain.Repository, envelope []byte, mode string, anonymous bool) (application.Status, error) {
	var result application.Status
	if anonymous && repo.Provider != "github" {
		return result, domain.ErrInvalid
	}
	if len(envelope) < 32 || len(envelope) > 16640 || (mode != "signature" && mode != "legacy" && mode != "disabled") {
		return result, domain.ErrInvalid
	}
	err := s.authorized(ctx, subject, repo.ID, access.ManageIntegration, func(tx pgx.Tx, current domain.Repository) error {
		if current != repo {
			return domain.ErrConflict
		}
		_, err := tx.Exec(ctx, `INSERT INTO forge_connections(repository_id,webhook_mode,secret_envelope,updated_by,anonymous) VALUES($1,$2,$3,$4,$5)
   ON CONFLICT(repository_id) DO UPDATE SET generation=forge_connections.generation+1,enabled=true,anonymous=excluded.anonymous,webhook_mode=excluded.webhook_mode,secret_envelope=excluded.secret_envelope,updated_by=excluded.updated_by,updated_at=now(),requested_seq=forge_connections.requested_seq+1,next_sync_at=now(),last_synced_at=NULL,lease_owner=NULL,lease_until=NULL,attempts=0,blocked=false,last_error_code=NULL,code_lease_owner=NULL,code_lease_until=NULL,code_blocked=false,code_error=NULL,code_checked_at=NULL,code_next_at=now(),code_probe_at=now()`, repo.ID, mode, envelope, subject, anonymous)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "UPDATE commits SET verify_after=now() WHERE repository_id=$1 AND confirmed_at IS NULL", repo.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE forge_code_runs SET available_at=now(),discovery_ended=CASE WHEN provider_available THEN discovery_ended ELSE false END,next_page=CASE WHEN provider_available THEN next_page ELSE 1 END WHERE repository_id=$1`, repo.ID); err != nil {
			return err
		}
		if err := resetMetadata(ctx, tx, repo.ID); err != nil {
			return err
		}
		result, err = readForgeStatus(ctx, tx, repo.ID)
		return err
	})
	return result, err
}

func readForgeStatus(ctx context.Context, tx pgx.Tx, repository string) (application.Status, error) {
	result := application.Status{RepositoryID: repository}
	err := tx.QueryRow(ctx, `SELECT c.enabled,c.generation,c.webhook_mode,COALESCE(c.last_error_code,''),c.last_synced_at,c.next_sync_at,c.blocked,r.provider,c.anonymous FROM forge_connections c JOIN repositories r ON r.id=c.repository_id WHERE c.repository_id=$1`, repository).Scan(&result.Enabled, &result.Generation, &result.WebhookMode, &result.LastErrorCode, &result.LastSyncedAt, &result.NextSyncAt, &result.Blocked, &result.Provider, &result.Anonymous)
	if errors.Is(err, pgx.ErrNoRows) {
		return result, domain.ErrNotFound
	}
	if err != nil {
		return result, err
	}
	if result.Enabled && result.WebhookMode != "disabled" {
		result.WebhookPath = "/v1/integrations/gitlab/" + repository + "/webhook"
	}
	result.Streams = []application.StreamStatus{}
	rows, err := tx.Query(ctx, "SELECT kind,next_page,cycle_before IS NOT NULL,watermark,last_complete_at,COALESCE(last_error_code,''),retry_at FROM forge_sync_streams WHERE repository_id=$1 ORDER BY kind", repository)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var stream application.StreamStatus
		if err := rows.Scan(&stream.Kind, &stream.NextPage, &stream.Active, &stream.Watermark, &stream.LastCompleteAt, &stream.ErrorCode, &stream.RetryAt); err != nil {
			return result, err
		}
		result.Streams = append(result.Streams, stream)
	}
	return result, rows.Err()
}

// Status returns connection metadata under current membership locks.
func (s *ForgeStore) Status(ctx context.Context, subject, repository string) (application.Status, error) {
	var result application.Status
	err := s.authorized(ctx, subject, repository, access.ReadIntegration, func(tx pgx.Tx, _ domain.Repository) error {
		var err error
		result, err = readForgeStatus(ctx, tx, repository)
		return err
	})
	return result, err
}

// Disconnect destroys recoverable provider credentials and fences all previously claimed work.
func (s *ForgeStore) Disconnect(ctx context.Context, subject, repository string) error {
	return s.authorized(ctx, subject, repository, access.ManageIntegration, func(tx pgx.Tx, _ domain.Repository) error {
		result, err := tx.Exec(ctx, `UPDATE forge_connections SET enabled=false,secret_envelope=NULL,generation=generation+1,lease_owner=NULL,lease_until=NULL,updated_by=$2,updated_at=now(),blocked=false WHERE repository_id=$1`, repository, subject)
		if err == nil && result.RowsAffected() != 1 {
			return domain.ErrNotFound
		}
		return err
	})
}

// RequestSync explicitly wakes/retries a connection and invalidates its previous worker owner.
func (s *ForgeStore) RequestSync(ctx context.Context, subject, repository string) error {
	return s.authorized(ctx, subject, repository, access.ManageIntegration, func(tx pgx.Tx, _ domain.Repository) error {
		result, err := tx.Exec(ctx, `UPDATE forge_connections SET next_sync_at=now(),requested_seq=requested_seq+1,blocked=false,attempts=0,lease_owner=NULL,lease_until=NULL WHERE repository_id=$1 AND enabled`, repository)
		if err == nil && result.RowsAffected() != 1 {
			return domain.ErrNotFound
		}
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, "UPDATE commits SET verify_after=now() WHERE repository_id=$1 AND confirmed_at IS NULL", repository)
		if err != nil {
			return err
		}
		return resetMetadata(ctx, tx, repository)
	})
}

// WebhookConnection reads an enabled webhook envelope; API-only policy is rejected before decryption.
func (s *ForgeStore) WebhookConnection(ctx context.Context, repository string) (application.Connection, error) {
	var connection application.Connection
	if !identifier.Valid(repository) {
		return connection, domain.ErrNotFound
	}
	err := s.pool.QueryRow(ctx, `SELECT r.id::text,r.gitlab_url,r.gitlab_project_id,r.provider,c.generation,c.secret_envelope FROM forge_connections c JOIN repositories r ON r.id=c.repository_id WHERE c.repository_id=$1 AND c.enabled AND c.webhook_mode IN('signature','legacy')`, repository).Scan(&connection.Repository.ID, &connection.Repository.BaseURL, &connection.Repository.ProjectID, &connection.Repository.Provider, &connection.Generation, &connection.Envelope)
	if errors.Is(err, pgx.ErrNoRows) {
		return connection, domain.ErrNotFound
	}
	return connection, err
}

// Accept atomically deduplicates a minimal event and schedules provider work; SHAs remain pending.
func (s *ForgeStore) Accept(ctx context.Context, connection application.Connection, event domain.Event) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var generation int64
	err = tx.QueryRow(ctx, "SELECT generation FROM forge_connections WHERE repository_id=$1 AND enabled AND webhook_mode IN('signature','legacy') FOR UPDATE", connection.Repository.ID).Scan(&generation)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && generation != connection.Generation {
		return false, domain.ErrUnauthenticated
	}
	if err != nil {
		return false, err
	}
	result, err := tx.Exec(ctx, `INSERT INTO forge_webhook_inbox(repository_id,delivery_key,body_digest,event_kind,merge_request_iid,after_sha) VALUES($1,$2,$3,$4,NULLIF($5,0),NULLIF($6,'')) ON CONFLICT DO NOTHING`, connection.Repository.ID, event.DeliveryKey, event.Digest, event.Kind, event.MergeRequestIID, event.After)
	if err != nil {
		return false, err
	}
	created := result.RowsAffected() == 1
	if !created {
		var digest, kind string
		if err := tx.QueryRow(ctx, "SELECT body_digest,event_kind FROM forge_webhook_inbox WHERE repository_id=$1 AND delivery_key=$2", connection.Repository.ID, event.DeliveryKey).Scan(&digest, &kind); err != nil {
			return false, err
		}
		if digest != event.Digest || kind != event.Kind {
			return false, domain.ErrConflict
		}
	} else {
		kinds := []string{}
		switch event.Kind {
		case "push", "tag_push":
			kinds = []string{"merge_requests", "pipelines", "child_pipelines"}
		case "merge_request", "note":
			kinds = []string{"merge_requests"}
		case "pipeline":
			kinds = []string{"pipelines", "child_pipelines"}
		case "deployment", "release":
			kinds = []string{"deployments"}
		}
		if len(kinds) > 0 {
			if _, err := tx.Exec(ctx, `UPDATE forge_sync_streams SET retry_at='1970-01-01 UTC',last_complete_at=CASE WHEN cycle_before IS NULL THEN NULL ELSE last_complete_at END,refresh_requested=cycle_before IS NOT NULL WHERE repository_id=$1 AND kind=ANY($2::text[])`, connection.Repository.ID, kinds); err != nil {
				return false, err
			}
		}
		if event.MergeRequestIID > 0 {
			if err := enqueueMR(ctx, tx, connection.Repository.ID, event.MergeRequestIID, time.Unix(0, 0), true); err != nil {
				return false, err
			}
		}
		if event.After != "" {
			if _, err := tx.Exec(ctx, `INSERT INTO commits(repository_id,sha) VALUES($1,$2) ON CONFLICT(repository_id,sha) DO UPDATE SET verify_after=LEAST(commits.verify_after,now())`, connection.Repository.ID, event.After); err != nil {
				return false, err
			}
		}
		if _, err := tx.Exec(ctx, "UPDATE forge_connections SET requested_seq=requested_seq+1,next_sync_at=LEAST(next_sync_at,now()) WHERE repository_id=$1", connection.Repository.ID); err != nil {
			return false, err
		}
	}
	return created, tx.Commit(ctx)
}

// Claim atomically leases one due connection; another worker can recover an expired lease.
func (s *ForgeStore) Claim(ctx context.Context) (application.Lease, bool, error) {
	var lease application.Lease
	owner, err := identifier.New()
	if err != nil {
		return lease, false, err
	}
	lease.Owner = owner
	err = s.pool.QueryRow(ctx, `WITH candidate AS(SELECT repository_id FROM forge_connections WHERE enabled AND NOT blocked AND next_sync_at<=now() AND (lease_until IS NULL OR lease_until<=now()) ORDER BY next_sync_at,repository_id FOR UPDATE SKIP LOCKED LIMIT 1)
 UPDATE forge_connections c SET lease_owner=$1,lease_until=now()+make_interval(secs=>$2) FROM candidate q,repositories r WHERE c.repository_id=q.repository_id AND r.id=c.repository_id
 RETURNING r.id::text,r.gitlab_url,r.gitlab_project_id,r.provider,c.generation,c.secret_envelope,c.requested_seq`, owner, application.LeaseDuration.Seconds()).Scan(&lease.Repository.ID, &lease.Repository.BaseURL, &lease.Repository.ProjectID, &lease.Repository.Provider, &lease.Generation, &lease.Envelope, &lease.Requested)
	if errors.Is(err, pgx.ErrNoRows) {
		return lease, false, nil
	}
	return lease, err == nil, err
}
func (s *ForgeStore) withLease(ctx context.Context, lease application.Lease, fn func(pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var repository string
	err = tx.QueryRow(ctx, `SELECT c.repository_id::text FROM forge_connections c JOIN repositories r ON r.id=c.repository_id WHERE c.repository_id=$1 AND c.enabled AND c.generation=$2 AND c.lease_owner=$3 AND c.lease_until>now() AND r.gitlab_url=$4 AND r.gitlab_project_id=$5 AND r.provider=$6 FOR UPDATE OF c FOR SHARE OF r`, lease.Repository.ID, lease.Generation, lease.Owner, lease.Repository.BaseURL, lease.Repository.ProjectID, lease.Repository.Provider).Scan(&repository)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrConflict
	}
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Pending reads a bounded verification page under a live, generation-fenced connection lease.
func (s *ForgeStore) Pending(ctx context.Context, lease application.Lease, limit int) ([]string, error) {
	if limit < 1 || limit > 64 {
		return nil, domain.ErrInvalid
	}
	commits := []string{}
	err := s.withLease(ctx, lease, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, "SELECT sha FROM commits WHERE repository_id=$1 AND confirmed_at IS NULL AND verify_after<=now() ORDER BY verify_after,sha LIMIT $2", lease.Repository.ID, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var sha string
			if err := rows.Scan(&sha); err != nil {
				return err
			}
			commits = append(commits, sha)
		}
		return rows.Err()
	})
	return commits, err
}

// MarkCommit persists only API-observed existence; a 404 remains unconfirmed with a retry time.
func (s *ForgeStore) MarkCommit(ctx context.Context, lease application.Lease, sha string, confirmed bool) error {
	return s.withLease(ctx, lease, func(tx pgx.Tx) error {
		result, err := tx.Exec(ctx, `UPDATE commits SET verification_checked_at=now(),verify_after=now()+interval '15 minutes',confirmed_at=CASE WHEN $3 THEN COALESCE(confirmed_at,now()) ELSE confirmed_at END WHERE repository_id=$1 AND sha=$2`, lease.Repository.ID, sha, confirmed)
		if err == nil && result.RowsAffected() != 1 {
			return domain.ErrConflict
		}
		if err == nil && confirmed {
			err = enqueueCodeCommit(ctx, tx, lease.Repository.ID, sha)
		}
		return err
	})
}

// Finish releases a live lease and keeps newer notifications, retry state and periodic work durable.
func (s *ForgeStore) Finish(ctx context.Context, lease application.Lease, outcome application.Outcome) error {
	if outcome.RetryAfter < 0 || outcome.RetryAfter > time.Hour {
		return domain.ErrInvalid
	}
	return s.withLease(ctx, lease, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE forge_connections SET lease_owner=NULL,lease_until=NULL,
   last_error_code=NULLIF($4,''),blocked=$5,attempts=CASE WHEN $4='' THEN 0 ELSE attempts+1 END,
   last_synced_at=CASE WHEN $4='' THEN now() ELSE last_synced_at END,
   next_sync_at=CASE WHEN $4<>'' THEN now()+make_interval(secs=>CASE WHEN $6::double precision>0 THEN $6 ELSE LEAST(900,5*power(2,LEAST(attempts,8))) END)
    WHEN requested_seq>$2 OR $3 THEN now() ELSE LEAST(now()+make_interval(secs=>$7),COALESCE((SELECT min(available_at) FROM forge_mr_jobs WHERE repository_id=$1),now()+make_interval(secs=>$7))) END
   WHERE repository_id=$1`, lease.Repository.ID, lease.Requested, outcome.More, outcome.Code, outcome.Blocked, outcome.RetryAfter.Seconds(), application.SyncInterval.Seconds())
		return err
	})
}
