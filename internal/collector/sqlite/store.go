// Package sqlite implements the private, process-safe local collector ledger.
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"time"

	_ "modernc.org/sqlite"
	capture "workgraph.local/platform/internal/capture/domain"
	"workgraph.local/platform/internal/collector/application"
	"workgraph.local/platform/internal/collector/domain"
)

// Store persists sanitized recordings and immutable delivery revisions across CLI processes.
type Store struct{ db *sql.DB }

// Open creates a private WAL ledger, refuses symlinks/future schemas and configures immediate writes.
func Open(ctx context.Context, path string) (*Store, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	directory := filepath.Dir(absolute)
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	if info, err := os.Lstat(directory); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, domain.ErrInvalid
	}
	if err := os.Chmod(directory, 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(absolute, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0600)
	if err == nil {
		_ = file.Close()
	} else if !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	if info, err := os.Lstat(absolute); err != nil || !info.Mode().IsRegular() {
		return nil, domain.ErrInvalid
	}
	if err := os.Chmod(absolute, 0600); err != nil {
		return nil, err
	}
	uri := url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}
	query := url.Values{"_pragma": []string{"busy_timeout(5000)", "journal_mode(WAL)", "synchronous(FULL)", "foreign_keys(1)"}, "_txlock": []string{"immediate"}}
	uri.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db}
	if err := store.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

// Close releases this process's handles without discarding durable state.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > 5 {
		return errors.New("local ledger schema is newer than this client")
	}
	if version == 5 {
		return tx.Commit()
	}
	if version == 0 {
		_, err = tx.ExecContext(ctx, `CREATE TABLE config(id INTEGER PRIMARY KEY CHECK(id=1),data TEXT NOT NULL);
	CREATE TABLE bindings(worktree TEXT PRIMARY KEY,task TEXT NOT NULL);
	CREATE TABLE recordings(local_key TEXT PRIMARY KEY,session_id TEXT NOT NULL UNIQUE,worktree TEXT NOT NULL,dirty INTEGER NOT NULL,state BLOB NOT NULL);
	CREATE INDEX recording_worktree ON recordings(worktree,dirty);
	CREATE TABLE recording_commits(session_id TEXT NOT NULL REFERENCES recordings(session_id) ON DELETE CASCADE,sha TEXT NOT NULL,PRIMARY KEY(session_id,sha));
	CREATE INDEX recording_commit_sha ON recording_commits(sha,session_id);
	CREATE TABLE recording_pending_files(session_id TEXT NOT NULL REFERENCES recordings(session_id) ON DELETE CASCADE,file TEXT NOT NULL,PRIMARY KEY(session_id,file));
	CREATE INDEX recording_pending_path ON recording_pending_files(file,session_id);
	CREATE TABLE events(session_id TEXT NOT NULL REFERENCES recordings(session_id),event_key TEXT NOT NULL,digest TEXT NOT NULL,PRIMARY KEY(session_id,event_key));
	CREATE TABLE revisions(session_id TEXT NOT NULL REFERENCES recordings(session_id),revision INTEGER NOT NULL,digest TEXT NOT NULL,canonical BLOB NOT NULL,delivered_at INTEGER,PRIMARY KEY(session_id,revision));
	CREATE TABLE revision_commits(session_id TEXT NOT NULL,revision INTEGER NOT NULL,sha TEXT NOT NULL,PRIMARY KEY(session_id,revision,sha),FOREIGN KEY(session_id,revision) REFERENCES revisions(session_id,revision) ON DELETE CASCADE);
	CREATE INDEX revision_commit_sha ON revision_commits(sha,session_id);
	CREATE TABLE outbox(id INTEGER PRIMARY KEY AUTOINCREMENT,session_id TEXT NOT NULL,revision INTEGER NOT NULL,digest TEXT NOT NULL,eligible INTEGER NOT NULL DEFAULT 0,attempts INTEGER NOT NULL DEFAULT 0,blocked INTEGER NOT NULL DEFAULT 0,available_at INTEGER NOT NULL DEFAULT 0,last_error TEXT,UNIQUE(session_id,revision),FOREIGN KEY(session_id,revision) REFERENCES revisions(session_id,revision));
	CREATE INDEX outbox_due ON outbox(eligible,blocked,available_at,id);
	PRAGMA user_version=1;`)
		if err != nil {
			return err
		}
	}
	if version < 2 {
		_, err = tx.ExecContext(ctx, `ALTER TABLE recordings ADD COLUMN publication_allowed INTEGER NOT NULL DEFAULT 0 CHECK(publication_allowed IN(0,1));
	CREATE TABLE native_pins(native_key TEXT PRIMARY KEY,local_key TEXT NOT NULL REFERENCES recordings(local_key) ON DELETE CASCADE);
	CREATE TABLE publication_commits(sha TEXT PRIMARY KEY);
	UPDATE recordings SET publication_allowed=1 WHERE session_id IN(SELECT session_id FROM outbox WHERE eligible=1 UNION SELECT session_id FROM revisions WHERE delivered_at IS NOT NULL);
	INSERT OR IGNORE INTO publication_commits(sha) SELECT DISTINCT rc.sha FROM revision_commits rc JOIN recordings r ON r.session_id=rc.session_id WHERE r.publication_allowed=1;
	PRAGMA user_version=2;`)
		if err != nil {
			return err
		}
	}
	if version < 3 {
		_, err = tx.ExecContext(ctx, `CREATE TABLE native_streams(native_key TEXT PRIMARY KEY,root_key TEXT NOT NULL REFERENCES recordings(local_key),active_key TEXT NOT NULL REFERENCES recordings(local_key));
 CREATE TABLE native_pages(native_key TEXT NOT NULL REFERENCES native_streams(native_key),page_id TEXT NOT NULL,local_key TEXT NOT NULL REFERENCES recordings(local_key),PRIMARY KEY(native_key,page_id));
 CREATE TABLE native_members(native_key TEXT NOT NULL REFERENCES native_streams(native_key),local_key TEXT NOT NULL REFERENCES recordings(local_key),PRIMARY KEY(native_key,local_key));
 CREATE INDEX native_member_key ON native_members(local_key,native_key);
 CREATE TABLE native_events(native_key TEXT NOT NULL REFERENCES native_streams(native_key),event_key TEXT NOT NULL,digest TEXT NOT NULL,session_id TEXT NOT NULL REFERENCES recordings(session_id),PRIMARY KEY(native_key,event_key));
 PRAGMA user_version=3;`)
		if err != nil {
			return err
		}
	}
	if version < 4 {
		_, err = tx.ExecContext(ctx, `CREATE TABLE native_frontier_sources(native_key TEXT PRIMARY KEY,data BLOB NOT NULL);
 CREATE TABLE native_frontiers(id TEXT PRIMARY KEY,native_key TEXT NOT NULL REFERENCES native_frontier_sources(native_key),worktree TEXT NOT NULL,status TEXT NOT NULL CHECK(status IN('started','ended','bound','applied','abandoned')),data BLOB NOT NULL);
 CREATE INDEX native_frontier_pending ON native_frontiers(worktree,status,id);
 PRAGMA user_version=4;`)
		if err != nil {
			return err
		}
	}
	// Origin-bearing recording JSON requires a reader/writer that preserves those immutable fields.
	if _, err = tx.ExecContext(ctx, "PRAGMA user_version=5"); err != nil {
		return err
	}
	return tx.Commit()
}

// Configure creates an immutable credential-free repository binding, or acknowledges an identical setup.
func (s *Store) Configure(ctx context.Context, input domain.Config) error {
	config, err := domain.NormalizeConfig(input)
	if err != nil {
		return err
	}
	raw, _ := json.Marshal(config)
	return s.Within(ctx, func(tx application.Transaction) error {
		concrete := tx.(*transaction)
		var existing []byte
		err := concrete.tx.QueryRowContext(ctx, "SELECT data FROM config WHERE id=1").Scan(&existing)
		if err == nil {
			var stored domain.Config
			if json.Unmarshal(existing, &stored) != nil || !stored.SameConnection(config) {
				return domain.ErrConflict
			}
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		_, err = concrete.tx.ExecContext(ctx, "INSERT INTO config(id,data) VALUES(1,?)", raw)
		return err
	})
}

// Config reads and revalidates persisted configuration.
func (s *Store) Config(ctx context.Context) (domain.Config, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx, "SELECT data FROM config WHERE id=1").Scan(&raw)
	return decodeConfig(raw, err)
}

// Within holds a short immediate transaction; application callbacks must perform no network I/O.
func (s *Store) Within(ctx context.Context, fn func(application.Transaction) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(&transaction{tx: tx, ctx: ctx}); err != nil {
		return err
	}
	return tx.Commit()
}

type transaction struct {
	tx  *sql.Tx
	ctx context.Context
}

func (t *transaction) Config() (domain.Config, error) {
	var raw []byte
	err := t.tx.QueryRowContext(t.ctx, "SELECT data FROM config WHERE id=1").Scan(&raw)
	return decodeConfig(raw, err)
}
func decodeConfig(raw []byte, err error) (domain.Config, error) {
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Config{}, domain.ErrNotInitialized
	}
	if err != nil {
		return domain.Config{}, err
	}
	var config domain.Config
	if json.Unmarshal(raw, &config) != nil {
		return domain.Config{}, domain.ErrInvalid
	}
	return domain.NormalizeConfig(config)
}

func (t *transaction) Binding(worktree string) (*capture.TaskRef, error) {
	var raw []byte
	err := t.tx.QueryRowContext(t.ctx, "SELECT task FROM bindings WHERE worktree=?", worktree).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var task capture.TaskRef
	if json.Unmarshal(raw, &task) != nil || !capture.ValidID(task.CountryID) || !capture.ValidID(task.TaskID) {
		return nil, domain.ErrInvalid
	}
	return &task, nil
}
func (t *transaction) Bind(worktree string, task *capture.TaskRef) error {
	if _, err := t.Config(); err != nil {
		return err
	}
	if task == nil {
		_, err := t.tx.ExecContext(t.ctx, "DELETE FROM bindings WHERE worktree=?", worktree)
		return err
	}
	raw, err := json.Marshal(task)
	if err != nil {
		return err
	}
	_, err = t.tx.ExecContext(t.ctx, "INSERT INTO bindings(worktree,task) VALUES(?,?) ON CONFLICT(worktree) DO UPDATE SET task=excluded.task", worktree, string(raw))
	return err
}
func (t *transaction) Find(key string) (*domain.Recording, bool, error) {
	var raw []byte
	err := t.tx.QueryRowContext(t.ctx, "SELECT state FROM recordings WHERE local_key=?", key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	recording, err := decodeRecording(raw)
	return recording, err == nil, err
}
func (t *transaction) NativePin(key string) (string, bool, error) {
	var local string
	err := t.tx.QueryRowContext(t.ctx, "SELECT local_key FROM native_pins WHERE native_key=?", key).Scan(&local)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return local, err == nil, err
}
func (t *transaction) PinNative(key, local string) error {
	if _, err := t.tx.ExecContext(t.ctx, "INSERT INTO native_pins(native_key,local_key) VALUES(?,?) ON CONFLICT DO NOTHING", key, local); err != nil {
		return err
	}
	actual, exists, err := t.NativePin(key)
	if err != nil {
		return err
	}
	if !exists || actual != local {
		return domain.ErrConflict
	}
	return nil
}
func (t *transaction) PublicationAllowed(sessionID string) (bool, error) {
	var allowed bool
	err := t.tx.QueryRowContext(t.ctx, "SELECT publication_allowed FROM recordings WHERE session_id=?", sessionID).Scan(&allowed)
	return allowed, err
}
func (t *transaction) ListDirty(worktree string) ([]application.RecordingEntry, error) {
	return t.list("dirty=1 AND worktree=?", worktree)
}
func (t *transaction) Observed(worktree string, paths []string) ([]application.RecordingEntry, error) {
	raw, _ := json.Marshal(paths)
	return t.list("worktree=? AND session_id IN(SELECT session_id FROM recording_pending_files WHERE file IN(SELECT value FROM json_each(?)))", worktree, string(raw))
}
func (t *transaction) Linked(commits []string, dirtyOnly bool) ([]application.RecordingEntry, error) {
	raw, _ := json.Marshal(commits)
	return t.list("(?=0 OR dirty=1) AND session_id IN(SELECT session_id FROM recording_commits WHERE sha IN(SELECT value FROM json_each(?)))", dirtyOnly, string(raw))
}
func (t *transaction) list(where string, args ...any) ([]application.RecordingEntry, error) {
	// where consists only of adapter-owned constant SQL, never caller input.
	rows, err := t.tx.QueryContext(t.ctx, "SELECT local_key,state FROM recordings WHERE "+where+" ORDER BY local_key", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []application.RecordingEntry{}
	for rows.Next() {
		var key string
		var raw []byte
		if err := rows.Scan(&key, &raw); err != nil {
			return nil, err
		}
		recording, err := decodeRecording(raw)
		if err != nil {
			return nil, err
		}
		entries = append(entries, application.RecordingEntry{Key: key, Recording: recording})
	}
	return entries, rows.Err()
}
func decodeRecording(raw []byte) (*domain.Recording, error) {
	var state domain.State
	if json.Unmarshal(raw, &state) != nil {
		return nil, domain.ErrInvalid
	}
	return domain.Restore(state)
}
func (t *transaction) Save(key string, recording *domain.Recording) error {
	state := recording.State()
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	result, err := t.tx.ExecContext(t.ctx, `INSERT INTO recordings(local_key,session_id,worktree,dirty,state) VALUES(?,?,?,?,?)
		ON CONFLICT(local_key) DO UPDATE SET state=excluded.state,dirty=excluded.dirty WHERE recordings.session_id=excluded.session_id AND recordings.worktree=excluded.worktree`, key, state.Snapshot.SessionID, state.Worktree, state.Dirty, raw)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return domain.ErrConflict
	}
	commits, _ := json.Marshal(state.Snapshot.Commits)
	pending, _ := json.Marshal(state.PendingFiles)
	if _, err := t.tx.ExecContext(t.ctx, "INSERT OR IGNORE INTO recording_commits(session_id,sha) SELECT ?,value FROM json_each(?)", state.Snapshot.SessionID, string(commits)); err != nil {
		return err
	}
	if _, err := t.tx.ExecContext(t.ctx, "DELETE FROM recording_pending_files WHERE session_id=?", state.Snapshot.SessionID); err != nil {
		return err
	}
	if _, err := t.tx.ExecContext(t.ctx, "INSERT INTO recording_pending_files(session_id,file) SELECT ?,value FROM json_each(?)", state.Snapshot.SessionID, string(pending)); err != nil {
		return err
	}
	return nil
}
func (t *transaction) EventDigest(session, key string) (string, bool, error) {
	var digest string
	err := t.tx.QueryRowContext(t.ctx, "SELECT digest FROM events WHERE session_id=? AND event_key=?", session, key).Scan(&digest)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return digest, err == nil, err
}
func (t *transaction) RememberEvent(session, key, digest string) error {
	_, err := t.tx.ExecContext(t.ctx, "INSERT INTO events(session_id,event_key,digest) VALUES(?,?,?)", session, key, digest)
	return err
}
func (t *transaction) Enqueue(session capture.Session) error {
	snapshot := session.Snapshot()
	commits, _ := json.Marshal(snapshot.Commits)
	_, err := t.tx.ExecContext(t.ctx, "INSERT INTO revisions(session_id,revision,digest,canonical) VALUES(?,?,?,?) ON CONFLICT DO NOTHING", snapshot.SessionID, snapshot.Revision, session.Digest(), session.CanonicalJSON())
	if err != nil {
		return err
	}
	var digest string
	var delivered sql.NullInt64
	if err := t.tx.QueryRowContext(t.ctx, "SELECT digest,delivered_at FROM revisions WHERE session_id=? AND revision=?", snapshot.SessionID, snapshot.Revision).Scan(&digest, &delivered); err != nil {
		return err
	}
	if digest != session.Digest() {
		return domain.ErrConflict
	}
	if _, err := t.tx.ExecContext(t.ctx, "INSERT OR IGNORE INTO revision_commits(session_id,revision,sha) SELECT ?,?,value FROM json_each(?)", snapshot.SessionID, snapshot.Revision, string(commits)); err != nil {
		return err
	}
	if delivered.Valid {
		return nil
	}
	if _, err := t.tx.ExecContext(t.ctx, "UPDATE recordings SET publication_allowed=1 WHERE session_id=? AND EXISTS(SELECT 1 FROM publication_commits WHERE sha IN(SELECT value FROM json_each(?)))", snapshot.SessionID, string(commits)); err != nil {
		return err
	}
	if err := t.allowNativeStreams(); err != nil {
		return err
	}
	if _, err := t.tx.ExecContext(t.ctx, "UPDATE outbox SET eligible=1 WHERE session_id=? AND EXISTS(SELECT 1 FROM recordings WHERE session_id=? AND publication_allowed=1)", snapshot.SessionID, snapshot.SessionID); err != nil {
		return err
	}
	_, err = t.tx.ExecContext(t.ctx, "INSERT INTO outbox(session_id,revision,digest,eligible) SELECT ?,?,?,publication_allowed FROM recordings WHERE session_id=? ON CONFLICT DO NOTHING", snapshot.SessionID, snapshot.Revision, session.Digest(), snapshot.SessionID)
	return err
}
func (t *transaction) Eligible(commits []string, worktree string) error {
	if worktree != "" {
		if len(commits) > 0 {
			return domain.ErrInvalid
		}
		if _, err := t.tx.ExecContext(t.ctx, "UPDATE recordings SET publication_allowed=1 WHERE worktree=?", worktree); err != nil {
			return err
		}
	} else {
		raw, _ := json.Marshal(commits)
		if _, err := t.tx.ExecContext(t.ctx, "INSERT OR IGNORE INTO publication_commits(sha) SELECT value FROM json_each(?)", string(raw)); err != nil {
			return err
		}
		if _, err := t.tx.ExecContext(t.ctx, `UPDATE recordings SET publication_allowed=1 WHERE session_id IN(SELECT session_id FROM revision_commits WHERE sha IN(SELECT value FROM json_each(?)))`, string(raw)); err != nil {
			return err
		}
	}
	if err := t.allowNativeStreams(); err != nil {
		return err
	}
	_, err := t.tx.ExecContext(t.ctx, "UPDATE outbox SET eligible=1 WHERE session_id IN(SELECT session_id FROM recordings WHERE publication_allowed=1)")
	return err
}

// Pending reads at most 64 due eligible records and verifies their immutable content.
func (s *Store) Pending(ctx context.Context, now time.Time, limit int) ([]application.QueuedRevision, error) {
	if limit < 1 || limit > 64 {
		return nil, domain.ErrInvalid
	}
	rows, err := s.db.QueryContext(ctx, `SELECT r.canonical,r.digest,o.attempts FROM outbox o JOIN revisions r ON r.session_id=o.session_id AND r.revision=o.revision
		WHERE o.eligible=1 AND o.blocked=0 AND o.available_at<=?
 AND NOT COALESCE((SELECT json_extract(data,'$.disabled') FROM config WHERE id=1),0)
 AND NOT EXISTS(SELECT 1 FROM config c,json_each(c.data,'$.disabled_agents') a WHERE c.id=1 AND a.value=json_extract(r.canonical,'$.agent.name'))
 ORDER BY o.id LIMIT ?`, now.UnixMilli(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	queued := []application.QueuedRevision{}
	for rows.Next() {
		var canonical []byte
		var digest string
		var attempts int
		if err := rows.Scan(&canonical, &digest, &attempts); err != nil {
			return nil, err
		}
		var snapshot capture.Snapshot
		if json.Unmarshal(canonical, &snapshot) != nil {
			return nil, domain.ErrInvalid
		}
		session, err := capture.NewSession(snapshot)
		if err != nil || session.Digest() != digest {
			return nil, domain.ErrInvalid
		}
		queued = append(queued, application.QueuedRevision{Session: session, Attempts: attempts})
	}
	return queued, rows.Err()
}

// Ack removes only the exact queued revision; an older acknowledgement cannot touch newer work.
func (s *Store) Ack(ctx context.Context, session capture.Session) (bool, error) {
	snapshot := session.Snapshot()
	acknowledged := false
	err := s.Within(ctx, func(tx application.Transaction) error {
		concrete := tx.(*transaction)
		result, err := concrete.tx.ExecContext(ctx, "DELETE FROM outbox WHERE session_id=? AND revision=? AND digest=?", snapshot.SessionID, snapshot.Revision, session.Digest())
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count == 0 {
			return nil
		}
		acknowledged = true
		_, err = concrete.tx.ExecContext(ctx, "UPDATE revisions SET delivered_at=? WHERE session_id=? AND revision=? AND digest=?", time.Now().UnixMilli(), snapshot.SessionID, snapshot.Revision, session.Digest())
		return err
	})
	return acknowledged, err
}

var safeCode = regexp.MustCompile(`^[a-z_]{1,32}$`)

// Fail retains failed delivery with bounded diagnostic, retry schedule and optional blocked state.
func (s *Store) Fail(ctx context.Context, session capture.Session, code string, blocked bool, next time.Time) error {
	if !safeCode.MatchString(code) {
		code = "unavailable"
	}
	snapshot := session.Snapshot()
	_, err := s.db.ExecContext(ctx, "UPDATE outbox SET attempts=attempts+1,last_error=?,blocked=?,available_at=? WHERE session_id=? AND revision=? AND digest=?", code, blocked, next.UnixMilli(), snapshot.SessionID, snapshot.Revision, session.Digest())
	return err
}

// Retry explicitly releases queued failures for operator-driven retry.
func (s *Store) Retry(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "UPDATE outbox SET blocked=0,available_at=0")
	return err
}

// Stats returns queue/recording counts without transcript or token contents.
func (s *Store) Stats(ctx context.Context) (application.Stats, error) {
	var stats application.Stats
	err := s.db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM recordings),(SELECT count(*) FROM outbox),(SELECT count(*) FROM outbox WHERE eligible=1 AND blocked=0),(SELECT count(*) FROM outbox WHERE blocked=1)`).Scan(&stats.Recordings, &stats.Pending, &stats.Eligible, &stats.Blocked)
	return stats, err
}
