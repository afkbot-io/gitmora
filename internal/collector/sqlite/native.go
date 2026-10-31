package sqlite

import (
	"database/sql"
	"errors"

	"workgraph.local/platform/internal/collector/application"
	"workgraph.local/platform/internal/collector/domain"
)

func (t *transaction) Stream(key string) (application.NativeStream, bool, error) {
	var s application.NativeStream
	err := t.tx.QueryRowContext(t.ctx, `SELECT root_key,active_key FROM native_streams WHERE native_key=?`, key).Scan(&s.RootKey, &s.ActiveKey)
	if errors.Is(err, sql.ErrNoRows) {
		return s, false, nil
	}
	return s, err == nil, err
}
func (t *transaction) PublishedDirty() ([]application.RecordingEntry, error) {
	return t.list("dirty=1 AND publication_allowed=1")
}
func (t *transaction) NativePage(key, page string) (string, bool, error) {
	var local string
	err := t.tx.QueryRowContext(t.ctx, `SELECT local_key FROM native_pages WHERE native_key=? AND page_id=?`, key, page).Scan(&local)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return local, err == nil, err
}
func (t *transaction) FindSession(id string) (application.RecordingEntry, bool, error) {
	var entry application.RecordingEntry
	var raw []byte
	err := t.tx.QueryRowContext(t.ctx, `SELECT local_key,state FROM recordings WHERE session_id=?`, id).Scan(&entry.Key, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return entry, false, nil
	}
	if err != nil {
		return entry, false, err
	}
	entry.Recording, err = decodeRecording(raw)
	return entry, err == nil, err
}
func (t *transaction) SaveNative(key, page, root, local string, advance bool) error {
	_, err := t.tx.ExecContext(t.ctx, `INSERT INTO native_streams(native_key,root_key,active_key) VALUES(?,?,?) ON CONFLICT(native_key) DO UPDATE SET active_key=CASE WHEN ? THEN excluded.active_key ELSE native_streams.active_key END WHERE native_streams.root_key=excluded.root_key`, key, root, local, advance)
	if err != nil {
		return err
	}
	actual, ok, err := t.Stream(key)
	if err != nil {
		return err
	}
	if !ok || actual.RootKey != root {
		return domain.ErrConflict
	}
	if _, err = t.tx.ExecContext(t.ctx, `INSERT INTO native_pages(native_key,page_id,local_key) VALUES(?,?,?) ON CONFLICT(native_key,page_id) DO UPDATE SET local_key=excluded.local_key`, key, page, local); err != nil {
		return err
	}
	if _, err = t.tx.ExecContext(t.ctx, `INSERT OR IGNORE INTO native_members(native_key,local_key) VALUES(?,?),(?,?)`, key, root, key, local); err != nil {
		return err
	}
	return t.allowNativeStreams()
}
func (t *transaction) NativeEvent(key, event string) (string, string, bool, error) {
	var id, digest string
	err := t.tx.QueryRowContext(t.ctx, `SELECT session_id,digest FROM native_events WHERE native_key=? AND event_key=?`, key, event).Scan(&id, &digest)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	return id, digest, err == nil, err
}
func (t *transaction) RememberNativeEvent(key, event, digest, id string) error {
	_, err := t.tx.ExecContext(t.ctx, `INSERT INTO native_events(native_key,event_key,digest,session_id) VALUES(?,?,?,?)`, key, event, digest, id)
	return err
}
func (t *transaction) allowNativeStreams() error {
	_, err := t.tx.ExecContext(t.ctx, `UPDATE recordings SET publication_allowed=1 WHERE publication_allowed=0 AND local_key IN(
 SELECT n.local_key FROM native_members n WHERE n.native_key IN(SELECT p.native_key FROM native_members p JOIN recordings r ON r.local_key=p.local_key WHERE r.publication_allowed=1))`)
	if err != nil {
		return err
	}
	_, err = t.tx.ExecContext(t.ctx, `UPDATE outbox SET eligible=1 WHERE eligible=0 AND session_id IN(SELECT session_id FROM recordings WHERE publication_allowed=1)`)
	return err
}
