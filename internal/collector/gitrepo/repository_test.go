package gitrepo_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"workgraph.local/platform/internal/collector/gitrepo"
)

func git(t *testing.T, path string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", path}, args...)...)
	command.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.invalid")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}
func fixture(t *testing.T) string {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	root := filepath.Join(t.TempDir(), "repo space's")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	git(t, root, "init", "-b", "main")
	return root
}

func TestDiscoverLocalIgnoresInheritedGitDirectories(t *testing.T) {
	root := fixture(t)
	expected, err := gitrepo.DiscoverLocal(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "missing"))
	t.Setenv("GIT_WORK_TREE", t.TempDir())
	actual, err := gitrepo.DiscoverLocal(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if *actual != *expected {
		t.Fatal("import ledger discovery followed inherited Git override")
	}
}
func commit(t *testing.T, root, name, text string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", "--", name)
	git(t, root, "commit", "-m", "Synthetic change")
	return git(t, root, "rev-parse", "HEAD")
}

func TestLinkedWorktreeSharesLedgerButHasIndependentIdentity(t *testing.T) {
	root := fixture(t)
	commit(t, root, "a.go", "first")
	linked := filepath.Join(t.TempDir(), "linked space's")
	git(t, root, "worktree", "add", "-b", "feature", linked)
	first, err := gitrepo.Discover(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := gitrepo.Discover(context.Background(), linked)
	if err != nil {
		t.Fatal(err)
	}
	if first.CommonDir != second.CommonDir || first.WorktreeKey() == second.WorktreeKey() || first.LedgerPath() != second.LedgerPath() {
		t.Fatal("Git common/worktree separation is incorrect")
	}
	if err := os.WriteFile(filepath.Join(linked, "a.go"), []byte("partially changed"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := second.Head(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Dirty) != 1 || info.Dirty[0] != "a.go" {
		t.Fatal("dirty paths omitted")
	}
}

func TestPrePushSelectionIncludesAllCommitsAndMissingRemoteFallback(t *testing.T) {
	root := fixture(t)
	first := commit(t, root, "a.go", "one")
	second := commit(t, root, "b.go", "two")
	third := commit(t, root, "c.go", "three")
	repository, err := gitrepo.Discover(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	input := "refs/heads/main " + third + " refs/heads/main " + first + "\n"
	selected, err := repository.Reachable(context.Background(), strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(selected) != 2 || !contains(selected, second) || !contains(selected, third) || contains(selected, first) {
		t.Fatal("push omitted intermediate commits or included excluded history")
	}
	input = "refs/heads/main " + third + " refs/heads/main " + strings.Repeat("f", 40) + "\n"
	selected, err = repository.Reachable(context.Background(), strings.NewReader(input))
	if err != nil || len(selected) != 3 {
		t.Fatal("missing remote object dropped reachable context")
	}
	if _, err := repository.Reachable(context.Background(), strings.NewReader("invalid push input\n")); err == nil {
		t.Fatal("malformed input accepted")
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
