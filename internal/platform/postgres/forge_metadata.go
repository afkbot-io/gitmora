package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"workgraph.local/platform/internal/access"
	capture "workgraph.local/platform/internal/capture/domain"
	"workgraph.local/platform/internal/forge/application"
	"workgraph.local/platform/internal/forge/domain"
)

func resetMetadata(ctx context.Context, tx pgx.Tx, repository string) error {
	_, err := tx.Exec(ctx, `INSERT INTO forge_sync_streams(repository_id,kind) SELECT $1,kind FROM unnest($2::text[]) kind
 ON CONFLICT(repository_id,kind) DO UPDATE SET version=forge_sync_streams.version+1,watermark='1970-01-01 UTC',cycle_after=NULL,cycle_before=NULL,next_page=1,last_complete_at=NULL,full_due_at=now(),retry_at='1970-01-01 UTC',last_error_code=NULL,refresh_requested=false`, repository, []string{"merge_requests", "pipelines", "child_pipelines", "deployments"})
	return err
}

// NextMetadata selects one fair, resumable source unit inside the current worker lease.
func (s *ForgeStore) NextMetadata(ctx context.Context, lease application.Lease) (application.MetadataWork, bool, error) {
	var unit application.MetadataWork
	found := false
	err := s.withLease(ctx, lease, func(tx pgx.Tx) error {
		var lane int
		if err := tx.QueryRow(ctx, "SELECT metadata_lane FROM forge_connections WHERE repository_id=$1", lease.Repository.ID).Scan(&lane); err != nil {
			return err
		}
		for offset := 0; offset < 5; offset++ {
			candidate := (lane + offset) % 5
			if candidate == 4 {
				job := &application.MRWork{}
				var expected []byte
				err := tx.QueryRow(ctx, `SELECT iid,version,requested_seq,stage,next_page,expected FROM forge_mr_jobs WHERE repository_id=$1 AND available_at<=now() ORDER BY available_at,iid LIMIT 1 FOR UPDATE`, lease.Repository.ID).Scan(&job.IID, &job.Version, &job.Requested, &job.Stage, &job.Page, &expected)
				if errors.Is(err, pgx.ErrNoRows) {
					continue
				}
				if err != nil {
					return err
				}
				if len(expected) > 0 && json.Unmarshal(expected, &job.Expected) != nil {
					return domain.ErrInvalid
				}
				unit = application.MetadataWork{Lane: candidate, MR: job}
				found = true
				return nil
			}
			kind := domain.StreamKinds()[candidate]
			var due bool
			if err := tx.QueryRow(ctx, `SELECT retry_at<=now() AND (cycle_before IS NOT NULL OR last_complete_at IS NULL OR last_complete_at<=now()-make_interval(secs=>$3) OR full_due_at<=now()) FROM forge_sync_streams WHERE repository_id=$1 AND kind=$2`, lease.Repository.ID, kind, application.SyncInterval.Seconds()).Scan(&due); err != nil {
				return err
			}
			if !due {
				continue
			}
			_, err := tx.Exec(ctx, `UPDATE forge_sync_streams SET cycle_after=CASE WHEN full_due_at<=now() THEN '1970-01-01 UTC'::timestamptz ELSE GREATEST('1970-01-01 UTC'::timestamptz,watermark-interval '10 minutes') END,cycle_before=now(),cycle_full=full_due_at<=now(),next_page=1,version=version+1 WHERE repository_id=$1 AND kind=$2 AND cycle_before IS NULL`, lease.Repository.ID, kind)
			if err != nil {
				return err
			}
			unit = application.MetadataWork{Lane: candidate}
			unit.Cursor.Kind = kind
			err = tx.QueryRow(ctx, `SELECT version,cycle_after,cycle_before,next_page,cycle_full FROM forge_sync_streams WHERE repository_id=$1 AND kind=$2`, lease.Repository.ID, kind).Scan(&unit.Cursor.Version, &unit.Cursor.After, &unit.Cursor.Before, &unit.Cursor.Page, &unit.Cursor.Full)
			found = err == nil
			return err
		}
		return nil
	})
	return unit, found, err
}

func enqueueMR(ctx context.Context, tx pgx.Tx, repository string, iid int64, updated time.Time, force bool) error {
	_, err := tx.Exec(ctx, `INSERT INTO forge_mr_jobs(repository_id,iid)
 SELECT $1,$2 WHERE $4 OR NOT EXISTS(SELECT 1 FROM forge_merge_requests m JOIN forge_mr_observations o ON o.repository_id=m.repository_id AND o.iid=m.iid AND o.digest=m.digest JOIN forge_mr_codes c ON c.repository_id=o.repository_id AND c.iid=o.iid AND c.code_key=o.code_key WHERE m.repository_id=$1 AND m.iid=$2 AND m.available AND m.provider_updated_at>=$3 AND c.complete)
 ON CONFLICT(repository_id,iid) DO UPDATE SET requested_seq=forge_mr_jobs.requested_seq+1`, repository, iid, updated, force)
	return err
}
func advanceLane(ctx context.Context, tx pgx.Tx, lease application.Lease, lane int) error {
	_, err := tx.Exec(ctx, "UPDATE forge_connections SET metadata_lane=$2 WHERE repository_id=$1", lease.Repository.ID, (lane+1)%5)
	return err
}

// ApplyChanges commits a provider page and its cursor together; failures never skip a page.
func (s *ForgeStore) ApplyChanges(ctx context.Context, lease application.Lease, unit application.MetadataWork, page domain.ChangePage) error {
	return s.withLease(ctx, lease, func(tx pgx.Tx) error {
		var version int64
		var currentPage int
		err := tx.QueryRow(ctx, "SELECT version,next_page FROM forge_sync_streams WHERE repository_id=$1 AND kind=$2 FOR UPDATE", lease.Repository.ID, unit.Cursor.Kind).Scan(&version, &currentPage)
		if err != nil {
			return err
		}
		if version != unit.Cursor.Version || currentPage != unit.Cursor.Page || (page.NextPage != 0 && page.NextPage != currentPage+1) {
			return domain.ErrConflict
		}
		if page.Unavailable {
			if _, err := tx.Exec(ctx, "UPDATE forge_sync_streams SET retry_at=now()+interval '15 minutes',last_error_code='provider_collection_unavailable',version=version+1 WHERE repository_id=$1 AND kind=$2", lease.Repository.ID, unit.Cursor.Kind); err != nil {
				return err
			}
			return advanceLane(ctx, tx, lease, unit.Lane)
		}
		if _, err := tx.Exec(ctx, "UPDATE forge_sync_streams SET retry_at='1970-01-01 UTC',last_error_code=NULL WHERE repository_id=$1 AND kind=$2", lease.Repository.ID, unit.Cursor.Kind); err != nil {
			return err
		}
		for _, hint := range page.MergeRequests {
			if hint.IID < 1 || hint.UpdatedAt.IsZero() {
				return domain.ErrInvalid
			}
			if err := enqueueMR(ctx, tx, lease.Repository.ID, hint.IID, hint.UpdatedAt, unit.Cursor.Full); err != nil {
				return err
			}
		}
		for _, model := range page.Pipelines {
			normalized, err := domain.NormalizePipeline(model)
			if err != nil || model.ProjectID != lease.Repository.ProjectID {
				return domain.ErrInvalid
			}
			raw, _ := json.Marshal(normalized)
			tag, err := tx.Exec(ctx, `INSERT INTO forge_pipelines(repository_id,id,sha,model,provider_updated_at) VALUES($1,$2,$3,$4,$5) ON CONFLICT(repository_id,id) DO UPDATE SET model=excluded.model,provider_updated_at=excluded.provider_updated_at WHERE forge_pipelines.sha=excluded.sha AND forge_pipelines.provider_updated_at<=excluded.provider_updated_at`, lease.Repository.ID, model.ID, model.SHA, raw, model.UpdatedAt)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				var prior string
				if err := tx.QueryRow(ctx, "SELECT sha FROM forge_pipelines WHERE repository_id=$1 AND id=$2", lease.Repository.ID, model.ID).Scan(&prior); err != nil {
					return err
				}
				if prior != model.SHA {
					return domain.ErrConflict
				}
			}
		}
		for _, model := range page.Deployments {
			normalized, err := domain.NormalizeDeployment(model)
			if err != nil {
				return err
			}
			raw, _ := json.Marshal(normalized)
			tag, err := tx.Exec(ctx, `INSERT INTO forge_deployments(repository_id,id,sha,model,provider_updated_at) VALUES($1,$2,$3,$4,$5) ON CONFLICT(repository_id,id) DO UPDATE SET model=excluded.model,provider_updated_at=excluded.provider_updated_at WHERE forge_deployments.sha=excluded.sha AND forge_deployments.provider_updated_at<=excluded.provider_updated_at`, lease.Repository.ID, model.ID, model.SHA, raw, model.UpdatedAt)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				var prior string
				if err := tx.QueryRow(ctx, "SELECT sha FROM forge_deployments WHERE repository_id=$1 AND id=$2", lease.Repository.ID, model.ID).Scan(&prior); err != nil {
					return err
				}
				if prior != model.SHA {
					return domain.ErrConflict
				}
			}
		}
		if page.NextPage == 0 {
			_, err = tx.Exec(ctx, `UPDATE forge_sync_streams SET version=version+1,watermark=cycle_before,cycle_after=NULL,cycle_before=NULL,next_page=1,last_complete_at=CASE WHEN refresh_requested THEN NULL ELSE now() END,refresh_requested=false,full_due_at=CASE WHEN cycle_full THEN now()+make_interval(secs=>$3) ELSE full_due_at END WHERE repository_id=$1 AND kind=$2`, lease.Repository.ID, unit.Cursor.Kind, application.FullRepairInterval.Seconds())
		} else {
			_, err = tx.Exec(ctx, "UPDATE forge_sync_streams SET version=version+1,next_page=$3 WHERE repository_id=$1 AND kind=$2", lease.Repository.ID, unit.Cursor.Kind, page.NextPage)
		}
		if err != nil {
			return err
		}
		return advanceLane(ctx, tx, lease, unit.Lane)
	})
}
func checkMRJob(ctx context.Context, tx pgx.Tx, lease application.Lease, unit application.MetadataWork) error {
	if unit.MR == nil {
		return domain.ErrInvalid
	}
	var version int64
	err := tx.QueryRow(ctx, "SELECT version FROM forge_mr_jobs WHERE repository_id=$1 AND iid=$2 FOR UPDATE", lease.Repository.ID, unit.MR.IID).Scan(&version)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && version != unit.MR.Version {
		return domain.ErrConflict
	}
	return err
}
func observeMR(ctx context.Context, tx pgx.Tx, lease application.Lease, model domain.MergeRequest) error {
	normalized, err := domain.NormalizeMR(model)
	if err != nil || model.ProjectID != lease.Repository.ProjectID {
		return domain.ErrInvalid
	}
	raw, _ := json.Marshal(normalized)
	if _, err := tx.Exec(ctx, `INSERT INTO forge_mr_codes(repository_id,iid,code_key) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, lease.Repository.ID, model.IID, model.CodeKey()); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO forge_mr_observations(repository_id,iid,digest,code_key,model,provider_updated_at) VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`, lease.Repository.ID, model.IID, normalized.Digest(), normalized.CodeKey(), raw, model.UpdatedAt); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO forge_merge_requests(repository_id,iid,digest,provider_updated_at) VALUES($1,$2,$3,$4) ON CONFLICT(repository_id,iid) DO UPDATE SET digest=excluded.digest,provider_updated_at=excluded.provider_updated_at,available=true WHERE forge_merge_requests.provider_updated_at<=excluded.provider_updated_at`, lease.Repository.ID, model.IID, normalized.Digest(), model.UpdatedAt); err != nil {
		return err
	}
	if model.State == "merged" {
		for _, sha := range []string{model.Merge, model.Squash} {
			if sha != "" {
				if _, err := tx.Exec(ctx, "INSERT INTO commits(repository_id,sha) VALUES($1,$2) ON CONFLICT DO NOTHING", lease.Repository.ID, sha); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
func applyDetail(ctx context.Context, tx pgx.Tx, lease application.Lease, unit application.MetadataWork, model *domain.MergeRequest) error {
	if model == nil {
		if _, err := tx.Exec(ctx, "UPDATE forge_merge_requests SET available=false WHERE repository_id=$1 AND iid=$2", lease.Repository.ID, unit.MR.IID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "UPDATE forge_mr_jobs SET stage='detail',expected=NULL,version=version+1,available_at=now()+interval '15 minutes' WHERE repository_id=$1 AND iid=$2", lease.Repository.ID, unit.MR.IID)
		return err
	}
	if model.IID != unit.MR.IID {
		return domain.ErrInvalid
	}
	if err := observeMR(ctx, tx, lease, *model); err != nil {
		return err
	}
	var complete bool
	if err := tx.QueryRow(ctx, "SELECT complete FROM forge_mr_codes WHERE repository_id=$1 AND iid=$2 AND code_key=$3", lease.Repository.ID, model.IID, model.CodeKey()).Scan(&complete); err != nil {
		return err
	}
	expected := *model
	expected.Title = ""
	expected.Description = ""
	expected.URL = ""
	raw, _ := json.Marshal(expected)
	if _, err := tx.Exec(ctx, "UPDATE forge_mr_jobs SET stage='commits',expected=$3,next_page=1,version=version+1,read_seq=$4,available_at=now() WHERE repository_id=$1 AND iid=$2", lease.Repository.ID, model.IID, raw, unit.MR.Requested); err != nil {
		return err
	}
	if !model.CodeReady() {
		_, err := tx.Exec(ctx, "UPDATE forge_mr_jobs SET stage='detail',available_at=now()+interval '30 seconds' WHERE repository_id=$1 AND iid=$2", lease.Repository.ID, model.IID)
		return err
	}
	if complete {
		return finishMRJob(ctx, tx, lease, model.IID)
	}
	return nil
}
func finishMRJob(ctx context.Context, tx pgx.Tx, lease application.Lease, iid int64) error {
	if _, err := tx.Exec(ctx, "DELETE FROM forge_mr_jobs WHERE repository_id=$1 AND iid=$2 AND requested_seq=read_seq", lease.Repository.ID, iid); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, "UPDATE forge_mr_jobs SET stage='detail',next_page=1,expected=NULL,version=version+1,available_at=now() WHERE repository_id=$1 AND iid=$2", lease.Repository.ID, iid)
	return err
}

// ApplyMR preserves an immutable detail observation and prepares its scoped source-membership job.
func (s *ForgeStore) ApplyMR(ctx context.Context, lease application.Lease, unit application.MetadataWork, model *domain.MergeRequest) error {
	return s.withLease(ctx, lease, func(tx pgx.Tx) error {
		if err := checkMRJob(ctx, tx, lease, unit); err != nil {
			return err
		}
		if err := applyDetail(ctx, tx, lease, unit, model); err != nil {
			return err
		}
		return advanceLane(ctx, tx, lease, unit.Lane)
	})
}

// ApplyMRCommits publishes lineage only after all guarded source pages complete.
func (s *ForgeStore) ApplyMRCommits(ctx context.Context, lease application.Lease, unit application.MetadataWork, page domain.MRCommitPage) error {
	return s.withLease(ctx, lease, func(tx pgx.Tx) error {
		if err := checkMRJob(ctx, tx, lease, unit); err != nil {
			return err
		}
		if page.Missing {
			if err := applyDetail(ctx, tx, lease, unit, nil); err != nil {
				return err
			}
			return advanceLane(ctx, tx, lease, unit.Lane)
		}
		if page.Replacement != nil {
			if err := applyDetail(ctx, tx, lease, unit, page.Replacement); err != nil {
				return err
			}
			return advanceLane(ctx, tx, lease, unit.Lane)
		}
		if unit.MR.Stage != "commits" || page.NextPage != 0 && page.NextPage != unit.MR.Page+1 {
			return domain.ErrConflict
		}
		model := unit.MR.Expected
		for _, sha := range page.Commits {
			if !capture.ValidCommit(sha) {
				return domain.ErrInvalid
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO forge_mr_commits(repository_id,iid,code_key,source_project_id,sha) SELECT $1,$2,$3,$4,sha FROM unnest($5::text[]) sha ON CONFLICT DO NOTHING`, lease.Repository.ID, model.IID, model.CodeKey(), model.SourceProjectID, page.Commits); err != nil {
			return err
		}
		if page.NextPage != 0 {
			if _, err := tx.Exec(ctx, "UPDATE forge_mr_jobs SET next_page=$3,version=version+1 WHERE repository_id=$1 AND iid=$2", lease.Repository.ID, model.IID, page.NextPage); err != nil {
				return err
			}
		} else {
			if _, err := tx.Exec(ctx, "UPDATE forge_mr_codes SET complete=true WHERE repository_id=$1 AND iid=$2 AND code_key=$3", lease.Repository.ID, model.IID, model.CodeKey()); err != nil {
				return err
			}
			if err := wakeTaskDiscovery(ctx, tx, lease.Repository.ID); err != nil {
				return err
			}
			if model.State == "merged" {
				for _, result := range []struct{ kind, sha string }{{"merge", model.Merge}, {"squash", model.Squash}} {
					if result.sha == "" {
						continue
					}
					if _, err := tx.Exec(ctx, `INSERT INTO forge_commit_lineage(repository_id,iid,code_key,source_project_id,source_sha,result_sha,kind) SELECT repository_id,iid,code_key,source_project_id,sha,$4,$5 FROM forge_mr_commits WHERE repository_id=$1 AND iid=$2 AND code_key=$3 ON CONFLICT DO NOTHING`, lease.Repository.ID, model.IID, model.CodeKey(), result.sha, result.kind); err != nil {
						return err
					}
				}
			}
			if err := finishMRJob(ctx, tx, lease, model.IID); err != nil {
				return err
			}
		}
		return advanceLane(ctx, tx, lease, unit.Lane)
	})
}

// MetadataDue reports unfinished/due source units without allowing a completed stream to starve jobs.
func (s *ForgeStore) MetadataDue(ctx context.Context, lease application.Lease) (bool, error) {
	var due bool
	err := s.withLease(ctx, lease, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM forge_sync_streams WHERE repository_id=$1 AND retry_at<=now() AND (cycle_before IS NOT NULL OR last_complete_at IS NULL OR last_complete_at<=now()-make_interval(secs=>$2) OR full_due_at<=now())) OR EXISTS(SELECT 1 FROM forge_mr_jobs WHERE repository_id=$1 AND available_at<=now())`, lease.Repository.ID, application.SyncInterval.Seconds()).Scan(&due)
	})
	return due, err
}

func metadataLimit(query application.MetadataQuery) (int, error) {
	if query.Limit == 0 {
		return 50, nil
	}
	if query.Limit < 1 || query.Limit > 100 {
		return 0, domain.ErrInvalid
	}
	return query.Limit, nil
}

// ReadMR returns an exact sanitized observation and a bounded page of its source project commits.
func (s *ForgeStore) ReadMR(ctx context.Context, subject, repository string, query application.MetadataQuery) (application.MRView, error) {
	result := application.MRView{SourceCommits: []string{}, Results: []string{}}
	limit, err := metadataLimit(query)
	if err != nil || query.IID < 1 || query.AfterSHA != "" && !capture.ValidCommit(query.AfterSHA) {
		return result, domain.ErrInvalid
	}
	err = s.authorized(ctx, subject, repository, access.ReadIntegration, func(tx pgx.Tx, _ domain.Repository) error {
		var raw []byte
		err := tx.QueryRow(ctx, `SELECT o.model,o.digest,o.code_key,m.available,c.complete FROM forge_merge_requests m JOIN forge_mr_observations o ON o.repository_id=m.repository_id AND o.iid=m.iid AND ($3='' AND o.digest=m.digest OR $3<>'' AND o.digest=$3) JOIN forge_mr_codes c ON c.repository_id=o.repository_id AND c.iid=o.iid AND c.code_key=o.code_key WHERE m.repository_id=$1 AND m.iid=$2`, repository, query.IID, query.Observation).Scan(&raw, &result.Observation, &result.CodeKey, &result.Available, &result.CommitsComplete)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNotFound
		}
		if err != nil {
			return err
		}
		if json.Unmarshal(raw, &result.Model) != nil || result.Model.Digest() != result.Observation {
			return errors.New("stored provider observation mismatch")
		}
		rows, err := tx.Query(ctx, "SELECT sha FROM forge_mr_commits WHERE repository_id=$1 AND iid=$2 AND code_key=$3 AND sha>$4 ORDER BY sha LIMIT $5", repository, query.IID, result.CodeKey, query.AfterSHA, limit+1)
		if err != nil {
			return err
		}
		for rows.Next() {
			var sha string
			if err := rows.Scan(&sha); err != nil {
				rows.Close()
				return err
			}
			result.SourceCommits = append(result.SourceCommits, sha)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if len(result.SourceCommits) > limit {
			result.SourceCommits = result.SourceCommits[:limit]
			result.NextSHA = result.SourceCommits[len(result.SourceCommits)-1]
		}
		if result.Model.State == "merged" && result.CommitsComplete {
			for _, sha := range []string{result.Model.Merge, result.Model.Squash} {
				if sha != "" {
					result.Results = append(result.Results, sha)
				}
			}
		}
		return nil
	})
	return result, err
}

// ReadPipelines expands bounded CI metadata; every row retains the immutable tested SHA.
func (s *ForgeStore) ReadPipelines(ctx context.Context, subject, repository string, query application.MetadataQuery) (application.PipelinePage, error) {
	result := application.PipelinePage{Items: []domain.Pipeline{}}
	limit, err := metadataLimit(query)
	if err != nil || query.AfterID < 0 {
		return result, domain.ErrInvalid
	}
	err = s.authorized(ctx, subject, repository, access.ReadIntegration, func(tx pgx.Tx, _ domain.Repository) error {
		rows, err := tx.Query(ctx, "SELECT model FROM forge_pipelines WHERE repository_id=$1 AND id>$2 ORDER BY id LIMIT $3", repository, query.AfterID, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			var model domain.Pipeline
			if err := rows.Scan(&raw); err != nil {
				return err
			}
			if json.Unmarshal(raw, &model) != nil {
				return domain.ErrInvalid
			}
			result.Items = append(result.Items, model)
		}
		return rows.Err()
	})
	if len(result.Items) > limit {
		result.Items = result.Items[:limit]
		result.NextID = result.Items[len(result.Items)-1].ID
	}
	return result, err
}

// ReadDeployments expands bounded deployed-SHA/environment evidence.
func (s *ForgeStore) ReadDeployments(ctx context.Context, subject, repository string, query application.MetadataQuery) (application.DeploymentPage, error) {
	result := application.DeploymentPage{Items: []domain.Deployment{}}
	limit, err := metadataLimit(query)
	if err != nil || query.AfterID < 0 {
		return result, domain.ErrInvalid
	}
	err = s.authorized(ctx, subject, repository, access.ReadIntegration, func(tx pgx.Tx, _ domain.Repository) error {
		rows, err := tx.Query(ctx, "SELECT model FROM forge_deployments WHERE repository_id=$1 AND id>$2 ORDER BY id LIMIT $3", repository, query.AfterID, limit+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			var model domain.Deployment
			if err := rows.Scan(&raw); err != nil {
				return err
			}
			if json.Unmarshal(raw, &model) != nil {
				return domain.ErrInvalid
			}
			result.Items = append(result.Items, model)
		}
		return rows.Err()
	})
	if len(result.Items) > limit {
		result.Items = result.Items[:limit]
		result.NextID = result.Items[len(result.Items)-1].ID
	}
	return result, err
}
