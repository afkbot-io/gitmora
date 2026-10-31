package main

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	capture "workgraph.local/platform/internal/capture/domain"
	"workgraph.local/platform/internal/collector/domain"
)

func TestFrontierNewProcessesRecoverFrozenGitWindowWithoutLaterHumanCommit(t *testing.T) {
	root, _ := repository(t)
	var received []capture.Snapshot
	var mu sync.Mutex
	server := ackServer(t, nil, &received, &mu)
	initialize(t, root, server.URL)
	input := map[string]string{"agent": "generic-local", "source_session_id": "native", "call_id": "tool"}
	var f domain.Frontier
	if err := json.Unmarshal([]byte(invoke(t, root, input, "frontier", "begin")), &f); err != nil {
		t.Fatal(err)
	}
	before := git(t, root, "rev-parse", "HEAD")
	write(t, root, "first.txt", "first")
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "tool first")
	first := git(t, root, "rev-parse", "HEAD")
	write(t, root, "second.txt", "second")
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "tool second")
	end := git(t, root, "rev-parse", "HEAD")
	invoke(t, root, input, "frontier", "end")
	// The agent can be absent for an arbitrary interval after the durable end.
	write(t, root, "human.txt", "human")
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "later human work")
	human := git(t, root, "rev-parse", "HEAD")
	if err := json.Unmarshal([]byte(invoke(t, root, input, "frontier", "end")), &f); err != nil || f.Before != before || f.After == nil || *f.After != end {
		t.Fatal("retry extended window", err)
	}
	o := observation("window")
	o.History = &domain.NativeHistory{PageID: strings.Repeat("a", 64), Epoch: strings.Repeat("e", 64), MessageIDs: []string{strings.Repeat("a", 64)}, Reason: "window"}
	invoke(t, root, o, "capture", "--pin-source", "--frontier", f.ID)
	// Capture and association use separate processes; a lost acknowledgement remains recoverable.
	invoke(t, root, nil, "frontier", "apply", "--id", f.ID)
	invoke(t, root, nil, "frontier", "apply", "--id", f.ID)
	if stats(t, root).Eligible != 0 {
		t.Fatal("recovery granted publication")
	}
	git(t, root, "push", "origin", "main")
	mu.Lock()
	defer mu.Unlock()
	if len(received) == 0 {
		t.Fatal("no recovered source delivered")
	}
	final := received[len(received)-1]
	if len(final.Commits) != 2 || !containsCommit(final.Commits, first) || !containsCommit(final.Commits, end) || containsCommit(final.Commits, human) {
		t.Fatalf("wrong boundaries: %v", final.Commits)
	}
}

func TestFrontierCLILeavesUnknownEndAndValidatesExplicitResolution(t *testing.T) {
	root, _ := repository(t)
	initialize(t, root, "http://127.0.0.1:1")
	input := map[string]string{"agent": "generic-local", "source_session_id": "native", "call_id": "never-ended"}
	var f domain.Frontier
	if err := json.Unmarshal([]byte(invoke(t, root, input, "frontier", "begin")), &f); err != nil {
		t.Fatal(err)
	}
	if _, err := cli(root, nil, "frontier", "apply", "--id", f.ID); err == nil {
		t.Fatal("unknown boundary applied")
	}
	if _, err := cli(root, nil, "frontier", "resolve", "--id", f.ID, "--end", strings.Repeat("a", 40), "--reason", "unavailable Git object"); err == nil {
		t.Fatal("nonexistent end accepted")
	}
	var pending struct {
		Frontiers []domain.Frontier `json:"frontiers"`
	}
	if err := json.Unmarshal([]byte(invoke(t, root, nil, "frontier", "list")), &pending); err != nil || len(pending.Frontiers) != 1 || pending.Frontiers[0].After != nil {
		t.Fatal("unknown end was inferred", err)
	}
	invoke(t, root, nil, "frontier", "resolve", "--id", f.ID, "--end", git(t, root, "rev-parse", "HEAD"), "--reason", "verified explicit end")
	invoke(t, root, nil, "frontier", "abandon", "--id", f.ID, "--reason", "operator closed unused association")
	if _, err := cli(root, nil, "frontier", "apply", "--id", f.ID); err == nil {
		t.Fatal("abandoned frontier applied")
	}
}

func containsCommit(commits []string, sha string) bool {
	for _, commit := range commits {
		if commit == sha {
			return true
		}
	}
	return false
}
