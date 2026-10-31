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

func TestLocalDiscoveryWorksWithoutTheNewNoLazyFetchFlag(t *testing.T) {
	root := fixture(t)
	actual, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
	script := "#!/bin/sh\nfor arg do if [ \"$arg\" = --no-lazy-fetch ]; then exit 129; fi; done\nunset GIT_NO_LAZY_FETCH\nexec " + quote(actual) + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(directory, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	repository, err := gitrepo.DiscoverLocal(context.Background(), root)
	physical, _ := filepath.EvalSymlinks(root)
	if err != nil || repository.Root != physical {
		t.Fatal("metadata discovery requires a newer Git flag", err)
	}
}

func TestLocalRepositoryKeepsHeadQueriesInItsSelectedWorktreeDespiteInheritedOverrides(t *testing.T) {
	root, other := fixture(t), fixture(t)
	want := commit(t, root, "selected.go", "selected")
	_ = commit(t, other, "other.go", "other")
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_WORK_TREE", other)
	repository, err := gitrepo.DiscoverLocal(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if actual, err := repository.HeadID(context.Background()); err != nil || actual != want {
		t.Fatal("source metadata drifted into another repository", err)
	}
	window, err := repository.HeadWindow(context.Background(), "", want)
	if err != nil || len(window) != 1 || window[0] != want {
		t.Fatal("source association used inherited Git routing", err)
	}
}
