package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/capture/application"
	"workgraph.local/platform/internal/capture/domain"
)

// CaptureStore persists revisions and their outbox work in one authorization-locked transaction.
type CaptureStore struct{ pool *pgxpool.Pool }

// NewCaptureStore constructs a PostgreSQL capture adapter using an existing managed pool.
func NewCaptureStore(pool *pgxpool.Pool) *CaptureStore { return &CaptureStore{pool: pool} }

// WithinAuthorized holds membership, user and repository share locks until commit or rollback.
func (s *CaptureStore) WithinAuthorized(ctx context.Context, subject, repository string, permission access.Permission, fn func(application.Transaction) error) error {
	if !domain.ValidID(subject) || !domain.ValidID(repository) {
		return access.ErrDenied
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var role string
	err = tx.QueryRow(ctx, `SELECT m.role FROM memberships m
		JOIN repositories r ON r.project_id=m.project_id
		JOIN users u ON u.id=m.user_id
		WHERE r.id=$1 AND m.user_id=$2 AND u.enabled
		FOR SHARE OF m,r,u`, repository, subject).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return access.ErrDenied
	}
	if err != nil {
		return err
	}
	if !access.Allows(role, permission) {
		return access.ErrDenied
	}
	if err := fn(&captureTransaction{tx: tx, repository: repository, subject: subject, permission: permission}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type captureTransaction struct {
	tx         pgx.Tx
	repository string
	subject    string
	permission access.Permission
}

func (t *captureTransaction) PutRevision(ctx context.Context, session domain.Session) (bool, error) {
	snapshot := session.Snapshot()
	if t.permission != access.WriteCapture || snapshot.RepositoryID != t.repository || session.Digest() == "" {
		return false, access.ErrDenied
	}
	identity, err := sessionIdentity(snapshot)
	if err != nil {
		return false, err
	}
	_, err = t.tx.Exec(ctx, `INSERT INTO capture_sessions(repository_id,session_id,originator_id,identity_digest,latest_revision)
		VALUES($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`, t.repository, snapshot.SessionID, t.subject, identity, snapshot.Revision)
	if err != nil {
		return false, err
	}
	var existingIdentity, originator string
	err = t.tx.QueryRow(ctx, `SELECT identity_digest,originator_id::text FROM capture_sessions
		WHERE repository_id=$1 AND session_id=$2 FOR UPDATE`, t.repository, snapshot.SessionID).Scan(&existingIdentity, &originator)
	if err != nil {
		return false, err
	}
	if originator != t.subject {
		return false, access.ErrDenied
	}
	if identity != existingIdentity {
		return false, application.ErrConflict
	}
	var existingDigest string
	err = t.tx.QueryRow(ctx, `SELECT digest FROM capture_revisions WHERE repository_id=$1 AND session_id=$2 AND revision=$3`, t.repository, snapshot.SessionID, snapshot.Revision).Scan(&existingDigest)
	if err == nil {
		if existingDigest != session.Digest() {
			return false, application.ErrConflict
		}
		return false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, err
	}
	_, err = t.tx.Exec(ctx, `INSERT INTO capture_revisions(repository_id,session_id,revision,digest,canonical) VALUES($1,$2,$3,$4,$5)`, t.repository, snapshot.SessionID, snapshot.Revision, session.Digest(), session.CanonicalJSON())
	if err != nil {
		return false, err
	}
	if len(snapshot.CommitRanges) > 0 {
		shas, bases := []string{}, []string{}
		starts, ends := []int32{}, []int32{}
		for _, interval := range snapshot.CommitRanges {
			shas = append(shas, interval.CommitSHA)
			bases = append(bases, interval.Basis)
			starts = append(starts, int32(interval.Start))
			ends = append(ends, int32(interval.End))
		}
		if _, err = t.tx.Exec(ctx, `INSERT INTO capture_commit_ranges(repository_id,session_id,revision,sha,start_message,end_message,basis) SELECT $1,$2,$3,sha,start_message,end_message,basis FROM unnest($4::text[],$5::int[],$6::int[],$7::text[]) AS x(sha,start_message,end_message,basis)`, t.repository, snapshot.SessionID, snapshot.Revision, shas, starts, ends, bases); err != nil {
			return false, err
		}
	}

	// Bulk inserts keep a maximum-size capture independent of network round-trip count.
	var country, task any
	if snapshot.Task != nil {
		country, task = snapshot.Task.CountryID, snapshot.Task.TaskID
	}
	title := snapshot.Agent.Name
	if len(snapshot.Messages) > 0 {
		r := []rune(snapshot.Messages[0].Text)
		if len(r) > 160 {
			r = r[:160]
		}
		if strings.TrimSpace(string(r)) != "" {
			title = string(r)
		}
	}
	_, err = t.tx.Exec(ctx, `INSERT INTO capture_context_scopes(repository_id,session_id,started_at_text,agent_name,country_id,task_id,title) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(repository_id,session_id) DO UPDATE SET title=EXCLUDED.title WHERE (SELECT latest_revision FROM capture_sessions WHERE repository_id=$1 AND session_id=$2)<=$8`, t.repository, snapshot.SessionID, snapshot.StartedAt.UTC().Format(time.RFC3339Nano), snapshot.Agent.Name, country, task, title, snapshot.Revision)
	if err != nil {
		return false, err
	}
	if c := snapshot.Continuation; c != nil {
		_, err = t.tx.Exec(ctx, `INSERT INTO capture_continuations(repository_id,session_id,root_session_id,previous_session_id,previous_revision,previous_digest,segment,reason) VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT DO NOTHING`, t.repository, snapshot.SessionID, c.RootSessionID, c.PreviousSessionID, c.PreviousRevision, c.PreviousDigest, c.Segment, c.Reason)
		if err != nil {
			return false, err
		}
	}
	// Consistent SHA ordering also avoids inverse lock order across concurrent sessions.
	if _, err := t.tx.Exec(ctx, `INSERT INTO commits(repository_id,sha)
		SELECT $1,sha FROM unnest($2::text[]) AS sha ORDER BY sha ON CONFLICT DO NOTHING`, t.repository, snapshot.Commits); err != nil {
		return false, err
	}
	if _, err := t.tx.Exec(ctx, `INSERT INTO capture_revision_commits(repository_id,session_id,revision,sha)
		SELECT $1,$2,$3,sha FROM unnest($4::text[]) AS sha`, t.repository, snapshot.SessionID, snapshot.Revision, snapshot.Commits); err != nil {
		return false, err
	}
	_, err = t.tx.Exec(ctx, "UPDATE capture_sessions SET latest_revision=GREATEST(latest_revision,$3) WHERE repository_id=$1 AND session_id=$2", t.repository, snapshot.SessionID, snapshot.Revision)
	return err == nil, err
}

func (t *captureTransaction) AddEvent(ctx context.Context, event application.CaptureEvent) error {
	if t.permission != access.WriteCapture || event.RepositoryID != t.repository {
		return access.ErrDenied
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	var outboxID int64
	err = t.tx.QueryRow(ctx, `INSERT INTO outbox_events(repository_id,event_key,event_type,payload)
 VALUES($1,$2,'capture.revision_recorded',$3) RETURNING id`, t.repository, fmt.Sprintf("capture:%s:%d", event.SessionID, event.Revision), payload).Scan(&outboxID)
	if err != nil {
		return err
	}
	return enqueueCaptureIndex(ctx, t.tx, event.RepositoryID, event.SessionID, event.Revision, outboxID)
}

func (t *captureTransaction) GetRevision(ctx context.Context, sessionID string, revision int64) (application.StoredRevision, error) {
	var canonical []byte
	var digest string
	err := t.tx.QueryRow(ctx, `SELECT canonical,digest FROM capture_revisions
		WHERE repository_id=$1 AND session_id=$2 AND ($3::bigint=0 OR revision=$3)
		ORDER BY revision DESC LIMIT 1`, t.repository, sessionID, revision).Scan(&canonical, &digest)
	if errors.Is(err, pgx.ErrNoRows) {
		return application.StoredRevision{}, application.ErrNotFound
	}
	if err != nil {
		return application.StoredRevision{}, err
	}
	var snapshot domain.Snapshot
	if err := json.Unmarshal(canonical, &snapshot); err != nil {
		return application.StoredRevision{}, fmt.Errorf("decode stored capture: %w", err)
	}
	session, err := domain.NewSession(snapshot)
	if err != nil {
		return application.StoredRevision{}, fmt.Errorf("validate stored capture: %w", err)
	}
	if session.Digest() != digest {
		return application.StoredRevision{}, errors.New("stored capture digest mismatch")
	}
	rows, err := t.tx.Query(ctx, `SELECT rc.sha FROM capture_revision_commits rc
		JOIN commits c ON c.repository_id=rc.repository_id AND c.sha=rc.sha
		WHERE rc.repository_id=$1 AND rc.session_id=$2 AND rc.revision=$3 AND c.confirmed_at IS NOT NULL ORDER BY rc.sha`, t.repository, sessionID, snapshot.Revision)
	if err != nil {
		return application.StoredRevision{}, err
	}
	defer rows.Close()
	confirmed := []string{}
	for rows.Next() {
		var sha string
		if err := rows.Scan(&sha); err != nil {
			return application.StoredRevision{}, err
		}
		confirmed = append(confirmed, sha)
	}
	if err := rows.Err(); err != nil {
		return application.StoredRevision{}, err
	}
	rows.Close()
	verified, err := t.parentReferenceVerified(ctx, session)
	return application.StoredRevision{Session: session, ConfirmedCommits: confirmed, ParentReferenceVerified: verified}, err
}

func sessionIdentity(snapshot domain.Snapshot) (string, error) {
	identity := struct {
		StartedAt    time.Time            `json:"started_at"`
		Agent        string               `json:"agent"`
		Task         *domain.TaskRef      `json:"task"`
		Continuation *domain.Continuation `json:"continuation,omitempty"`
		Origin       *domain.ImportOrigin `json:"origin,omitempty"`
	}{snapshot.StartedAt, snapshot.Agent.Name, snapshot.Task, snapshot.Continuation, snapshot.Origin}
	bytes, err := json.Marshal(identity)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(bytes)
	return hex.EncodeToString(sum[:]), nil
}

func (t *captureTransaction) parentReferenceVerified(ctx context.Context, child domain.Session) (bool, error) {
	c := child.Snapshot().Continuation
	if c == nil {
		return false, nil
	}
	var raw []byte
	var digest string
	err := t.tx.QueryRow(ctx, `SELECT p.canonical,p.digest FROM capture_revisions p JOIN capture_sessions ps ON ps.repository_id=p.repository_id AND ps.session_id=p.session_id JOIN capture_sessions cs ON cs.repository_id=ps.repository_id AND cs.session_id=$3 AND cs.originator_id=ps.originator_id WHERE p.repository_id=$1 AND p.session_id=$2 AND p.revision=$4`, t.repository, c.PreviousSessionID, child.Snapshot().SessionID, c.PreviousRevision).Scan(&raw, &digest)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var input domain.Snapshot
	if json.Unmarshal(raw, &input) != nil {
		return false, domain.ErrInvalid
	}
	parent, err := domain.NewSession(input)
	if err != nil || parent.Digest() != digest {
		return false, domain.ErrInvalid
	}
	return child.MatchesParent(parent), nil
}
