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

func TestHookInstallationPreservesOriginalFilesAndRestoresScopedSetting(t *testing.T) {
	root := fixture(t)
	commit(t, root, "a.go", "one")
	originalDir := filepath.Join(root, "custom hooks")
	if err := os.MkdirAll(originalDir, 0700); err != nil {
		t.Fatal(err)
	}
	original := []byte("#!/bin/sh\nexit 17\n")
	if err := os.WriteFile(filepath.Join(originalDir, "README"), []byte("User hook documentation"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(originalDir, "pre-push"), original, 0700); err != nil {
		t.Fatal(err)
	}
	git(t, root, "config", "--local", "core.hooksPath", "custom hooks")
	executable := filepath.Join(t.TempDir(), "collector space's")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	repository, err := gitrepo.Discover(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	installation, err := repository.InstallHooks(context.Background(), executable)
	if err != nil {
		t.Fatal(err)
	}
	if got := git(t, root, "config", "--get", "core.hooksPath"); got != installation.ManagedDir {
		t.Fatal("managed path not selected")
	}
	if _, err := gitrepo.ReadInstallation(installation.ManifestPath); err != nil {
		t.Fatal("unrelated documentation broke dispatch metadata", err)
	}
	lateHook := []byte("#!/bin/sh\nprintf '%s' \"$1\" > forwarded-marker\n")
	if err := os.WriteFile(filepath.Join(originalDir, "post-merge"), lateHook, 0700); err != nil {
		t.Fatal(err)
	}
	forward := exec.Command(filepath.Join(installation.ManagedDir, "post-merge"), "1")
	forward.Dir = root
	if err := forward.Run(); err != nil {
		t.Fatal("hook added after installation was not forwarded", err)
	}
	if marker, err := os.ReadFile(filepath.Join(root, "forwarded-marker")); err != nil || string(marker) != "1" {
		t.Fatal("late hook arguments changed")
	}
	if bytes, err := os.ReadFile(filepath.Join(originalDir, "pre-push")); err != nil || string(bytes) != string(original) {
		t.Fatal("user hook changed")
	}
	if _, err := repository.InstallHooks(context.Background(), executable); err != nil {
		t.Fatal("identical install is not idempotent")
	}
	if err := repository.UninstallHooks(context.Background()); err != nil {
		t.Fatal(err)
	}
	if git(t, root, "config", "--get", "core.hooksPath") != "custom hooks" {
		t.Fatal("original local hook setting not restored")
	}
}

func TestWorktreeSpecificHookConfigurationUsesGitBooleanSemantics(t *testing.T) {
	root := fixture(t)
	commit(t, root, "a.go", "one")
	linked := filepath.Join(t.TempDir(), "linked")
	git(t, root, "worktree", "add", "-b", "feature", linked)
	git(t, root, "config", "extensions.worktreeConfig", "1")
	git(t, root, "config", "--worktree", "core.hooksPath", "main-hooks")
	git(t, linked, "config", "--worktree", "core.hooksPath", "linked-hooks")
	executable := filepath.Join(t.TempDir(), "collector")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	repository, err := gitrepo.Discover(context.Background(), linked)
	if err != nil {
		t.Fatal(err)
	}
	installation, err := repository.InstallHooks(context.Background(), executable)
	if err != nil {
		t.Fatal(err)
	}
	if installation.Scope != "--worktree" || git(t, linked, "config", "--get", "core.hooksPath") != installation.ManagedDir {
		t.Fatal("worktree override prevented installation")
	}
	if git(t, root, "config", "--get", "core.hooksPath") != "main-hooks" {
		t.Fatal("another worktree's hooks changed")
	}
	if err := repository.UninstallHooks(context.Background()); err != nil {
		t.Fatal(err)
	}
	if git(t, linked, "config", "--get", "core.hooksPath") != "linked-hooks" {
		t.Fatal("worktree hook setting was not restored")
	}
}

func TestUninstallRefusesToOverwriteChangedHookOrConfiguration(t *testing.T) {
	for _, change := range []string{"wrapper", "setting"} {
		t.Run(change, func(t *testing.T) {
			root := fixture(t)
			repository, err := gitrepo.Discover(context.Background(), root)
			if err != nil {
				t.Fatal(err)
			}
			executable := filepath.Join(t.TempDir(), "collector")
			if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
				t.Fatal(err)
			}
			installation, err := repository.InstallHooks(context.Background(), executable)
			if err != nil {
				t.Fatal(err)
			}
			if change == "wrapper" {
				if err := os.WriteFile(filepath.Join(installation.ManagedDir, "pre-push"), []byte("user customization"), 0700); err != nil {
					t.Fatal(err)
				}
			} else {
				git(t, root, "config", "--local", "core.hooksPath", "new-user-hook-dir")
			}
			if err := repository.UninstallHooks(context.Background()); err == nil {
				t.Fatal("user change was silently overwritten")
			}
			if change == "setting" && !strings.Contains(git(t, root, "config", "--get", "core.hooksPath"), "new-user-hook-dir") {
				t.Fatal("user setting destroyed")
			}
		})
	}
}
