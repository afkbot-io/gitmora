package sqlite_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	capture "workgraph.local/platform/internal/capture/domain"
	"workgraph.local/platform/internal/collector/application"
	"workgraph.local/platform/internal/collector/domain"
)

func nativeWindow(event, page, text string) domain.Observation {
	o := observation()
	o.EventID = event
	o.ChangedFiles = nil
	o.Messages = []capture.Message{{Role: "assistant", Text: text}}
	o.History = &domain.NativeHistory{PageID: strings.Repeat(page, 64), Epoch: strings.Repeat("e", 64), MessageIDs: []string{strings.Repeat(page, 64)}, Reason: "window"}
	return o
}

func TestLateCheckpointSealsOtherAuthorizedDirtyWindows(t *testing.T) {
	ctx := context.Background()
	store := open(t, filepath.Join(t.TempDir(), "state.sqlite"))
	if err := store.Configure(ctx, config); err != nil {
		t.Fatal(err)
	}
	service := application.NewService(store, nil, time.Now)
	source := domain.NativeSource{Worktree: "tree", Agent: "local-agent", SessionID: "native"}
	before, sha := strings.Repeat("b", 40), strings.Repeat("a", 40)
	frontier, err := service.BeginFrontierWithHeads(ctx, source, "late-checkpoint", before, []string{before})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.EndFrontier(ctx, source, "late-checkpoint", sha); err != nil {
		t.Fatal(err)
	}
	root, err := service.RecordNativeCheckpoints(ctx, "tree", nativeWindow("root", "a", "Earlier window"), []domain.CheckpointBinding{{ID: frontier.ID, MessageEnd: 1}})
	if err != nil {
		t.Fatal(err)
	}
	tail, err := service.RecordNative(ctx, "tree", nativeWindow("tail", "b", "Complete tail"))
	if err != nil {
		t.Fatal(err)
	}
	if err := service.PreparePush(ctx, []string{sha}); err != nil {
		t.Fatal(err)
	}
	frontier, err = service.EndFrontier(ctx, source, "late-checkpoint", sha)
	if err != nil || frontier.TargetSessionID != root {
		t.Fatal("late checkpoint moved to another source window", err)
	}
	if err := service.ApplyFrontier(ctx, "tree", frontier, []string{sha}); err != nil {
		t.Fatal(err)
	}
	queue, err := store.Pending(ctx, time.Now(), 64)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range queue {
		if q.Session.Snapshot().SessionID == tail {
			return
		}
	}
	t.Fatal("late checkpoint left another authorized window unsealed")
}

func TestVersionTwoNativePinContinuesWithOriginalTaskAndConsent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.sqlite")
	store := open(t, path)
	if err := store.Configure(ctx, config); err != nil {
		t.Fatal(err)
	}
	service := application.NewService(store, nil, time.Now)
	task := &capture.TaskRef{CountryID: config.RepositoryID, TaskID: "22222222-2222-4222-8222-222222222222"}
	if err := bindLegacyFixture(ctx, store, "tree", task); err != nil {
		t.Fatal(err)
	}
	root, err := seedLegacyNativeRecording(ctx, store, "tree", observation(), task)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Checkpoint(ctx, "tree", true); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`DROP TABLE IF EXISTS git_frontier_scopes; DROP TABLE IF EXISTS git_head_scopes; DROP TABLE native_frontiers; DROP TABLE native_frontier_sources; DROP TABLE native_events; DROP TABLE native_pages; DROP TABLE native_members; DROP TABLE native_streams; PRAGMA user_version=2;`); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	store = open(t, path)
	service = application.NewService(store, nil, time.Now)
	if err := bindLegacyFixture(ctx, store, "tree", nil); err != nil {
		t.Fatal(err)
	}
	child, err := service.RecordNative(ctx, "tree", nativeWindow("upgrade", "a", "New protocol observation"))
	if err != nil || child == root {
		t.Fatal(child, err)
	}
	queue, err := store.Pending(ctx, time.Now(), 64)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range queue {
		snap := q.Session.Snapshot()
		if snap.SessionID == child {
			if snap.Continuation == nil || snap.Continuation.Reason != "protocol_upgrade" || snap.Continuation.RootSessionID != root || snap.Task == nil || *snap.Task != *task {
				t.Fatal("version two pin was retargeted", snap)
			}
			return
		}
	}
	t.Fatal("version two consent was lost")
}

func TestNativeWindowsSurviveRestartCompactionAndKeepConsentTaskAndFileDedup(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.sqlite")
	store := open(t, path)
	if err := store.Configure(ctx, config); err != nil {
		t.Fatal(err)
	}
	service := application.NewService(store, nil, time.Now)
	task := &capture.TaskRef{CountryID: config.RepositoryID, TaskID: "22222222-2222-4222-8222-222222222222"}
	if err := bindLegacyFixture(ctx, store, "tree", task); err != nil {
		t.Fatal(err)
	}
	first := nativeWindow("first", "a", "Original exact tool observation")
	first.FileEvents = []domain.FileEvent{{ID: "edit", Path: "a.go"}}
	root, err := service.RecordNative(ctx, "tree", first)
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("a", 40)
	if err := service.Commit(ctx, "tree", sha, []string{"a.go"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := service.PreparePush(ctx, []string{sha}); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store = open(t, path)
	service = application.NewService(store, nil, time.Now)
	if err := bindLegacyFixture(ctx, store, "tree", &capture.TaskRef{CountryID: config.RepositoryID, TaskID: "33333333-3333-4333-8333-333333333333"}); err != nil {
		t.Fatal(err)
	}
	summary := nativeWindow("summary", "b", "Agent summary after compaction")
	summary.Messages[0].Kind = "agent_summary"
	summary.History.Reason = "compaction"
	summary.History.Epoch = strings.Repeat("f", 64)
	summary.FileEvents = first.FileEvents
	child, err := service.RecordNative(ctx, "tree", summary)
	if err != nil || child == root {
		t.Fatal(child, err)
	}
	if replay, err := service.RecordNative(ctx, "tree", first); err != nil || replay != root {
		t.Fatal("old replay changed mapping", replay, err)
	}
	queue, err := store.Pending(ctx, time.Now(), 64)
	if err != nil || len(queue) != 2 {
		t.Fatal("continuation consent/queue lost", len(queue), err)
	}
	var parent capture.Session
	for _, q := range queue {
		if q.Session.Snapshot().SessionID == root {
			parent = q.Session
		}
	}
	for _, q := range queue {
		snap := q.Session.Snapshot()
		if snap.Task != nil {
			t.Fatal("task retargeted")
		}
		if snap.SessionID == child {
			if !q.Session.MatchesParent(parent) || len(snap.Files) != 0 || snap.Messages[0].Kind != "agent_summary" {
				t.Fatal("continuation lost provenance or replayed file event", snap)
			}
		}
	}
	private := nativeWindow("private", "c", "Private new source")
	private.SourceSessionID = "another-native-source"
	if _, err := service.RecordNative(ctx, "tree", private); err != nil {
		t.Fatal(err)
	}
	if err := service.Checkpoint(ctx, "tree", false); err != nil {
		t.Fatal(err)
	}
	queue, err = store.Pending(ctx, time.Now(), 64)
	if err != nil || len(queue) != 2 {
		t.Fatal("private source inherited consent", len(queue), err)
	}
	changed := summary
	changed.EventID = "edited"
	changed.Messages = []capture.Message{{Role: "assistant", Kind: "agent_summary", Text: "Corrected source summary"}}
	branch, err := service.RecordNative(ctx, "tree", changed)
	if err != nil || branch == child {
		t.Fatal("replacement overwrote original segment", branch, err)
	}
	changed.EventID = summary.EventID
	if _, err := service.RecordNative(ctx, "tree", changed); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("event identity conflict ignored", err)
	}
}

func TestNativePruningPreservesCapturedBytesAndStalePrefixesDoNotRollBack(t *testing.T) {
	ctx := context.Background()
	store := open(t, filepath.Join(t.TempDir(), "state.sqlite"))
	if err := store.Configure(ctx, config); err != nil {
		t.Fatal(err)
	}
	service := application.NewService(store, nil, time.Now)
	o := nativeWindow("tool", "a", "Original bounded result")
	o.Messages[0].Role = "tool"
	o.Messages[0].ToolName = "read"
	id, err := service.RecordNative(ctx, "tree", o)
	if err != nil {
		t.Fatal(err)
	}
	complete := o
	complete.EventID = "complete"
	complete.Messages = append(append([]capture.Message{}, o.Messages...), capture.Message{Role: "assistant", Text: "Later complete message"})
	copyHistory := *o.History
	complete.History = &copyHistory
	complete.History.MessageIDs = []string{strings.Repeat("a", 64), strings.Repeat("b", 64)}
	if got, err := service.RecordNative(ctx, "tree", complete); err != nil || got != id {
		t.Fatal(got, err)
	}
	o.EventID = "pruned"
	o.Messages[0].Text = "[source output pruned]"
	o.History.Pruned = []int{0}
	if got, err := service.RecordNative(ctx, "tree", o); err != nil || got != id {
		t.Fatal(got, err)
	}
	if err := service.Checkpoint(ctx, "tree", true); err != nil {
		t.Fatal(err)
	}
	queue, err := store.Pending(ctx, time.Now(), 64)
	if err != nil || len(queue) != 1 || queue[0].Session.Snapshot().Messages[0].Text != "Original bounded result" || len(queue[0].Session.Snapshot().Messages) != 2 {
		t.Fatal("pruning destroyed captured bytes", queue, err)
	}
}

func TestHumanPushSealsAnUncommittedTailWindowInTheSameAuthorizedStream(t *testing.T) {
	ctx := context.Background()
	store := open(t, filepath.Join(t.TempDir(), "state.sqlite"))
	if err := store.Configure(ctx, config); err != nil {
		t.Fatal(err)
	}
	service := application.NewService(store, nil, time.Now)
	first := nativeWindow("before", "a", "Earlier window with file change")
	first.FileEvents = []domain.FileEvent{{ID: "edit", Path: "a.go"}}
	if _, err := service.RecordNative(ctx, "tree", first); err != nil {
		t.Fatal(err)
	}
	last := nativeWindow("tail", "b", "Final uncommitted tail window")
	tail, err := service.RecordNative(ctx, "tree", last)
	if err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("b", 40)
	if err := service.Commit(ctx, "tree", sha, []string{"a.go"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := service.PreparePush(ctx, []string{sha}); err != nil {
		t.Fatal(err)
	}
	queue, err := store.Pending(ctx, time.Now(), 64)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range queue {
		if q.Session.Snapshot().SessionID == tail {
			return
		}
	}
	t.Fatal("authorized dirty tail never entered the delivery queue")
}

func TestMetadataUpgradeKeepsSealedParentReferencesAndEnrichesOnlyActiveWindow(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.sqlite")
	store := open(t, path)
	if err := store.Configure(ctx, config); err != nil {
		t.Fatal(err)
	}
	service := application.NewService(store, nil, time.Now)
	first := nativeWindow("old-root", "a", "Original parent")
	root, err := service.RecordNative(ctx, "tree", first)
	if err != nil {
		t.Fatal(err)
	}
	tailInput := nativeWindow("old-tail", "b", "Original tail")
	tail, err := service.RecordNative(ctx, "tree", tailInput)
	if err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var before []byte
	if err = database.QueryRow(`SELECT state FROM recordings WHERE session_id=?`, root).Scan(&before); err != nil {
		t.Fatal(err)
	}
	first.EventID = "metadata-root"
	first.Agent.Model = "configured-now"
	first.Messages[0].Model = "recorded-model"
	first.Messages[0].NativeID = "root-item"
	got, err := service.RecordNative(ctx, "tree", first)
	if err != nil || got != root {
		t.Fatal("parent replay created a replacement", got, err)
	}
	var after []byte
	var count int
	if err = database.QueryRow(`SELECT state FROM recordings WHERE session_id=?`, root).Scan(&after); err != nil || string(before) != string(after) {
		t.Fatal("pinned parent bytes changed on metadata-only upgrade", err)
	}
	if err = database.QueryRow(`SELECT count(*) FROM recordings`).Scan(&count); err != nil || count != 2 {
		t.Fatal("duplicate history introduced", count, err)
	}
	tailInput.EventID = "metadata-tail"
	tailInput.Messages[0].Model = "new-active-model"
	tailInput.Messages[0].NativeID = "tail-item"
	got, err = service.RecordNative(ctx, "tree", tailInput)
	if err != nil || got != tail {
		t.Fatal("active metadata did not enrich in place", got, err)
	}
	var raw []byte
	if err = database.QueryRow(`SELECT state FROM recordings WHERE session_id=?`, tail).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var state domain.State
	if err = json.Unmarshal(raw, &state); err != nil || state.Snapshot.Messages[0].Model != "new-active-model" {
		t.Fatal("active model missing", err)
	}
	var parent domain.State
	if err = json.Unmarshal(before, &parent); err != nil {
		t.Fatal(err)
	}
	canonical, err := capture.NewSession(parent.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if state.Snapshot.Continuation.PreviousRevision != parent.LastRevision || state.Snapshot.Continuation.PreviousDigest != canonical.Digest() {
		t.Fatal("continuation no longer points to the displayed parent")
	}
}
