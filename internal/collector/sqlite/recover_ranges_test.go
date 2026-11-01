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
)

func TestRecoveryDoesNotBridgeUnknownCommitAndReplayDoesNotQueueChanges(t *testing.T) {
	ctx := context.Background()
	store := open(t, filepath.Join(t.TempDir(), "state.sqlite"))
	if err := store.Configure(ctx, config); err != nil {
		t.Fatal(err)
	}
	service := application.NewService(store, nil, time.Now)
	o := nativeWindow("all", "a", "first request")
	o.Messages = nil
	o.History.MessageIDs = nil
	a, b, c := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	for i, sha := range []string{a, b, c} {
		raw, _ := json.Marshal(map[string]any{"command": "git commit -m fixture", "output": "[main " + sha[:7] + "] fixture\n", "exit_code": 0})
		o.Messages = append(o.Messages, capture.Message{Role: "user", Text: "work for " + sha}, capture.Message{Role: "tool", ToolName: "shell", Text: string(raw)})
		o.History.MessageIDs = append(o.History.MessageIDs, strings.Repeat(string(rune('a'+i*2)), 64), strings.Repeat(string(rune('b'+i*2)), 64))
	}
	id, err := service.RecordNative(ctx, "tree", o)
	if err != nil {
		t.Fatal(err)
	}
	prefixes, err := service.RecoveryPrefixes(ctx, "tree", id)
	if err != nil || len(prefixes) != 3 {
		t.Fatal(prefixes, err)
	}
	report, err := service.RecoverCommitRanges(ctx, "tree", id, []string{a, c})
	if err != nil || report.Ranges != 1 || report.UpdatedWindows != 1 {
		t.Fatal(report, err)
	}
	if err := store.Within(ctx, func(tx application.Transaction) error {
		e, found, err := tx.FindSession(id)
		if err != nil {
			return err
		}
		if !found {
			t.Fatal("source lost")
		}
		ranges := e.Recording.State().Snapshot.CommitRanges
		if len(ranges) != 1 || ranges[0].CommitSHA != c || ranges[0].Start != 4 || ranges[0].End != 6 {
			t.Fatal("unresolved commit leaked into later scope", ranges)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	report, err = service.RecoverCommitRanges(ctx, "tree", id, []string{a, c})
	if err != nil || report.Ranges != 0 || report.UpdatedWindows != 0 {
		t.Fatal("replay claimed new work", report, err)
	}
	stats, err := store.Stats(ctx)
	if err != nil || stats.Eligible != 0 {
		t.Fatal("recovery granted publication", stats, err)
	}
}
