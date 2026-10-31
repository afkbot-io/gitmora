package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/forge/application"
	"workgraph.local/platform/internal/forge/domain"
	"workgraph.local/platform/internal/identifier"
)

func enqueueCodeCommit(ctx context.Context, tx pgx.Tx, repo, sha string) error {
	_, err := tx.Exec(ctx, `INSERT INTO forge_code_runs(repository_id,sha) SELECT repository_id,sha FROM commits WHERE repository_id=$1 AND sha=$2 AND confirmed_at IS NOT NULL ON CONFLICT DO NOTHING`, repo, sha)
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE forge_connections SET code_next_at=LEAST(code_next_at,now()) WHERE repository_id=$1`, repo)
	}
	return err
}

const codeRunDue = `x.available_at<=now() AND ((NOT x.discovery_ended AND NOT x.local_limit) OR EXISTS(SELECT 1 FROM forge_code_paths p WHERE p.repository_id=x.repository_id AND p.sha=x.sha AND p.status IN('pending','missing') AND p.retry_at<=now()))`

// ClaimCode leases a repository's independent code lane; provider I/O holds no database lock.
func (s *ForgeStore) ClaimCode(ctx context.Context) (application.CodeWork, bool, error) {
	var w application.CodeWork
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return w, false, err
	}
	defer tx.Rollback(ctx)
	err = tx.QueryRow(ctx, `SELECT r.id::text,r.gitlab_url,r.gitlab_project_id,c.generation,c.secret_envelope FROM forge_connections c JOIN repositories r ON r.id=c.repository_id
 WHERE c.enabled AND NOT c.blocked AND NOT c.code_blocked AND c.code_next_at<=now() AND (c.code_lease_until IS NULL OR c.code_lease_until<=now())
 AND (EXISTS(SELECT 1 FROM forge_code_runs x WHERE x.repository_id=c.repository_id AND `+codeRunDue+`)
 OR(c.code_probe_at<=now() AND EXISTS(SELECT 1 FROM forge_code_snapshots f JOIN forge_code_runs x ON x.repository_id=f.repository_id AND x.sha=f.sha WHERE f.repository_id=c.repository_id AND x.provider_available)))
 ORDER BY c.code_next_at,c.repository_id LIMIT 1 FOR UPDATE OF c SKIP LOCKED FOR SHARE OF r`).Scan(&w.Repository.ID, &w.Repository.BaseURL, &w.Repository.ProjectID, &w.Generation, &w.Envelope)
	if errors.Is(err, pgx.ErrNoRows) {
		return w, false, nil
	}
	if err != nil {
		return w, false, err
	}
	var ended bool
	var lane int
	err = tx.QueryRow(ctx, `SELECT x.sha,x.next_page,x.version,x.discovery_ended OR x.local_limit,x.lane FROM forge_code_runs x WHERE x.repository_id=$1 AND `+codeRunDue+` ORDER BY x.available_at,x.sha LIMIT 1 FOR UPDATE`, w.Repository.ID).Scan(&w.SHA, &w.Page, &w.Version, &ended, &lane)
	if errors.Is(err, pgx.ErrNoRows) {
		w.Probe = true
		err = tx.QueryRow(ctx, `SELECT f.sha,f.path,x.version FROM forge_code_snapshots f JOIN forge_code_runs x ON x.repository_id=f.repository_id AND x.sha=f.sha WHERE f.repository_id=$1 AND x.provider_available ORDER BY f.observed_at DESC,f.sha,f.path LIMIT 1`, w.Repository.ID).Scan(&w.SHA, &w.Path, &w.Version)
	} else if err == nil && (ended || lane == 1) {
		var path string
		e := tx.QueryRow(ctx, `SELECT path FROM forge_code_paths WHERE repository_id=$1 AND sha=$2 AND status IN('pending','missing') AND retry_at<=now() ORDER BY retry_at,path LIMIT 1`, w.Repository.ID, w.SHA).Scan(&path)
		if e == nil {
			w.Path = path
		} else if !errors.Is(e, pgx.ErrNoRows) {
			return w, false, e
		}
	}
	if err != nil {
		return w, false, err
	}
	if ended && w.Path == "" && !w.Probe {
		return w, false, domain.ErrConflict
	}
	w.Owner, err = identifier.New()
	if err != nil {
		return w, false, err
	}
	_, err = tx.Exec(ctx, `UPDATE forge_connections SET code_lease_owner=$2,code_lease_until=now()+interval '60 seconds' WHERE repository_id=$1`, w.Repository.ID, w.Owner)
	if err != nil {
		return w, false, err
	}
	err = tx.Commit(ctx)
	return w, err == nil, err
}

// FinishCode fences against rotation/disable/catalog change and atomically stores projections plus index jobs.
func (s *ForgeStore) FinishCode(ctx context.Context, w application.CodeWork, out application.CodeOutcome) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var repo string
	err = tx.QueryRow(ctx, `SELECT c.repository_id::text FROM forge_connections c JOIN repositories r ON r.id=c.repository_id WHERE c.repository_id=$1 AND c.enabled AND NOT c.blocked AND NOT c.code_blocked AND c.generation=$2 AND c.code_lease_owner=$3 AND c.code_lease_until>now() AND r.gitlab_url=$4 AND r.gitlab_project_id=$5 FOR UPDATE OF c FOR SHARE OF r`, w.Repository.ID, w.Generation, w.Owner, w.Repository.BaseURL, w.Repository.ProjectID).Scan(&repo)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrConflict
	}
	if err != nil {
		return err
	}
	var version int64
	var page, attempts int
	err = tx.QueryRow(ctx, `SELECT version,next_page,attempts FROM forge_code_runs WHERE repository_id=$1 AND sha=$2 FOR UPDATE`, repo, w.SHA).Scan(&version, &page, &attempts)
	if err != nil {
		return err
	}
	if version != w.Version || w.Path == "" && page != w.Page {
		return domain.ErrConflict
	}
	if out.Code != "" {
		delay := out.RetryAfter
		if delay <= 0 {
			delay = time.Duration(1<<min(attempts, 8)) * 5 * time.Second
		}
		delay = min(delay, time.Hour)
		// File-local transient failures back off that path while other due work progresses.
		fileLocal := w.Path != "" && !w.Probe && !out.Blocked && out.Code != "provider_rate_limited"
		if fileLocal {
			_, err = tx.Exec(ctx, `UPDATE forge_code_paths SET retry_at=now()+$4*interval '1 second',reason=$5 WHERE repository_id=$1 AND sha=$2 AND path=$3`, repo, w.SHA, w.Path, delay.Seconds(), out.Code)
			if err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `UPDATE forge_code_runs SET attempts=attempts+1,last_error_code=$3,available_at=CASE WHEN $5 THEN now() ELSE now()+$4*interval '1 second' END WHERE repository_id=$1 AND sha=$2`, repo, w.SHA, out.Code, delay.Seconds(), fileLocal)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE forge_connections SET code_lease_owner=NULL,code_lease_until=NULL,code_error=$2,code_blocked=$3,code_next_at=CASE WHEN $5 THEN now() ELSE now()+$4*interval '1 second' END WHERE repository_id=$1`, repo, out.Code, out.Blocked, delay.Seconds(), fileLocal)
		if err != nil {
			return err
		}
		return tx.Commit(ctx)
	}
	code := ""
	checked := false
	if w.Path == "" {
		if out.Diff == nil || out.File != nil || len(out.Diff.Files) > 50 || out.Diff.NextPage != 0 && out.Diff.NextPage != w.Page+1 {
			return domain.ErrInvalid
		}
		if out.Diff.Missing {
			code = "code_commit_unavailable"
			_, err = tx.Exec(ctx, `UPDATE forge_code_runs SET provider_available=false,available_at=now()+interval '15 minutes',last_error_code=$3 WHERE repository_id=$1 AND sha=$2`, repo, w.SHA, code)
		} else {
			var count int
			if err = tx.QueryRow(ctx, `SELECT count(*) FROM forge_code_paths WHERE repository_id=$1 AND sha=$2`, repo, w.SHA).Scan(&count); err != nil {
				return err
			}
			limited := false
			for _, file := range out.Diff.Files {
				if !identifier.ValidGitPath(file.Path) || !slices.Contains([]string{"", "deleted", "symlink", "submodule", "unsupported_mode", "unsupported_path", "sensitive_path"}, file.Skip) {
					return domain.ErrInvalid
				}
				status := "pending"
				if file.Skip != "" {
					status = "skipped"
				}
				if count >= 10000 {
					limited = true
					break
				}
				var tag pgconn.CommandTag
				tag, err = tx.Exec(ctx, `INSERT INTO forge_code_paths(repository_id,sha,path,status,reason) VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, repo, w.SHA, file.Path, status, file.Skip)
				count += int(tag.RowsAffected())
				if err != nil {
					return err
				}
				if status == "pending" {
					_, err = tx.Exec(ctx, `UPDATE forge_code_paths SET status='pending',reason='',retry_at=now() WHERE repository_id=$1 AND sha=$2 AND path=$3 AND status='missing'`, repo, w.SHA, file.Path)
					if err != nil {
						return err
					}
				}
			}
			_, err = tx.Exec(ctx, `UPDATE forge_code_runs SET next_page=CASE WHEN $3>0 THEN $3 ELSE next_page END,discovery_ended=$3=0 AND NOT $4,local_limit=$4,provider_available=true,lane=1,available_at=now(),attempts=0,last_error_code=NULL WHERE repository_id=$1 AND sha=$2`, repo, w.SHA, out.Diff.NextPage, limited)
		}
	} else {
		if out.File == nil || out.Diff != nil {
			return domain.ErrInvalid
		}
		file := out.File
		status, reason := "stored", ""
		if file.Missing {
			status, reason = "missing", "path_unavailable"
		} else if file.Skip != "" {
			if !slices.Contains([]string{"too_large", "binary_or_encoding", "lfs_pointer", "redaction_expansion_limit"}, file.Skip) {
				return domain.ErrInvalid
			}
			status, reason = "skipped", file.Skip
			checked = true
		} else {
			if file.File == nil {
				return domain.ErrInvalid
			}
			f := file.File
			if f.Identity.RepositoryID != repo || f.Identity.BaseURL != w.Repository.BaseURL || f.Identity.ProjectID != w.Repository.ProjectID || f.Identity.CommitSHA != w.SHA || f.Identity.Path != w.Path || f.ValidateRetained() != nil {
				return domain.ErrInvalid
			}
			digest := f.ProjectionDigest()
			var existing string
			e := tx.QueryRow(ctx, `SELECT digest FROM forge_code_snapshots WHERE repository_id=$1 AND sha=$2 AND path=$3`, repo, w.SHA, w.Path).Scan(&existing)
			if e == nil && existing != digest {
				return domain.ErrConflict
			}
			if e != nil && !errors.Is(e, pgx.ErrNoRows) {
				return e
			}
			raw, e := json.Marshal(f)
			if e != nil {
				return e
			}
			_, err = tx.Exec(ctx, `INSERT INTO forge_code_snapshots(repository_id,sha,path,digest,blob_id,provider_base_url,provider_project_id,model) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT DO NOTHING`, repo, w.SHA, w.Path, digest, f.Identity.BlobID, f.Identity.BaseURL, f.Identity.ProjectID, raw)
			if err != nil {
				return err
			}
			if err = enqueueCodeIndex(ctx, tx, repo, w.SHA, w.Path, digest); err != nil {
				return err
			}
			checked = true
		}
		_, err = tx.Exec(ctx, `UPDATE forge_code_paths SET status=$4,reason=$5,observed_at=now(),retry_at=CASE WHEN $4='missing' THEN now()+interval '15 minutes' ELSE now() END WHERE repository_id=$1 AND sha=$2 AND path=$3`, repo, w.SHA, w.Path, status, reason)
		if err != nil {
			return err
		}
		if w.Probe && file.Missing {
			_, err = tx.Exec(ctx, `UPDATE forge_code_runs SET provider_available=false,discovery_ended=false,local_limit=false,next_page=1,available_at=now()+interval '15 minutes',last_error_code='code_commit_unavailable' WHERE repository_id=$1 AND sha=$2`, repo, w.SHA)
		} else {
			_, err = tx.Exec(ctx, `UPDATE forge_code_runs SET lane=0,available_at=now(),attempts=0,last_error_code=NULL,provider_available=true WHERE repository_id=$1 AND sha=$2`, repo, w.SHA)
		}
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `UPDATE forge_connections SET code_lease_owner=NULL,code_lease_until=NULL,code_error=NULLIF($2,''),code_checked_at=CASE WHEN $3 THEN now() ELSE code_checked_at END,code_probe_at=CASE WHEN $3 THEN now()+interval '5 minutes' WHEN $4 THEN now()+interval '15 seconds' ELSE code_probe_at END,
 code_next_at=LEAST(COALESCE((SELECT min(GREATEST(x.available_at,CASE WHEN NOT x.discovery_ended AND NOT x.local_limit THEN x.available_at ELSE (SELECT min(p.retry_at) FROM forge_code_paths p WHERE p.repository_id=x.repository_id AND p.sha=x.sha AND p.status IN('pending','missing')) END)) FROM forge_code_runs x WHERE x.repository_id=$1 AND ((NOT x.discovery_ended AND NOT x.local_limit) OR EXISTS(SELECT 1 FROM forge_code_paths p WHERE p.repository_id=x.repository_id AND p.sha=x.sha AND p.status IN('pending','missing')))),now()+interval '5 minutes'),CASE WHEN $3 THEN now()+interval '5 minutes' WHEN $4 THEN now()+interval '15 seconds' ELSE code_probe_at END) WHERE repository_id=$1`, repo, code, checked, w.Probe)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// CodeStatus returns source coverage and safe health even when retained bodies are unavailable.
func (s *ForgeStore) CodeStatus(ctx context.Context, user, repo, sha string) (application.CodeStatus, error) {
	result := application.CodeStatus{RepositoryID: repo, CommitSHA: sha, SkipReasons: map[string]int{}, DiffLimitsApply: true}
	if !identifier.ValidCommit(sha) {
		return result, domain.ErrInvalid
	}
	err := s.authorized(ctx, user, repo, access.ReadIntegration, func(tx pgx.Tx, _ domain.Repository) error {
		err := tx.QueryRow(ctx, `SELECT x.discovery_ended,x.local_limit,c.enabled,x.provider_available,COALESCE(x.last_error_code,c.code_error,''),c.code_blocked OR c.blocked,c.code_checked_at FROM forge_code_runs x JOIN forge_connections c ON c.repository_id=x.repository_id WHERE x.repository_id=$1 AND x.sha=$2`, repo, sha).Scan(&result.DiscoveryEnded, &result.LocalLimit, &result.Enabled, &result.ProviderAvailable, &result.LastError, &result.Blocked, &result.PermissionCheckedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNotFound
		}
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT status,reason,count(*) FROM forge_code_paths WHERE repository_id=$1 AND sha=$2 GROUP BY status,reason`, repo, sha)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var status, reason string
			var n int
			if err = rows.Scan(&status, &reason, &n); err != nil {
				return err
			}
			switch status {
			case "pending":
				result.Pending += n
			case "stored":
				result.Stored += n
			case "skipped":
				result.Skipped += n
				result.SkipReasons[reason] += n
			case "missing":
				result.Missing += n
			}
		}
		return rows.Err()
	})
	return result, err
}

// ReadCode rechecks current catalog identity, source availability and a ten-minute repository permission lease.
func (s *ForgeStore) ReadCode(ctx context.Context, user, repo, sha, path string) (application.CodeFileView, error) {
	var result application.CodeFileView
	if !identifier.ValidCommit(sha) || !identifier.ValidGitPath(path) {
		return result, domain.ErrInvalid
	}
	err := s.authorized(ctx, user, repo, access.ReadIntegration, func(tx pgx.Tx, r domain.Repository) error {
		var raw []byte
		err := tx.QueryRow(ctx, `SELECT r.project_id::text,f.model,f.digest,f.observed_at FROM forge_code_snapshots f JOIN repositories r ON r.id=f.repository_id JOIN forge_connections c ON c.repository_id=f.repository_id JOIN forge_code_runs x ON x.repository_id=f.repository_id AND x.sha=f.sha JOIN forge_code_paths p ON p.repository_id=f.repository_id AND p.sha=f.sha AND p.path=f.path
  WHERE f.repository_id=$1 AND f.sha=$2 AND f.path=$3 AND f.provider_base_url=$4 AND f.provider_project_id=$5 AND c.enabled AND NOT c.blocked AND NOT c.code_blocked AND c.code_checked_at>now()-interval '10 minutes' AND x.provider_available AND p.status='stored'`, repo, sha, path, r.BaseURL, r.ProjectID).Scan(&result.ProjectID, &raw, &result.Digest, &result.ObservedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNotFound
		}
		if err != nil {
			return err
		}
		if json.Unmarshal(raw, &result.File) != nil || result.File.ValidateRetained() != nil || result.File.ProjectionDigest() != result.Digest {
			return domain.ErrInvalid
		}
		return nil
	})
	return result, err
}

// RetryCode invalidates in-flight work and schedules provider re-enumeration without deleting snapshots.
func (s *ForgeStore) RetryCode(ctx context.Context, user, repo, sha string) error {
	if !identifier.ValidCommit(sha) {
		return domain.ErrInvalid
	}
	return s.authorized(ctx, user, repo, access.ManageIntegration, func(tx pgx.Tx, _ domain.Repository) error {
		if err := enqueueCodeCommit(ctx, tx, repo, sha); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE forge_code_runs SET version=version+1,next_page=1,discovery_ended=false,local_limit=false,available_at=now(),attempts=0,last_error_code=NULL WHERE repository_id=$1 AND sha=$2`, repo, sha)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return domain.ErrNotFound
		}
		if _, err = tx.Exec(ctx, `UPDATE forge_code_paths SET status='pending',reason='',retry_at=now() WHERE repository_id=$1 AND sha=$2 AND status IN('stored','missing','pending')`, repo, sha); err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE forge_connections SET code_blocked=false,code_error=NULL,code_next_at=now(),code_probe_at=now(),code_checked_at=NULL,code_lease_owner=NULL,code_lease_until=NULL WHERE repository_id=$1`, repo)
		return err
	})
}

var _ application.CodeStore = (*ForgeStore)(nil)
