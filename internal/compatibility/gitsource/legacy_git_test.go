package gitsource

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"workgraph.local/platform/internal/compatibility/domain"
)

func legacyGit(t *testing.T) {
	t.Helper()
	actual, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	script := "#!/bin/sh\nfor arg do if [ \"$arg\" = --no-lazy-fetch ]; then exit 129; fi; done\nunset GIT_NO_LAZY_FETCH\nexec '" + strings.ReplaceAll(actual, "'", "'\"'\"'") + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(directory, "git"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func TestExistingLocalObjectsWorkWithoutTheNewNoLazyFetchFlag(t *testing.T) {
	root, target := repo(t, "sha1")
	legacyGit(t)
	r := reader{root: root, remaining: domain.MaxSourceBytes}
	if raw, err := r.object(context.Background(), target, "commit"); err != nil || len(raw) == 0 {
		t.Fatal("ordinary local source requires newer Git", err)
	}
}

func TestLegacyGitCannotFetchMissingPromisorObjectsOrExecuteRemoteHelpers(t *testing.T) {
	root, target := repo(t, "sha1")
	object := git(t, root, "", "rev-parse", "HEAD:code.go")
	marker := filepath.Join(t.TempDir(), "transport-executed")
	spy := filepath.Join(t.TempDir(), "transport-spy")
	if err := os.WriteFile(spy, []byte("#!/bin/sh\n: > '"+strings.ReplaceAll(marker, "'", "'\"'\"'")+"'\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	git(t, root, "", "config", "core.repositoryformatversion", "1")
	git(t, root, "", "config", "extensions.partialClone", "origin")
	git(t, root, "", "config", "remote.origin.promisor", "true")
	git(t, root, "", "config", "remote.origin.url", "ext::"+spy)
	git(t, root, "", "config", "protocol.ext.allow", "always")
	if err := os.Remove(filepath.Join(root, ".git", "objects", object[:2], object[2:])); err != nil {
		t.Fatal(err)
	}
	// Establish that this fixture really attempts a transport without the acquisition policy.
	environment := []string{}
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "GIT_") {
			environment = append(environment, value)
		}
	}
	environment = append(environment, "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	control := exec.Command("git", "-C", root, "cat-file", "-s", object)
	control.Env = environment
	control.Stdout = io.Discard
	control.Stderr = io.Discard
	_ = control.Run()
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("fixture never attempted lazy transport", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	legacyGit(t)
	t.Setenv("GIT_ALLOW_PROTOCOL", "ext:https") // Caller/local config cannot weaken the adapter's denial.
	r := reader{root: root, remaining: domain.MaxSourceBytes}
	if _, err := r.object(context.Background(), object, "blob"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal("missing source accepted", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("source acquisition executed a transport helper")
	}
	if raw, err := r.object(context.Background(), target, "commit"); err != nil || len(raw) == 0 {
		t.Fatal("guard blocked existing local objects", err)
	}
}
