package sqlite_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	capture "workgraph.local/platform/internal/capture/domain"
	"workgraph.local/platform/internal/collector/application"
	compat "workgraph.local/platform/internal/compatibility/application"
	format "workgraph.local/platform/internal/compatibility/domain"
)

func TestImportedPathsRemainHistoricalAndReplayKeepsOriginalTask(t *testing.T) {
	ctx := context.Background()
	store := open(t, filepath.Join(t.TempDir(), "state.sqlite"))
	if err := store.Configure(ctx, config); err != nil {
		t.Fatal(err)
	}
	scope := format.Scope{ObjectID: strings.Repeat("b", 40), RefObject: strings.Repeat("c", 40), TargetCommit: strings.Repeat("a", 40), Ref: "refs/notes/ai", Time: time.Now().UTC()}
	raw := []byte("historical.go\n  0123456789abcdef 1-2\n---\n" + `{"schema_version":"authorship/3.0.0","base_commit_sha":"ffffffffffffffffffffffffffffffffffffffff","prompts":{"0123456789abcdef":{"agent_id":{"tool":"local","id":"native","model":"local"}}}}`)
	projection, err := format.ParseGitAI(scope, raw)
	if err != nil {
		t.Fatal(err)
	}
	windows, err := compat.Prepare(projection)
	if err != nil {
		t.Fatal(err)
	}
	service := application.NewService(store, nil, time.Now)
	task := &capture.TaskRef{CountryID: config.RepositoryID, TaskID: "22222222-2222-4222-8222-222222222222"}
	if err := bindLegacyFixture(ctx, store, "tree", task); err != nil {
		t.Fatal(err)
	}
	id, err := service.RecordNative(ctx, "tree", windows[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Commit(ctx, "tree", strings.Repeat("d", 40), []string{"historical.go"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := service.Checkpoint(ctx, "tree", true); err != nil {
		t.Fatal(err)
	}
	pending, err := store.Pending(ctx, time.Now(), 64)
	if err != nil || len(pending) != 1 {
		t.Fatal(err)
	}
	if got := pending[0].Session.Snapshot().Commits; len(got) != 1 || got[0] != scope.TargetCommit {
		t.Fatal("historical path attributed to later human work", got)
	}
	if err := bindLegacyFixture(ctx, store, "tree", nil); err != nil {
		t.Fatal(err)
	}
	if pending[0].Session.Snapshot().Task != nil {
		t.Fatal("retired binding applied to a new import")
	}
	if replay, err := service.RecordNative(ctx, "tree", windows[0]); err != nil || replay != id {
		t.Fatal("import duplicated on replay", err)
	}
}

func TestImportedWindowsKeepPrivateConsentAndExactSourceParentOnPush(t *testing.T) {
	ctx := context.Background()
	store := open(t, filepath.Join(t.TempDir(), "state.sqlite"))
	if err := store.Configure(ctx, config); err != nil {
		t.Fatal(err)
	}
	scope := format.Scope{ObjectID: strings.Repeat("b", 40), RefObject: strings.Repeat("c", 40), TargetCommit: strings.Repeat("a", 40), Ref: "refs/entire/checkpoints/ab/abcdef1234ab"}
	metadata := []byte(`{"checkpoint_id":"abcdef1234ab","session_id":"native","created_at":"2026-10-30T10:00:00Z","agent":"local","model":"local","files_touched":[]}`)
	raw, _ := json.Marshal(map[string]any{"v": 1, "type": "user", "content": strings.Repeat("VerifiedImportWindow ", 40000)})
	projection, err := format.ParseEntire(scope, "abcdef1234ab", metadata, raw)
	if err != nil {
		t.Fatal(err)
	}
	windows, err := compat.Prepare(projection)
	if err != nil || len(windows) < 2 {
		t.Fatal("no windowed import", err)
	}
	service := application.NewService(store, nil, time.Now)
	for _, window := range windows {
		if _, err := service.RecordNative(ctx, "tree", window); err != nil {
			t.Fatal(err)
		}
	}
	if pending, err := store.Pending(ctx, time.Now(), 64); err != nil || len(pending) != 0 {
		t.Fatal("import granted publication without consent", err)
	}
	if err := service.PreparePush(ctx, []string{scope.TargetCommit}); err != nil {
		t.Fatal(err)
	}
	pending, err := store.Pending(ctx, time.Now(), 64)
	if err != nil || len(pending) != len(windows) {
		t.Fatal("selected push lost source windows", err)
	}
	byID := map[string]capture.Session{}
	for _, entry := range pending {
		byID[entry.Session.Snapshot().SessionID] = entry.Session
	}
	for _, entry := range pending {
		snap := entry.Session.Snapshot()
		if snap.Origin == nil || snap.Origin.TargetCommit != scope.TargetCommit {
			t.Fatal("source origin lost")
		}
		if snap.Continuation != nil {
			parent, found := byID[snap.Continuation.PreviousSessionID]
			if !found || !entry.Session.MatchesParent(parent) {
				t.Fatal("import parent origin/reference did not verify")
			}
		}
	}
}
