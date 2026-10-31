package sqlite_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	capture "workgraph.local/platform/internal/capture/domain"
	"workgraph.local/platform/internal/collector/application"
	"workgraph.local/platform/internal/collector/domain"
)

func TestDisabledCollectionSurvivesRestartAndStopsRecordAndDelivery(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.sqlite")
	store := open(t, path)
	if e := store.Configure(ctx, config); e != nil {
		t.Fatal(e)
	}
	svc := application.NewService(store, nil, time.Now)
	if _, e := svc.Record(ctx, "tree", observation()); e != nil {
		t.Fatal(e)
	}
	if e := svc.Checkpoint(ctx, "tree", true); e != nil {
		t.Fatal(e)
	}
	if e := store.SetCollection(ctx, false, ""); e != nil {
		t.Fatal(e)
	}
	store.Close()
	store = open(t, path)
	svc = application.NewService(store, nil, time.Now)
	if _, e := svc.Record(ctx, "tree", observation()); !errors.Is(e, domain.ErrDisabled) {
		t.Fatal("recording continued", e)
	}
	calls := 0
	svc = application.NewService(store, failingUploader{onSend: func(capture.Session) { calls++ }}, time.Now)
	if _, e := svc.Sync(ctx); !errors.Is(e, domain.ErrDisabled) || calls != 0 {
		t.Fatal("disabled delivery continued", e, calls)
	}
	if stats, e := store.Stats(ctx); e != nil || stats.Pending != 1 {
		t.Fatal("disable discarded queue", stats, e)
	}
	if e := store.SetCollection(ctx, true, ""); e != nil {
		t.Fatal(e)
	}
	if result, e := svc.Sync(ctx); e != nil || result.Sent != 1 {
		t.Fatal("explicit re-enable did not resume", result, e)
	}
}
func TestDisabledAgentDoesNotStarveAnotherAgentsQueue(t *testing.T) {
	ctx := context.Background()
	store := open(t, filepath.Join(t.TempDir(), "state.sqlite"))
	if e := store.Configure(ctx, config); e != nil {
		t.Fatal(e)
	}
	svc := application.NewService(store, nil, time.Now)
	for i := 0; i < 20; i++ {
		o := observation()
		o.SourceSessionID = fmt.Sprintf("disabled-%d", i)
		o.Agent.Name = "codex"
		if _, e := svc.Record(ctx, "tree", o); e != nil {
			t.Fatal(e)
		}
	}
	other := observation()
	other.Agent.Name = "opencode"
	if _, e := svc.Record(ctx, "tree", other); e != nil {
		t.Fatal(e)
	}
	if e := svc.Checkpoint(ctx, "tree", true); e != nil {
		t.Fatal(e)
	}
	if e := store.SetCollection(ctx, false, "codex"); e != nil {
		t.Fatal(e)
	}
	o := observation()
	o.Agent.Name = "codex"
	if _, e := svc.Record(ctx, "tree", o); !errors.Is(e, domain.ErrDisabled) {
		t.Fatal("disabled agent recorded", e)
	}
	calls := 0
	svc = application.NewService(store, failingUploader{onSend: func(s capture.Session) {
		calls++
		if s.Snapshot().Agent.Name != "opencode" {
			t.Error("disabled agent uploaded")
		}
	}}, time.Now)
	result, e := svc.Sync(ctx)
	if e != nil || result.Sent != 1 || calls != 1 {
		t.Fatal("disabled entries occupied first queue page", result, e)
	}
	if stats, e := store.Stats(ctx); e != nil || stats.Pending != 20 {
		t.Fatal("disabled agent queue lost", stats, e)
	}
}
