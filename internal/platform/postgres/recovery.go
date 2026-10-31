package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/recovery/domain"
)

// RecoveryStore is an offline stopped-target adapter, never a live API/worker administration surface.
// The host restore coordinator must isolate the destination before calling this transaction.
type RecoveryStore struct{ pool *pgxpool.Pool }

// NewRecoveryStore binds recovery authority to the explicitly selected private destination database.
func NewRecoveryStore(pool *pgxpool.Pool) *RecoveryStore { return &RecoveryStore{pool: pool} }

// RecoveryMetadata describes the exact schema and prior recovery epoch, without provider/capture data or credentials.
type RecoveryMetadata struct {
	Version         int    `json:"version"`
	Schema          string `json:"schema"`
	ParentID        string `json:"parent_id,omitempty"`
	PostgresVersion int    `json:"postgres_version"`
}

// ReadRecoveryMetadata reads one consistent source view while excluding concurrent schema migration.
func ReadRecoveryMetadata(ctx context.Context, pool *pgxpool.Pool) (RecoveryMetadata, error) {
	var m RecoveryMetadata
	tx, e := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return m, e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared(918274610231)`); e != nil {
		return m, e
	}
	if e = verifySchema(ctx, tx); e != nil {
		return m, domain.ErrSchema
	}
	m.Schema, e = SchemaFingerprint()
	if e != nil {
		return m, e
	}
	if e = tx.QueryRow(ctx, `SELECT COALESCE(head_id::text,''),current_setting('server_version_num')::int FROM recovery_state WHERE id=1`).Scan(&m.ParentID, &m.PostgresVersion); e != nil {
		return m, e
	}
	m.Version = 1
	return m, tx.Commit(ctx)
}

// Quarantine commits the authority reset, replay barrier and fresh metadata-only root atomically.
// Immutable captures, provider ciphertext, completed jobs and their historical owners are retained.
func (s *RecoveryStore) Quarantine(ctx context.Context, input domain.Plan) (receipt domain.Receipt, result error) {
	plan, e := input.Normalize(time.Now())
	if e != nil {
		return receipt, e
	}
	defer func() {
		if result != nil && ctx.Err() != nil {
			result = ctx.Err()
			return
		}
		if result != nil && !errors.Is(result, domain.ErrConflict) && !errors.Is(result, domain.ErrSchema) && !errors.Is(result, domain.ErrInvalid) {
			result = domain.ErrUnavailable
		}
	}()
	expected, e := SchemaFingerprint()
	if e != nil {
		return receipt, e
	}
	if plan.Schema != expected {
		return receipt, domain.ErrSchema
	}
	tx, e := s.pool.Begin(ctx)
	if e != nil {
		return receipt, e
	}
	defer tx.Rollback(ctx)
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(918274610231)`); e != nil {
		return receipt, e
	}
	if e = verifySchema(ctx, tx); e != nil {
		return receipt, domain.ErrSchema
	}
	if _, e = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(918274610232)`); e != nil {
		return receipt, e
	}
	if e = directoryLock(ctx, tx); e != nil {
		return receipt, e
	}
	var head string
	if e = tx.QueryRow(ctx, `SELECT COALESCE(head_id::text,'')FROM recovery_state WHERE id=1 FOR UPDATE`).Scan(&head); e != nil {
		return receipt, e
	}
	var hash string
	var raw []byte
	e = tx.QueryRow(ctx, `SELECT fingerprint,receipt FROM recovery_events WHERE id=$1`, plan.ID).Scan(&hash, &raw)
	if e == nil {
		if hash != plan.Fingerprint() || head != plan.ID {
			return receipt, domain.ErrConflict
		}
		if e = json.Unmarshal(raw, &receipt); e != nil {
			return receipt, e
		}
		return receipt, tx.Commit(ctx)
	}
	if !errors.Is(e, pgx.ErrNoRows) {
		return receipt, e
	}
	if head != plan.ParentID {
		return receipt, domain.ErrConflict
	}
	var occupied bool
	e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 UNION ALL SELECT 1 FROM directory_identifiers WHERE kind='user' AND id=$1 UNION ALL SELECT 1 FROM api_tokens WHERE id=$2 OR secret_hash=$3)`, plan.UserID, plan.Key.ID, plan.Key.Hash).Scan(&occupied)
	if e != nil {
		return receipt, e
	}
	if occupied {
		return receipt, domain.ErrConflict
	}
	receipt = domain.Receipt{ID: plan.ID, BackupID: plan.BackupID, UserID: plan.UserID, TokenID: plan.Key.ID}
	if e = tx.QueryRow(ctx, `SELECT now()`).Scan(&receipt.At); e != nil {
		return receipt, e
	}
	receipt.At = receipt.At.UTC()
	change := func(query string, args ...any) (int64, error) {
		tag, e := tx.Exec(ctx, query, args...)
		return tag.RowsAffected(), e
	}
	// Archive displaced role metadata before deleting grants. These tables confer no authority.
	if receipt.Counts.Memberships, e = change(`INSERT INTO recovery_memberships(recovery_id,project_id,user_id,role,version)SELECT $1,project_id,user_id,role,version FROM memberships`, plan.ID); e != nil {
		return receipt, e
	}
	if receipt.Counts.Operators, e = change(`INSERT INTO recovery_operators(recovery_id,user_id)SELECT $1,user_id FROM directory_operators`, plan.ID); e != nil {
		return receipt, e
	}
	for _, query := range []string{`DELETE FROM memberships`, `DELETE FROM directory_operators`, `UPDATE projects SET version=nextval('directory_revision')`} {
		if _, e = tx.Exec(ctx, query); e != nil {
			return receipt, e
		}
	}
	if receipt.Counts.Users, e = change(`UPDATE users SET enabled=false,version=nextval('directory_revision')WHERE enabled`); e != nil {
		return receipt, e
	}
	if receipt.Counts.Tokens, e = change(`UPDATE api_tokens SET revoked_at=now()WHERE revoked_at IS NULL`); e != nil {
		return receipt, e
	}
	if receipt.Counts.Bindings, e = change(`UPDATE identity_bindings SET enabled=false,version=nextval('directory_revision')WHERE enabled`); e != nil {
		return receipt, e
	}
	if _, e = tx.Exec(ctx, `UPDATE identity_sessions SET revoked_at=COALESCE(revoked_at,now())`); e != nil {
		return receipt, e
	}
	for _, query := range []string{`DELETE FROM identity_login_flows`, `DELETE FROM identity_cli_enrollments`} {
		n, err := change(query)
		if err != nil {
			return receipt, err
		}
		receipt.Counts.Flows += n
	}
	if _, e = tx.Exec(ctx, `UPDATE identity_runtime SET configuration='',updated_at=now()WHERE id=1`); e != nil {
		return receipt, e
	}
	if receipt.Counts.GitLabConnections, e = change(`UPDATE forge_connections SET enabled=false,blocked=true,generation=generation+1,lease_owner=NULL,lease_until=NULL,code_lease_owner=NULL,code_lease_until=NULL,code_blocked=true,last_error_code='recovery_quarantine',code_error='recovery_quarantine',updated_at=now()`); e != nil {
		return receipt, e
	}
	if receipt.Counts.TasktopiaConnections, e = change(`UPDATE work_connections SET enabled=false,blocked=true,generation=generation+1,auto_link_merge_requests=false,last_error_code='recovery_quarantine',updated_at=now()`); e != nil {
		return receipt, e
	}
	for _, kind := range []struct {
		filter string
		count  *int64
	}{
		{`kind IN('publication','link')`, &receipt.Counts.TaskWrites}, {`kind='refresh'`, &receipt.Counts.TaskReads},
	} {
		*kind.count, e = change(`UPDATE work_jobs SET state='blocked',lease_owner=NULL,lease_until=NULL,last_error_code='recovery_quarantine'WHERE state='pending'AND ` + kind.filter)
		if e != nil {
			return receipt, e
		}
	}
	if _, e = tx.Exec(ctx, `UPDATE work_jobs SET lease_owner=NULL,lease_until=NULL WHERE lease_owner IS NOT NULL OR lease_until IS NOT NULL`); e != nil {
		return receipt, e
	}
	// Local indexing can resume from durable pending work without inheriting stale process leases.
	for _, table := range []string{"outbox_events", "retrieval_jobs"} {
		if _, e = tx.Exec(ctx, `UPDATE `+table+` SET lease_owner=NULL,lease_until=NULL WHERE completed_at IS NULL`); e != nil {
			return receipt, e
		}
	}
	if _, e = tx.Exec(ctx, `INSERT INTO users(id,display_name)VALUES($1,$2)`, plan.UserID, plan.Name); e != nil {
		return receipt, e
	}
	if e = directoryReserve(ctx, tx, "user", plan.UserID); e != nil {
		return receipt, e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO directory_operators(user_id)VALUES($1)`, plan.UserID); e != nil {
		return receipt, e
	}
	if e = saveCredential(ctx, tx, access.CredentialRecord{ID: plan.Key.ID, UserID: plan.UserID, Label: "Recovery metadata operator", Hash: plan.Key.Hash, Scopes: domain.MetadataScopes(), CreatedAt: plan.Key.CreatedAt, ExpiresAt: plan.Key.ExpiresAt}); e != nil {
		return receipt, e
	}
	raw, e = json.Marshal(receipt)
	if e != nil {
		return receipt, e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO recovery_events(id,backup_id,parent_id,fingerprint,schema_fingerprint,user_id,token_id,receipt)VALUES($1,$2,NULLIF($3,'')::uuid,$4,$5,$6,$7,$8)`, plan.ID, plan.BackupID, plan.ParentID, plan.Fingerprint(), plan.Schema, plan.UserID, plan.Key.ID, raw); e != nil {
		return receipt, e
	}
	if _, e = tx.Exec(ctx, `UPDATE recovery_state SET head_id=$1 WHERE id=1`, plan.ID); e != nil {
		return receipt, e
	}
	return receipt, tx.Commit(ctx)
}
