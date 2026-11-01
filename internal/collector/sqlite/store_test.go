package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	capture "workgraph.local/platform/internal/capture/domain"
	"workgraph.local/platform/internal/collector/application"
	"workgraph.local/platform/internal/collector/domain"
	"workgraph.local/platform/internal/collector/sqlite"
)

var config = domain.Config{Version: 1, RepositoryID: "11111111-1111-4111-8111-111111111111", ServerURL: "http://127.0.0.1:8080", Remote: "origin", RemoteURL: "/synthetic/bare.git", TokenEnv: "WORKGRAPH_TOKEN"}

func observation() domain.Observation {
	return domain.Observation{SchemaVersion: 1, SourceSessionID: "native", EventID: "turn-1", Agent: capture.Agent{Name: "local-agent", Model: "local-model"}, Messages: []capture.Message{{Role: "user", Text: "DB_PASSWORD=never-on-disk"}}, ChangedFiles: []string{"a.go", "b.go"}}
}
func open(t *testing.T, path string) *sqlite.Store {
	t.Helper()
	store, err := sqlite.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestRecordingQueueSurvivesRestartAndAckOnlyDeletesItsExactRevision(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "private", "state.sqlite")
	store := open(t, path)
	if err := store.Configure(ctx, config); err != nil {
		t.Fatal(err)
	}
	service := application.NewService(store, nil, time.Now)
	id, err := service.Record(ctx, "tree", observation())
	if err != nil {
		t.Fatal(err)
	}
	if again, err := service.Record(ctx, "tree", observation()); err != nil || again != id {
		t.Fatal("event retry changed identity")
	}
	sha := strings.Repeat("a", 40)
	if err := service.Commit(ctx, "tree", sha, []string{"a.go"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := service.PreparePush(ctx, []string{sha}); err != nil {
		t.Fatal(err)
	}
	queued, err := store.Pending(ctx, time.Now(), 16)
	if err != nil || len(queued) != 1 {
		t.Fatalf("pending: %d %v", len(queued), err)
	}
	old := queued[0].Session
	input := observation()
	input.EventID = "turn-2"
	input.Messages = append(input.Messages, capture.Message{Role: "assistant", Text: "Second observation"})
	input.ChangedFiles = nil
	if _, err := service.Record(ctx, "tree", input); err != nil {
		t.Fatal(err)
	}
	if err := service.PreparePush(ctx, []string{sha}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store = open(t, path)
	wrong := old.Snapshot()
	wrong.Messages = append(wrong.Messages, capture.Message{Role: "assistant", Text: "Wrong acknowledgement content"})
	wrongSession, err := capture.NewSession(wrong)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := store.Ack(ctx, wrongSession); err != nil || ok {
		t.Fatal("wrong digest acknowledged persisted evidence")
	}
	if ok, err := store.Ack(ctx, old); err != nil || !ok {
		t.Fatal("exact ACK failed")
	}
	queued, err = store.Pending(ctx, time.Now(), 16)
	if err != nil || len(queued) != 1 || queued[0].Session.Snapshot().Revision != 2 {
		t.Fatal("old ACK removed a newer revision")
	}
	if ok, err := store.Ack(ctx, old); err != nil || ok {
		t.Fatal("duplicate ACK affected another revision")
	}
	for _, entry := range queued {
		if strings.Contains(string(entry.Session.CanonicalJSON()), "never-on-disk") {
			t.Fatal("secret reached persisted evidence")
		}
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		bytes, err := os.ReadFile(path + suffix)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if strings.Contains(string(bytes), "never-on-disk") {
			t.Fatal("raw secret reached SQLite files")
		}
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm()&0077 != 0 {
		t.Fatal("database is not private")
	}
}

func TestBindingWorktreesAndNativeReplayConflictsAreIsolated(t *testing.T) {
	ctx := context.Background()
	store := open(t, filepath.Join(t.TempDir(), "state.sqlite"))
	if err := store.Configure(ctx, config); err != nil {
		t.Fatal(err)
	}
	service := application.NewService(store, nil, time.Now)
	first, err := service.Record(ctx, "tree-a", observation())
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.Record(ctx, "tree-b", observation())
	if err != nil || first == second {
		t.Fatal("native session leaked across worktrees")
	}
	task := &capture.TaskRef{CountryID: config.RepositoryID, TaskID: "22222222-2222-4222-8222-222222222222"}
	if err := bindLegacyFixture(ctx, store, "tree-a", task); err != nil {
		t.Fatal(err)
	}
	third, err := service.Record(ctx, "tree-a", observation())
	if err != nil || third == first {
		t.Fatal("new task retargeted an existing recording")
	}
	changed := observation()
	changed.Messages[0].Text = "Different evidence"
	if _, err := service.Record(ctx, "tree-a", changed); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("event identity reused: %v", err)
	}
	if err := service.Commit(ctx, "tree-a", strings.Repeat("a", 40), []string{"a.go"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := service.PreparePush(ctx, []string{strings.Repeat("a", 40)}); err != nil {
		t.Fatal(err)
	}
	queue, err := store.Pending(ctx, time.Now(), 16)
	if err != nil || len(queue) != 2 {
		t.Fatal("commit associated another worktree")
	}
	for _, entry := range queue {
		if entry.Session.Snapshot().SessionID == second {
			t.Fatal("other worktree captured this commit")
		}
	}
}

func TestConcurrentConnectionsReplayOnceAndConfigCannotRetargetQueue(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.sqlite")
	first := open(t, path)
	if err := first.Configure(ctx, config); err != nil {
		t.Fatal(err)
	}
	second := open(t, path)
	var wg sync.WaitGroup
	failures := make(chan error, 20)
	for i := range 20 {
		wg.Go(func() {
			store := first
			if i%2 == 1 {
				store = second
			}
			_, err := application.NewService(store, nil, time.Now).Record(ctx, "tree", observation())
			if err != nil {
				failures <- err
			}
		})
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	stats, err := first.Stats(ctx)
	if err != nil || stats.Recordings != 1 {
		t.Fatal("concurrent replay created multiple recordings")
	}
	other := config
	other.RepositoryID = "22222222-2222-4222-8222-222222222222"
	if err := first.Configure(ctx, other); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("installation silently changed repository identity")
	}
}

func TestUncommittedDeliveryDoesNotSelectAnotherWorktree(t *testing.T) {
	ctx := context.Background()
	store := open(t, filepath.Join(t.TempDir(), "state.sqlite"))
	if err := store.Configure(ctx, config); err != nil {
		t.Fatal(err)
	}
	service := application.NewService(store, nil, time.Now)
	first, err := service.Record(ctx, "tree-a", observation())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Record(ctx, "tree-b", observation()); err != nil {
		t.Fatal(err)
	}
	if err := service.Checkpoint(ctx, "tree-b", false); err != nil {
		t.Fatal(err)
	}
	if err := service.Checkpoint(ctx, "tree-a", true); err != nil {
		t.Fatal(err)
	}
	queue, err := store.Pending(ctx, time.Now(), 16)
	if err != nil {
		t.Fatal(err)
	}
	if len(queue) != 1 || queue[0].Session.Snapshot().SessionID != first {
		t.Fatal("explicit current-worktree delivery selected unrelated uncommitted context")
	}
}

func TestLateConversationUpdateIsPublishedOnlyForPreviouslyAuthorizedSession(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.sqlite")
	store := open(t, path)
	if err := store.Configure(ctx, config); err != nil {
		t.Fatal(err)
	}
	service := application.NewService(store, nil, time.Now)
	id, err := service.Record(ctx, "tree-a", observation())
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("a", 40)
	if err := service.Commit(ctx, "tree-a", sha, []string{"a.go"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := service.PreparePush(ctx, []string{sha}); err != nil {
		t.Fatal(err)
	}
	queued, err := store.Pending(ctx, time.Now(), 16)
	if err != nil || len(queued) != 1 {
		t.Fatal("initial context not eligible")
	}
	if _, err := store.Ack(ctx, queued[0].Session); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store = open(t, path)
	service = application.NewService(store, nil, time.Now)
	late := observation()
	late.EventID = "after-push"
	late.ChangedFiles = nil
	late.Messages = append(late.Messages, capture.Message{Role: "assistant", Text: "Final summary after git push"})
	if _, err := service.Record(ctx, "tree-a", late); err != nil {
		t.Fatal(err)
	}
	private := observation()
	private.SourceSessionID = "unpublished-same-worktree"
	private.EventID = "private"
	if _, err := service.Record(ctx, "tree-a", private); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Record(ctx, "tree-b", observation()); err != nil {
		t.Fatal(err)
	}
	queued, err = store.Pending(ctx, time.Now(), 16)
	if err != nil || len(queued) != 1 || queued[0].Session.Snapshot().SessionID != id || queued[0].Session.Snapshot().Revision != 2 {
		t.Fatal("late update was lost or unrelated private context became eligible")
	}
}

func TestNativeConversationKeepsItsTaskBindingAcrossRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.sqlite")
	store := open(t, path)
	if err := store.Configure(ctx, config); err != nil {
		t.Fatal(err)
	}
	service := application.NewService(store, nil, time.Now)
	firstTask := &capture.TaskRef{CountryID: config.RepositoryID, TaskID: "22222222-2222-4222-8222-222222222222"}
	secondTask := &capture.TaskRef{CountryID: config.RepositoryID, TaskID: "33333333-3333-4333-8333-333333333333"}
	if err := bindLegacyFixture(ctx, store, "tree", firstTask); err != nil {
		t.Fatal(err)
	}
	first, err := service.RecordNative(ctx, "tree", observation())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store = open(t, path)
	service = application.NewService(store, nil, time.Now)
	if err := bindLegacyFixture(ctx, store, "tree", secondTask); err != nil {
		t.Fatal(err)
	}
	late := observation()
	late.EventID = "late"
	late.Messages = append(late.Messages, capture.Message{Role: "assistant", Text: "Completed original task"})
	late.ChangedFiles = nil
	if got, err := service.RecordNative(ctx, "tree", late); err != nil || got != first {
		t.Fatal("native session was retargeted to current task")
	}
	other := observation()
	other.SourceSessionID = "new-conversation"
	if got, err := service.RecordNative(ctx, "tree", other); err != nil || got == first {
		t.Fatal("new native conversation reused old task")
	}
	if err := service.Checkpoint(ctx, "tree", true); err != nil {
		t.Fatal(err)
	}
	queue, err := store.Pending(ctx, time.Now(), 16)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range queue {
		snapshot := entry.Session.Snapshot()
		if snapshot.SessionID == first && snapshot.Task.TaskID != firstTask.TaskID {
			t.Fatal("pinned task changed")
		}
	}
}

func TestNativeHistoricalFileEventsDoNotAttributeLaterHumanEditAfterRestart(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.sqlite")
	store := open(t, path)
	if err := store.Configure(ctx, config); err != nil {
		t.Fatal(err)
	}
	service := application.NewService(store, nil, time.Now)
	input := observation()
	input.ChangedFiles = nil
	input.FileEvents = []domain.FileEvent{{ID: "native-tool-1", Path: "a.go"}}
	if _, err := service.RecordNative(ctx, "tree", input); err != nil {
		t.Fatal(err)
	}
	first := strings.Repeat("a", 40)
	if err := service.Commit(ctx, "tree", first, []string{"a.go"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store = open(t, path)
	service = application.NewService(store, nil, time.Now)
	input.EventID = "native-late"
	input.Messages = append(input.Messages, capture.Message{Role: "assistant", Text: "Final reply"})
	if _, err := service.RecordNative(ctx, "tree", input); err != nil {
		t.Fatal(err)
	}
	if err := service.Commit(ctx, "tree", strings.Repeat("b", 40), []string{"a.go"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := service.Checkpoint(ctx, "tree", true); err != nil {
		t.Fatal(err)
	}
	queue, err := store.Pending(ctx, time.Now(), 16)
	if err != nil || len(queue) != 2 {
		t.Fatal("native revisions missing")
	}
	last := queue[len(queue)-1].Session.Snapshot()
	if len(last.Commits) != 1 || last.Commits[0] != first {
		t.Fatal("replayed native history attributed an unrelated human edit")
	}
}

func TestVersionOneMigrationRetainsDeliveredConsentWithoutPublishingPrivateRecordings(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.sqlite")
	store := open(t, path)
	if err := store.Configure(ctx, config); err != nil {
		t.Fatal(err)
	}
	service := application.NewService(store, nil, time.Now)
	id, err := service.Record(ctx, "published", observation())
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("a", 40)
	if err := service.Commit(ctx, "published", sha, []string{"a.go"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := service.PreparePush(ctx, []string{sha}); err != nil {
		t.Fatal(err)
	}
	queue, err := store.Pending(ctx, time.Now(), 16)
	if err != nil || len(queue) != 1 {
		t.Fatal("missing initial queue")
	}
	if _, err := store.Ack(ctx, queue[0].Session); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Record(ctx, "private", observation()); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = legacy.Exec(`DROP TABLE native_frontiers; DROP TABLE native_frontier_sources; DROP TABLE native_events; DROP TABLE native_pages; DROP TABLE native_members; DROP TABLE native_streams; DROP TABLE native_pins; DROP TABLE publication_commits; ALTER TABLE recordings DROP COLUMN publication_allowed; PRAGMA user_version=1;`)
	if err != nil {
		_ = legacy.Close()
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	store = open(t, path)
	service = application.NewService(store, nil, time.Now)
	late := observation()
	late.EventID = "migrated-late"
	late.ChangedFiles = nil
	late.Messages = append(late.Messages, capture.Message{Role: "assistant", Text: "Late after upgrade"})
	if _, err := service.Record(ctx, "published", late); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Record(ctx, "private", late); err != nil {
		t.Fatal(err)
	}
	queue, err = store.Pending(ctx, time.Now(), 16)
	if err != nil || len(queue) != 1 || queue[0].Session.Snapshot().SessionID != id || queue[0].Session.Snapshot().Revision != 2 {
		t.Fatal("upgrade lost publication consent or published private work")
	}
}

type failingUploader struct {
	err    error
	onSend func(capture.Session)
}

func (u failingUploader) Send(_ context.Context, session capture.Session) error {
	if u.onSend != nil {
		u.onSend(session)
	}
	return u.err
}

func TestDeliveryFailuresRetryWithoutHoldingLocalTransaction(t *testing.T) {
	ctx := context.Background()
	store := open(t, filepath.Join(t.TempDir(), "state.sqlite"))
	if err := store.Configure(ctx, config); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	clock := func() time.Time { return now }
	service := application.NewService(store, nil, clock)
	if _, err := service.Record(ctx, "tree", observation()); err != nil {
		t.Fatal(err)
	}
	if err := service.Checkpoint(ctx, "tree", true); err != nil {
		t.Fatal(err)
	}
	failure := &application.DeliveryError{Code: "unavailable", Retryable: true}
	service = application.NewService(store, failingUploader{err: failure, onSend: func(capture.Session) {
		input := observation()
		input.EventID = "during-network"
		if _, err := application.NewService(store, nil, clock).Record(ctx, "other-tree", input); err != nil {
			t.Fatal("network held recording transaction", err)
		}
	}}, clock)
	result, err := service.Sync(ctx)
	if err != nil || result.Failed != 1 || result.Stats.Pending != 1 {
		t.Fatal("failed delivery was lost")
	}
	if due, err := store.Pending(ctx, now, 16); err != nil || len(due) != 0 {
		t.Fatal("retry ignored backoff")
	}
	now = now.Add(2 * time.Second)
	service = application.NewService(store, failingUploader{}, clock)
	result, err = service.Sync(ctx)
	if err != nil || result.Sent != 1 || result.Stats.Pending != 0 {
		t.Fatal("retry did not recover")
	}
}

func bindLegacyFixture(ctx context.Context, store *sqlite.Store, worktree string, task *capture.TaskRef) error {
	return store.Within(ctx, func(tx application.Transaction) error {
		return tx.(interface {
			SeedLegacyBinding(string, *capture.TaskRef) error
		}).SeedLegacyBinding(worktree, task)
	})
}
