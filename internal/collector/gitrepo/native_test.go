package gitrepo_test

import (
	"context"
	"testing"
	"workgraph.local/platform/internal/collector/gitrepo"
)

func TestNativeHeadWindowIncludesIntermediateCommitsAndHandlesAmend(t *testing.T) {
	root := fixture(t)
	repository, err := gitrepo.Discover(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if head, err := repository.HeadID(context.Background()); err != nil || head != "" {
		t.Fatal("unborn worktree was not represented explicitly")
	}
	base := commit(t, root, "base.txt", "base")
	first := commit(t, root, "a.go", "one")
	second := commit(t, root, "b.go", "two")
	commits, err := repository.HeadWindow(context.Background(), base, second)
	if err != nil || len(commits) != 2 || !contains(commits, first) || !contains(commits, second) {
		t.Fatal("native tool head window missed commits")
	}
	git(t, root, "commit", "--amend", "-m", "amended")
	amended := git(t, root, "rev-parse", "HEAD")
	commits, err = repository.HeadWindow(context.Background(), second, amended)
	if err != nil || len(commits) != 1 || commits[0] != amended {
		t.Fatal("rewritten head window was not bounded to its observed new tip")
	}
}
