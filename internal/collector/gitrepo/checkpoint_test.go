package gitrepo_test

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"workgraph.local/platform/internal/collector/gitrepo"
)

func TestCheckpointExcludesExistingBranchButRetainsNewWorkAfterCheckout(t *testing.T) {
	root := fixture(t)
	base := commit(t, root, "base.txt", "base")
	git(t, root, "checkout", "-b", "side")
	oldSide := commit(t, root, "side.txt", "old side")
	git(t, root, "checkout", "main")
	before := commit(t, root, "main.txt", "main work")
	repo, err := gitrepo.DiscoverLocal(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	heads, err := repo.ReferenceHeads(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	git(t, root, "checkout", "side")
	selected, err := repo.CheckpointWindow(context.Background(), before, oldSide, heads)
	if err != nil || len(selected) != 0 {
		t.Fatal("checkout adopted pre-existing work", selected, err)
	}
	next := commit(t, root, "new.txt", "new side work")
	selected, err = repo.CheckpointWindow(context.Background(), before, next, heads)
	if err != nil || len(selected) != 1 || selected[0] != next {
		t.Fatal("new work after checkout was lost or broadened", selected, err)
	}
	fresh, err := repo.ReferenceHeads(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	git(t, root, "commit", "--amend", "-m", "rewritten new work")
	amended := git(t, root, "rev-parse", "HEAD")
	selected, err = repo.CheckpointWindow(context.Background(), next, amended, fresh)
	if err != nil || len(selected) != 1 || selected[0] != amended {
		t.Fatal("amend context lost", selected, err)
	}
	if len(heads) == 0 || base == "" {
		t.Fatal("fixture lacks branches")
	}
}

func TestRecoveryObjectVerificationDoesNotWalkEntireRepository(t *testing.T) {
	root := fixture(t)
	var stream strings.Builder
	for i := 0; i < 2105; i++ {
		message := fmt.Sprintf("Synthetic %d", i)
		fmt.Fprintf(&stream, "commit refs/heads/main\ncommitter Test <test@example.invalid> %d +0000\ndata %d\n%s\n", i+1, len(message), message)
	}
	command := exec.Command("git", "-C", root, "fast-import", "--quiet")
	command.Stdin = strings.NewReader(stream.String())
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	first, last := git(t, root, "rev-parse", "HEAD~2104"), git(t, root, "rev-parse", "HEAD")
	repo, err := gitrepo.DiscoverLocal(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	known, err := repo.ResolveCommitPrefixes(context.Background(), []string{first[:12], last[:12], "ffffffffffffffff"})
	if err != nil || len(known) != 2 || !contains(known, first) || !contains(known, last) {
		t.Fatal("large history made recorded objects unavailable", known, err)
	}
}
