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

func TestPreToolHeadsAreFrozenOnReplayAndIdenticalScopesAreShared(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.sqlite")
	store := open(t, path)
	if err := store.Configure(ctx, config); err != nil {
		t.Fatal(err)
	}
	service := application.NewService(store, nil, time.Now)
	source := domain.NativeSource{Worktree: "tree", Agent: "local-agent", SessionID: "native"}
	a, b := strings.Repeat("a", 40), strings.Repeat("b", 40)
	f, err := service.BeginFrontierWithHeads(ctx, source, "one", a, []string{b, a, a})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.BeginFrontierWithHeads(ctx, source, "one", b, []string{b}); err != nil {
		t.Fatal(err)
	}
	heads, found, err := service.FrontierHeads(ctx, "tree", f.ID)
	if err != nil || !found || len(heads) != 2 || heads[0] != a || heads[1] != b {
		t.Fatal("retry changed the pre-tool scope", heads, err)
	}
	if _, err = service.BeginFrontierWithHeads(ctx, source, "two", a, []string{a, b}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = store.Within(ctx, func(tx application.Transaction) error { // Check through the production read port as well.
		known, ok, err := tx.HeadScope(f.ID)
		if err == nil && ok {
			count = len(known)
		}
		return err
	}); err != nil || count != 2 {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var scopes, links int
	if err = db.QueryRowContext(ctx, "SELECT (SELECT count(*) FROM git_head_scopes),(SELECT count(*) FROM git_frontier_scopes)").Scan(&scopes, &links); err != nil || scopes != 1 || links != 2 {
		t.Fatal("identical pre-tool tips were not deduplicated", scopes, links, err)
	}
}
