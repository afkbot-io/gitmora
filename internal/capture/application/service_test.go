package application_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/capture/application"
	"workgraph.local/platform/internal/capture/domain"
)

const actor = "33333333-3333-4333-8333-333333333333"

func validSnapshot() domain.Snapshot {
	return domain.Snapshot{SchemaVersion: 1, RepositoryID: "11111111-1111-4111-8111-111111111111", SessionID: "22222222-2222-4222-8222-222222222222", Revision: 1, Agent: domain.Agent{Name: "agent", Model: "local"}, StartedAt: time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC), Messages: []domain.Message{{Role: "user", Text: "DB_PASSWORD=do-not-store-this"}}, Commits: []string{strings.Repeat("a", 40)}}
}

type recordingStore struct {
	deny       bool
	duplicate  bool
	eventError error
	writes     int
	events     []application.CaptureEvent
	session    domain.Session
	permission access.Permission
}

func (s *recordingStore) WithinAuthorized(ctx context.Context, subject, repository string, permission access.Permission, fn func(application.Transaction) error) error {
	s.permission = permission
	if s.deny {
		return access.ErrDenied
	}
	if subject != actor || repository != validSnapshot().RepositoryID {
		return errors.New("unexpected scope")
	}
	return fn(s)
}

func (s *recordingStore) PutRevision(_ context.Context, session domain.Session) (bool, error) {
	s.writes++
	s.session = session
	return !s.duplicate, nil
}
func (s *recordingStore) AddEvent(_ context.Context, event application.CaptureEvent) error {
	s.events = append(s.events, event)
	return s.eventError
}
func (s *recordingStore) GetRevision(_ context.Context, _ string, _ int64) (application.StoredRevision, error) {
	return application.StoredRevision{Session: s.session}, nil
}

func TestIngestRedactsAndQueuesOnlyNewEvidence(t *testing.T) {
	store := &recordingStore{}
	service := application.NewService(store)
	result, err := service.Ingest(context.Background(), actor, validSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Created || result.Digest == "" || len(store.events) != 1 {
		t.Fatal("new evidence must emit one event")
	}
	if strings.Contains(string(store.session.CanonicalJSON()), "do-not-store-this") {
		t.Fatal("raw secret reached persistence")
	}
	if store.events[0].Digest != result.Digest || store.permission != access.WriteCapture {
		t.Fatal("incorrect event or authorization scope")
	}
	store.duplicate = true
	result, err = service.Ingest(context.Background(), actor, validSnapshot())
	if err != nil || result.Created || len(store.events) != 1 {
		t.Fatal("retry scheduled duplicate work")
	}
}

func TestIngestDenialAndEventFailureAreReturned(t *testing.T) {
	store := &recordingStore{deny: true}
	_, err := application.NewService(store).Ingest(context.Background(), actor, validSnapshot())
	if !errors.Is(err, access.ErrDenied) || store.writes != 0 {
		t.Fatal("denied upload reached storage")
	}
	store.deny = false
	store.eventError = errors.New("outbox unavailable")
	_, err = application.NewService(store).Ingest(context.Background(), actor, validSnapshot())
	if !errors.Is(err, store.eventError) {
		t.Fatal("successful response despite event failure")
	}
}

func TestInvalidIdentityAndSnapshotNeverReachStore(t *testing.T) {
	store := &recordingStore{}
	service := application.NewService(store)
	if _, err := service.Ingest(context.Background(), "", validSnapshot()); !errors.Is(err, access.ErrDenied) {
		t.Fatal("accepted missing subject")
	}
	bad := validSnapshot()
	bad.Commits = []string{"short"}
	if _, err := service.Ingest(context.Background(), actor, bad); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal("accepted invalid capture")
	}
	if store.writes != 0 {
		t.Fatal("invalid request reached storage")
	}
}
