package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"workgraph.local/platform/internal/collector/application"
	"workgraph.local/platform/internal/collector/domain"
)

func TestVersionFourOriginCapabilityUpgradePreservesNativeStateAndFrontiers(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.sqlite")
	store := open(t, path)
	if err := store.Configure(ctx, config); err != nil {
		t.Fatal(err)
	}
	service := application.NewService(store, nil, time.Now)
	id, err := service.RecordNative(ctx, "tree", nativeWindow("old", "a", "old native source"))
	if err != nil {
		t.Fatal(err)
	}
	source := domain.NativeSource{Worktree: "tree", Agent: "local-agent", SessionID: "native"}
	before, err := service.BeginFrontierWithHeads(ctx, source, "durable-call", strings.Repeat("a", 40), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	var state []byte
	if err := legacy.QueryRow("SELECT state FROM recordings WHERE session_id=?", id).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec("DROP TABLE git_frontier_scopes; DROP TABLE git_head_scopes; PRAGMA user_version=4"); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	store = open(t, path)
	service = application.NewService(store, nil, time.Now)
	if replay, err := service.RecordNative(ctx, "tree", nativeWindow("old", "a", "old native source")); err != nil || replay != id {
		t.Fatal("native identity changed", err)
	}
	found, err := service.Frontier(ctx, "tree", before.ID)
	if err != nil || found.Before != before.Before || found.After != nil {
		t.Fatal("frontier changed", err)
	}
	check, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer check.Close()
	var version int
	var unchanged []byte
	if err := check.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 6 {
		t.Fatal("older clients can silently drop origin fields", version, err)
	}
	if err := check.QueryRow("SELECT state FROM recordings WHERE session_id=?", id).Scan(&unchanged); err != nil || string(unchanged) != string(state) {
		t.Fatal("migration rewrote native bytes", err)
	}
}
