package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitRejectsCredentialBearingRemoteBeforePersistingConfig(t *testing.T) {
	for _, remote := range []string{
		"ssh://git:fixture-private-password@gitlab.internal/team/app.git",
		"https://git:fixture-private-password@gitlab.internal/team/app%zz.git",
	} {
		root, _ := repository(t)
		git(t, root, "remote", "set-url", "origin", remote)
		output, err := cli(root, nil, "init", "--server", "http://127.0.0.1:1", "--repository-id", "11111111-1111-4111-8111-111111111111")
		if err == nil || strings.Contains(output, "fixture-private-password") {
			t.Fatal("unsafe Git remote was accepted or exposed in diagnostics")
		}
		raw, err := os.ReadFile(filepath.Join(root, ".git", "workgraph", "state.sqlite"))
		if err != nil || strings.Contains(string(raw), "fixture-private-password") {
			t.Fatal("collector persisted a remote credential", err)
		}
	}
}

func TestNativeCommandsKeepPhysicalWorktreeWhenParentProvidesOtherGitRouting(t *testing.T) {
	selected, _ := repository(t)
	other, _ := repository(t)
	write(t, selected, "selected-only.go", "selected")
	git(t, selected, "add", "selected-only.go")
	git(t, selected, "commit", "-m", "distinct physical head")
	initialize(t, selected, "http://127.0.0.1:1")
	initialize(t, other, "http://127.0.0.1:1")
	head := git(t, selected, "rev-parse", "HEAD")
	t.Setenv("GIT_DIR", filepath.Join(other, ".git"))
	t.Setenv("GIT_WORK_TREE", other)
	var ordinary struct {
		Root string `json:"worktree_root"`
	}
	if err := json.Unmarshal([]byte(invoke(t, selected, nil, "status")), &ordinary); err != nil {
		t.Fatal(err)
	}
	physicalOther, _ := filepath.EvalSymlinks(other)
	if ordinary.Root != physicalOther {
		t.Fatal("ordinary Git-context behavior changed")
	}
	var boundary struct {
		Before string `json:"before"`
	}
	if err := json.Unmarshal([]byte(invoke(t, selected, map[string]string{"agent": "opencode", "source_session_id": "opencode:physical", "call_id": "tool"}, "frontier", "begin", "--physical-worktree")), &boundary); err != nil || boundary.Before != head {
		t.Fatal("native frontier drifted to another worktree", err)
	}
	input := observation("physical")
	input.Agent.Name = "opencode"
	invoke(t, selected, input, "capture", "--physical-worktree", "--pin-source")
	for _, item := range []struct {
		Root  string
		Count int
	}{{selected, 1}, {other, 0}} {
		var status struct {
			Queue struct {
				Recordings int `json:"recordings"`
			} `json:"queue"`
		}
		if err := json.Unmarshal([]byte(invoke(t, item.Root, nil, "status", "--physical-worktree")), &status); err != nil || status.Queue.Recordings != item.Count {
			t.Fatal("native capture crossed physical repository boundary", err)
		}
	}
}

func TestDisableSurvivesRestoringAnOlderEnabledLedger(t *testing.T) {
	root, _ := repository(t)
	initialize(t, root, "http://127.0.0.1:1")
	ledger := filepath.Join(root, ".git", "workgraph", "state.sqlite")
	before, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ledger, []byte("damaged"), 0600); err != nil {
		t.Fatal(err)
	}
	invoke(t, root, nil, "disable")
	if err := os.WriteFile(ledger, before, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := cli(root, observation("after-restore"), "capture"); err == nil {
		t.Fatal("restored enabled ledger bypassed disabled consent")
	}
	invoke(t, root, nil, "enable")
	invoke(t, root, observation("after-explicit-enable"), "capture")
}

func TestRepositoryDisableBlocksAgentCaptureUntilExplicitReenable(t *testing.T) {
	root, _ := repository(t)
	initialize(t, root, "http://127.0.0.1:1")
	invoke(t, root, observation("initial"), "capture")
	invoke(t, root, nil, "disable")
	if output, e := cli(root, observation("blocked"), "capture"); e == nil || strings.Contains(output, "never-upload-raw") {
		t.Fatal("disabled repository still captures", e)
	}
	var status struct {
		Enabled bool `json:"collection_enabled"`
	}
	if e := json.Unmarshal([]byte(invoke(t, root, nil, "status")), &status); e != nil || status.Enabled {
		t.Fatal("disabled state not visible", e)
	}
	invoke(t, root, nil, "enable")
	invoke(t, root, nil, "agent", "disable", "opencode", "--local")
	input := observation("opencode-blocked")
	input.Agent.Name = "opencode"
	if _, e := cli(root, input, "capture"); e == nil {
		t.Fatal("disabled local agent still captures")
	}
	input.Agent.Name = "codex"
	input.EventID = "other-agent"
	invoke(t, root, input, "capture")
	invoke(t, root, nil, "agent", "enable", "opencode", "--local")
	input.Agent.Name = "opencode"
	input.EventID = "opencode-enabled"
	invoke(t, root, input, "capture")
}
