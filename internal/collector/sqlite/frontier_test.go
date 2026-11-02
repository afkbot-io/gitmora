package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

func TestCheckpointRecoveryRejectsOutOfOrderApplication(t *testing.T) {
	ctx := context.Background()
	store := open(t, filepath.Join(t.TempDir(), "state.sqlite"))
	if err := store.Configure(ctx, config); err != nil {
		t.Fatal(err)
	}
	service := application.NewService(store, nil, time.Now)
	source := domain.NativeSource{Worktree: "tree", Agent: "local-agent", SessionID: "native"}
	a, b, c := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	first, err := service.BeginFrontierWithHeads(ctx, source, "first", a, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.EndFrontier(ctx, source, "first", b); err != nil {
		t.Fatal(err)
	}
	second, err := service.BeginFrontierWithHeads(ctx, source, "second", b, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.EndFrontier(ctx, source, "second", c); err != nil {
		t.Fatal(err)
	}
	o := nativeWindow("both", "a", "first task")
	o.Messages = append(o.Messages, capture.Message{Role: "assistant", Text: "first result"}, capture.Message{Role: "user", Text: "second task"}, capture.Message{Role: "assistant", Text: "second result"})
	o.History.MessageIDs = []string{strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64), strings.Repeat("d", 64)}
	if _, err = service.RecordNativeCheckpoints(ctx, "tree", o, []domain.CheckpointBinding{{ID: second.ID, MessageEnd: 4}}); err != nil {
		t.Fatal(err)
	}
	partial, err := service.Frontier(ctx, "tree", second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.ApplyFrontier(ctx, "tree", partial, []string{c}); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("checkpoint consumed context across an unresolved tool", err)
	}
	if _, err = service.RecordNativeCheckpoints(ctx, "tree", o, []domain.CheckpointBinding{{ID: first.ID, MessageEnd: 2}, {ID: second.ID, MessageEnd: 4}}); err != nil {
		t.Fatal(err)
	}
	second, err = service.Frontier(ctx, "tree", second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.ApplyFrontier(ctx, "tree", second, []string{c}); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("later checkpoint consumed earlier context", err)
	}
	if err = store.SetCollection(ctx, false, source.Agent); err != nil {
		t.Fatal(err)
	}
	blocked, err := service.Frontier(ctx, "tree", first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.ApplyFrontier(ctx, "tree", blocked, []string{b}); !errors.Is(err, domain.ErrDisabled) {
		t.Fatal("disabled agent recovered checkpoint", err)
	}
	if err = store.SetCollection(ctx, true, source.Agent); err != nil {
		t.Fatal(err)
	}
	first, err = service.Frontier(ctx, "tree", first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.ApplyFrontier(ctx, "tree", first, []string{b}); err != nil {
		t.Fatal(err)
	}
	if err = service.ApplyFrontier(ctx, "tree", second, []string{c}); err != nil {
		t.Fatal(err)
	}
	if err = store.Within(ctx, func(tx application.Transaction) error {
		e, found, err := tx.FindSession(second.TargetSessionID)
		if err != nil {
			return err
		}
		if !found {
			t.Fatal("missing checkpoint target")
		}
		ranges := e.Recording.State().Snapshot.CommitRanges
		if len(ranges) != 2 || ranges[0].Start != 0 || ranges[0].End != 2 || ranges[1].Start != 2 || ranges[1].End != 4 {
			t.Fatal("checkpoint intervals overlap", ranges)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

}

func TestFrontierRestartPinsTaskEndAndExactReplayTarget(t *testing.T) {
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
	source := domain.NativeSource{Worktree: "tree", Agent: "local-agent", SessionID: "native"}
	a, b, c := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	begin, err := service.BeginFrontierWithHeads(ctx, source, "call", a, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := bindLegacyFixture(ctx, store, "tree", nil); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store = open(t, path)
	service = application.NewService(store, nil, time.Now)
	again, err := service.BeginFrontierWithHeads(ctx, source, "call", c, nil)
	if err != nil || again.Before != a || again.ID != begin.ID {
		t.Fatal("begin replay changed", err)
	}
	ended, err := service.EndFrontier(ctx, source, "call", b)
	if err != nil {
		t.Fatal(err)
	}
	ended, err = service.EndFrontier(ctx, source, "call", c)
	if err != nil || ended.After == nil || *ended.After != b {
		t.Fatal("end replay changed", err)
	}
	o := nativeWindow("root", "a", "tool result")
	id, err := service.RecordNative(ctx, "tree", o)
	if err != nil {
		t.Fatal(err)
	}
	// An unchanged source observation must still atomically bind an unacknowledged frontier.
	replay, err := service.RecordNativeFrontiers(ctx, "tree", o, []string{ended.ID})
	if err != nil || replay != id {
		t.Fatal("target replay", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store = open(t, path)
	service = application.NewService(store, nil, time.Now)
	f, err := service.Frontier(ctx, "tree", ended.ID)
	if err != nil || f.TargetSessionID != id {
		t.Fatal("target lost on restart", err)
	}
	if err := service.ApplyFrontier(ctx, "tree", f, []string{b}); err != nil {
		t.Fatal(err)
	}
	if err := service.ApplyFrontier(ctx, "tree", f, []string{b}); err != nil {
		t.Fatal("ACK replay", err)
	}
	if queue, err := store.Pending(ctx, time.Now(), 64); err != nil || len(queue) != 0 {
		t.Fatal("association granted consent", err)
	}
	if err := service.PreparePush(ctx, []string{b}); err != nil {
		t.Fatal(err)
	}
	queue, err := store.Pending(ctx, time.Now(), 64)
	if err != nil || len(queue) != 1 {
		t.Fatal("authorized snapshot missing", err)
	}
	snapshot := queue[0].Session.Snapshot()
	if snapshot.Task != nil || len(snapshot.Commits) != 1 || snapshot.Commits[0] != b {
		t.Fatalf("wrong original scope/boundary: %+v", snapshot)
	}
}

func TestFrontierScopeFailuresRollbackCaptureAndUnknownEndStaysOpen(t *testing.T) {
	ctx := context.Background()
	store := open(t, filepath.Join(t.TempDir(), "state.sqlite"))
	if err := store.Configure(ctx, config); err != nil {
		t.Fatal(err)
	}
	service := application.NewService(store, nil, time.Now)
	source := domain.NativeSource{Worktree: "tree", Agent: "local-agent", SessionID: "native"}
	f, err := service.BeginFrontierWithHeads(ctx, source, "call", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	o := nativeWindow("root", "a", "private")
	if _, err := service.RecordNativeFrontiers(ctx, "tree", o, []string{f.ID}); !errors.Is(err, domain.ErrConflict) {
		t.Fatal(err)
	}
	stats, err := store.Stats(ctx)
	if err != nil || stats.Recordings != 0 {
		t.Fatal("failed binding partially recorded", err)
	}
	if _, err := service.Frontier(ctx, "other-tree", f.ID); !errors.Is(err, domain.ErrConflict) {
		t.Fatal(err)
	}
	list, err := service.PendingFrontiers(ctx, "tree")
	if err != nil || len(list) != 1 || list[0].Status() != "started" {
		t.Fatal("unknown end inferred", err)
	}
	f, err = service.ResolveFrontier(ctx, "tree", f.ID, strings.Repeat("b", 40), "verified actual Git object")
	if err != nil {
		t.Fatal(err)
	}
	foreign := o
	foreign.SourceSessionID = "foreign"
	if _, err := service.RecordNativeFrontiers(ctx, "tree", foreign, []string{f.ID}); !errors.Is(err, domain.ErrConflict) {
		t.Fatal(err)
	}
	if _, err := service.RecordNativeFrontiers(ctx, "tree", o, []string{f.ID}); err != nil {
		t.Fatal(err)
	}
	stale := f
	f, err = service.Frontier(ctx, "tree", f.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ApplyFrontier(ctx, "tree", stale, []string{strings.Repeat("b", 40)}); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("stale target accepted", err)
	}
	if err := service.AbandonFrontier(ctx, "tree", f.ID, "operator skipped association"); err != nil {
		t.Fatal(err)
	}
	if err := service.ApplyFrontier(ctx, "tree", f, []string{strings.Repeat("b", 40)}); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("abandoned write accepted", err)
	}
}

func TestFrontierConcurrentReplayCapacityAndRelease(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.sqlite")
	store := open(t, path)
	if err := store.Configure(ctx, config); err != nil {
		t.Fatal(err)
	}
	source := domain.NativeSource{Worktree: "tree", Agent: "local-agent", SessionID: "native"}
	var group sync.WaitGroup
	failures := make(chan error, 12)
	for i := 0; i < 12; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			other, err := sqlite.Open(ctx, path)
			if err != nil {
				failures <- err
				return
			}
			defer other.Close()
			service := application.NewService(other, nil, time.Now)
			if _, err := service.BeginFrontierWithHeads(ctx, source, "same-call", strings.Repeat("a", 40), nil); err != nil {
				failures <- err
			}
		}()
	}
	group.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	service := application.NewService(store, nil, time.Now)
	list, err := service.PendingFrontiers(ctx, "tree")
	if err != nil || len(list) != 1 {
		t.Fatal("duplicate admission", err)
	}
	for i := 1; i < domain.MaxPendingFrontiers; i++ {
		if _, err := service.BeginFrontierWithHeads(ctx, source, fmt.Sprintf("call-%d", i), "", nil); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.BeginFrontierWithHeads(ctx, source, "overflow", "", nil); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("unbounded admission", err)
	}
	if err := service.AbandonFrontier(ctx, "tree", list[0].ID, "explicitly closed"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.BeginFrontierWithHeads(ctx, source, "overflow", "", nil); err != nil {
		t.Fatal(err)
	}
	if replay, err := service.BeginFrontierWithHeads(ctx, source, "same-call", "", nil); err != nil || !replay.Abandoned {
		t.Fatal("terminal identity reused", err)
	}
}

func TestFrontierVersionThreeMigrationAndFutureRefusal(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.sqlite")
	store := open(t, path)
	if err := store.Configure(ctx, config); err != nil {
		t.Fatal(err)
	}
	service := application.NewService(store, nil, time.Now)
	o := nativeWindow("old", "a", "preserved native capture")
	id, err := service.RecordNative(ctx, "tree", o)
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
	if _, err := old.Exec(`DROP TABLE IF EXISTS git_frontier_scopes; DROP TABLE IF EXISTS git_head_scopes; DROP TABLE native_frontiers;DROP TABLE native_frontier_sources;PRAGMA user_version=3;`); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	store = open(t, path)
	service = application.NewService(store, nil, time.Now)
	if replay, err := service.RecordNative(ctx, "tree", o); err != nil || replay != id {
		t.Fatal("migration lost native event", err)
	}
	if pending, err := store.Pending(ctx, time.Now(), 64); err != nil || len(pending) != 1 {
		t.Fatal("migration lost consent", err)
	}
	if _, err := service.BeginFrontierWithHeads(ctx, domain.NativeSource{Worktree: "tree", Agent: "local-agent", SessionID: "native"}, "new", "", nil); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	future, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := future.Exec(`PRAGMA user_version=7`); err != nil {
		t.Fatal(err)
	}
	if err := future.Close(); err != nil {
		t.Fatal(err)
	}
	if unexpected, err := sqlite.Open(ctx, path); err == nil {
		unexpected.Close()
		t.Fatal("future schema accepted")
	}
}
