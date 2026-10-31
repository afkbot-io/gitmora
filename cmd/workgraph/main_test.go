package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	capture "workgraph.local/platform/internal/capture/domain"
	"workgraph.local/platform/internal/collector/application"
	"workgraph.local/platform/internal/collector/domain"
)

var binary string

func TestMain(m *testing.M) {
	if external := os.Getenv("WORKGRAPH_TEST_CLI"); external != "" {
		binary = external
		os.Exit(m.Run())
	}
	directory, err := os.MkdirTemp("", "workgraph CLI space's-")
	if err != nil {
		panic(err)
	}
	binary = filepath.Join(directory, "workgraph")
	command := exec.Command("go", "build", "-o", binary, ".")
	if output, err := command.CombinedOutput(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, string(output))
		_ = os.RemoveAll(directory)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(directory)
	os.Exit(code)
}

func environment() []string {
	return append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.invalid", "GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.invalid", "WORKGRAPH_TOKEN=wg_"+strings.Repeat("A", 43))
}
func gitCommand(root string, args ...string) (string, error) {
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	command.Env = environment()
	output, err := command.CombinedOutput()
	return strings.TrimSpace(string(output)), err
}
func git(t *testing.T, root string, args ...string) string {
	t.Helper()
	output, err := gitCommand(root, args...)
	if err != nil {
		t.Fatalf("Git %v: %v %s", args, err, output)
	}
	return output
}
func cli(root string, input any, args ...string) (string, error) {
	var raw []byte
	if input != nil {
		raw, _ = json.Marshal(input)
	}
	command := exec.Command(binary, args...)
	command.Dir = root
	command.Env = environment()
	command.Stdin = bytes.NewReader(raw)
	output, err := command.CombinedOutput()
	return string(output), err
}
func invoke(t *testing.T, root string, input any, args ...string) string {
	t.Helper()
	output, err := cli(root, input, args...)
	if err != nil {
		t.Fatalf("CLI %v: %v %s", args, err, output)
	}
	return output
}
func repository(t *testing.T) (string, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "repo space's")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	git(t, root, "init", "-b", "main")
	write(t, root, "base.txt", "base")
	git(t, root, "add", ".")
	git(t, root, "commit", "-m", "base")
	remote := filepath.Join(t.TempDir(), "bare space's.git")
	git(t, root, "init", "--bare", remote)
	git(t, root, "remote", "add", "origin", remote)
	git(t, root, "push", "-u", "origin", "main")
	return root, remote
}
func write(t *testing.T, root, path, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, path), []byte(text), 0700); err != nil {
		t.Fatal(err)
	}
}
func observation(id string, files ...string) domain.Observation {
	return domain.Observation{SchemaVersion: 1, SourceSessionID: "native", EventID: id, Agent: capture.Agent{Name: "generic-local", Model: "local-model"}, Messages: []capture.Message{{Role: "user", Text: "DB_PASSWORD=never-upload-raw"}}, ChangedFiles: files}
}
func initialize(t *testing.T, root, server string) {
	invoke(t, root, nil, "init", "--server", server, "--repository-id", "11111111-1111-4111-8111-111111111111")
}
func stats(t *testing.T, root string) application.Stats {
	t.Helper()
	var status struct {
		Queue application.Stats `json:"queue"`
	}
	if err := json.Unmarshal([]byte(invoke(t, root, nil, "status")), &status); err != nil {
		t.Fatal(err)
	}
	return status.Queue
}

func ackServer(t *testing.T, fail *atomic.Bool, received *[]capture.Snapshot, mu *sync.Mutex) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail != nil && fail.Load() {
			w.WriteHeader(503)
			return
		}
		var input capture.Snapshot
		if json.NewDecoder(r.Body).Decode(&input) != nil {
			t.Error("bad upload")
			w.WriteHeader(400)
			return
		}
		session, err := capture.NewSession(input)
		if err != nil {
			t.Error(err)
			w.WriteHeader(422)
			return
		}
		mu.Lock()
		*received = append(*received, input)
		mu.Unlock()
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{"repository_id": input.RepositoryID, "session_id": input.SessionID, "revision": input.Revision, "digest": session.Digest(), "created": true})
	}))
	t.Cleanup(server.Close)
	return server
}

func TestOrdinaryMultiCommitPushDeliversSanitizedContextAndPreservesUserHooks(t *testing.T) {
	root, _ := repository(t)
	var received []capture.Snapshot
	var mu sync.Mutex
	server := ackServer(t, nil, &received, &mu)
	write(t, root, ".git/hooks/pre-commit", "#!/bin/sh\nprintf 'ran' >> .git/original-commit\n")
	write(t, root, ".git/hooks/pre-push", "#!/bin/sh\ncat > .git/original-push-input\n")
	initialize(t, root, server.URL)
	write(t, root, "a.go", "a")
	write(t, root, "b.go", "b")
	invoke(t, root, observation("turn-1", "a.go", "b.go"), "capture")
	git(t, root, "add", "a.go")
	git(t, root, "commit", "-m", "first")
	first := git(t, root, "rev-parse", "HEAD")
	git(t, root, "add", "b.go")
	git(t, root, "commit", "-m", "second")
	second := git(t, root, "rev-parse", "HEAD")
	if stats(t, root).Pending != 2 {
		t.Fatal("commits did not seal durable context")
	}
	git(t, root, "push", "origin", "main")
	if stats(t, root).Pending != 0 {
		t.Fatal("normal push did not acknowledge context")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(received) != 2 {
		t.Fatalf("uploads=%d", len(received))
	}
	if !has(received[1].Commits, first) || !has(received[1].Commits, second) || strings.Contains(received[1].Messages[0].Text, "never-upload-raw") {
		t.Fatal("commit history or redaction lost")
	}
	if raw, err := os.ReadFile(filepath.Join(root, ".git/original-push-input")); err != nil || !strings.Contains(string(raw), second) {
		t.Fatal("existing pre-push did not receive intact stdin")
	}
	if raw, err := os.ReadFile(filepath.Join(root, ".git/original-commit")); err != nil || string(raw) != "ranran" {
		t.Fatal("existing pre-commit changed")
	}
	invoke(t, root, nil, "disable")
	if raw, err := os.ReadFile(filepath.Join(root, ".git/hooks/pre-push")); err != nil || !strings.Contains(string(raw), "original-push-input") {
		t.Fatal("disable changed original hook")
	}
}

func TestUnavailableServerDoesNotBlockGitAndManualRetrySurvivesProcessRestart(t *testing.T) {
	root, remote := repository(t)
	var fail atomic.Bool
	fail.Store(true)
	var received []capture.Snapshot
	var mu sync.Mutex
	server := ackServer(t, &fail, &received, &mu)
	initialize(t, root, server.URL)
	write(t, root, "a.go", "a")
	invoke(t, root, observation("turn-1", "a.go"), "capture")
	git(t, root, "add", "a.go")
	git(t, root, "commit", "-m", "change")
	head := git(t, root, "rev-parse", "HEAD")
	git(t, root, "push", "origin", "main")
	if git(t, remote, "rev-parse", "refs/heads/main") != head || stats(t, root).Pending != 1 {
		t.Fatal("code push or offline queue failed")
	}
	fail.Store(false)
	invoke(t, root, nil, "sync", "--retry")
	if stats(t, root).Pending != 0 {
		t.Fatal("separate retry process did not recover")
	}
}

func TestExistingRejectingPrePushKeepsItsAuthority(t *testing.T) {
	root, _ := repository(t)
	var received []capture.Snapshot
	var mu sync.Mutex
	server := ackServer(t, nil, &received, &mu)
	write(t, root, ".git/hooks/pre-push", "#!/bin/sh\ncat > .git/rejected-input\nexit 17\n")
	initialize(t, root, server.URL)
	write(t, root, "a.go", "a")
	invoke(t, root, observation("turn-1", "a.go"), "capture")
	git(t, root, "add", "a.go")
	git(t, root, "commit", "-m", "change")
	if _, err := gitCommand(root, "push", "origin", "main"); err == nil {
		t.Fatal("existing rejection was bypassed")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(received) != 0 {
		t.Fatal("context sent before existing pre-push acceptance")
	}
}

func TestAmendPreservesOldAndNewCommitAssociations(t *testing.T) {
	root, _ := repository(t)
	var received []capture.Snapshot
	var mu sync.Mutex
	server := ackServer(t, nil, &received, &mu)
	initialize(t, root, server.URL)
	write(t, root, "a.go", "a")
	invoke(t, root, observation("turn-1", "a.go"), "capture")
	git(t, root, "add", "a.go")
	git(t, root, "commit", "-m", "change")
	old := git(t, root, "rev-parse", "HEAD")
	git(t, root, "commit", "--amend", "-m", "amended change")
	newSHA := git(t, root, "rev-parse", "HEAD")
	git(t, root, "push", "origin", "main")
	if stats(t, root).Pending != 0 {
		t.Fatal("rewritten historical context was stranded in the queue")
	}
	mu.Lock()
	defer mu.Unlock()
	found := false
	for _, snapshot := range received {
		if has(snapshot.Commits, newSHA) && has(snapshot.Commits, old) {
			found = true
		}
	}
	if !found {
		t.Fatal("post-rewrite mapping was lost")
	}
}

func TestRebaseAndLinkedWorktreeKeepContextScoped(t *testing.T) {
	root, _ := repository(t)
	var received []capture.Snapshot
	var mu sync.Mutex
	server := ackServer(t, nil, &received, &mu)
	linked := filepath.Join(t.TempDir(), "linked space's")
	git(t, root, "worktree", "add", "-b", "feature", linked)
	initialize(t, root, server.URL)
	initialize(t, linked, server.URL)
	write(t, linked, "a.go", "feature")
	invoke(t, linked, observation("turn-1", "a.go"), "capture")
	git(t, linked, "add", "a.go")
	git(t, linked, "commit", "-m", "feature change")
	old := git(t, linked, "rev-parse", "HEAD")
	// The same native identifier in the main worktree is an independent recording.
	write(t, root, "main.go", "main")
	invoke(t, root, observation("turn-1", "main.go"), "capture")
	git(t, root, "add", "main.go")
	git(t, root, "commit", "-m", "main change")
	mainSHA := git(t, root, "rev-parse", "HEAD")
	git(t, linked, "rebase", "main")
	rebased := git(t, linked, "rev-parse", "HEAD")
	git(t, linked, "push", "origin", "feature")
	mu.Lock()
	defer mu.Unlock()
	featureID, mainID := "", ""
	for _, snapshot := range received {
		if has(snapshot.Commits, rebased) {
			featureID = snapshot.SessionID
			if !has(snapshot.Commits, old) || has(snapshot.Commits, mainSHA) {
				t.Fatal("rebase association mixed worktrees")
			}
		}
		if has(snapshot.Commits, mainSHA) {
			mainID = snapshot.SessionID
		}
	}
	if featureID == "" || mainID == "" || featureID == mainID {
		t.Fatal("shared ledger lost worktree-scoped sessions")
	}
}

func TestDisableWorksEvenWhenCollectorDatabaseIsCorrupt(t *testing.T) {
	root, _ := repository(t)
	initialize(t, root, "http://127.0.0.1:8080")
	if err := os.WriteFile(filepath.Join(root, ".git", "workgraph", "state.sqlite"), []byte("corrupted ledger"), 0600); err != nil {
		t.Fatal(err)
	}
	invoke(t, root, nil, "disable")
	if _, err := gitCommand(root, "config", "--local", "--get", "core.hooksPath"); err == nil {
		t.Fatal("managed hooks still selected")
	}
}

func TestIndependentCaptureProcessesDeduplicateTheSameEvent(t *testing.T) {
	root, _ := repository(t)
	initialize(t, root, "http://127.0.0.1:8080")
	write(t, root, "a.go", "a")
	var wg sync.WaitGroup
	results := make(chan string, 12)
	failures := make(chan error, 12)
	for range 12 {
		wg.Go(func() {
			output, err := cli(root, observation("turn-1", "a.go"), "capture")
			if err != nil {
				failures <- err
				return
			}
			var result struct {
				ID string `json:"session_id"`
			}
			if err := json.Unmarshal([]byte(output), &result); err != nil {
				failures <- err
				return
			}
			results <- result.ID
		})
	}
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	seen := ""
	for id := range results {
		if seen == "" {
			seen = id
		} else if seen != id {
			t.Fatal("separate capture processes created duplicate sessions")
		}
	}
	if seen == "" || stats(t, root).Recordings != 1 {
		t.Fatal("concurrent capture was not persisted exactly once")
	}
}

func TestRemoteRejectionDoesNotConfuseContextDeliveryWithGitSuccess(t *testing.T) {
	root, remote := repository(t)
	previous := git(t, remote, "rev-parse", "refs/heads/main")
	var received []capture.Snapshot
	var mu sync.Mutex
	server := ackServer(t, nil, &received, &mu)
	initialize(t, root, server.URL)
	write(t, root, "a.go", "a")
	invoke(t, root, observation("turn-1", "a.go"), "capture")
	git(t, root, "add", "a.go")
	git(t, root, "commit", "-m", "change")
	head := git(t, root, "rev-parse", "HEAD")
	write(t, remote, "hooks/pre-receive", "#!/bin/sh\nexit 1\n")
	if _, err := gitCommand(root, "push", "origin", "main"); err == nil {
		t.Fatal("remote rejection was bypassed")
	}
	if git(t, remote, "rev-parse", "refs/heads/main") != previous {
		t.Fatal("rejected code became remote branch state")
	}
	if stats(t, root).Pending != 0 {
		t.Fatal("acknowledged context was incorrectly tied to code transport success")
	}
	mu.Lock()
	uploaded := len(received) == 1 && has(received[0].Commits, head)
	mu.Unlock()
	if !uploaded {
		t.Fatal("pending context did not survive a rejected code push")
	}
	if err := os.Remove(filepath.Join(remote, "hooks/pre-receive")); err != nil {
		t.Fatal(err)
	}
	git(t, root, "push", "origin", "main")
	if git(t, remote, "rev-parse", "refs/heads/main") != head {
		t.Fatal("code retry failed")
	}
}

func has(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func TestNativeCaptureAfterCombinedCommitPushAndFinalReply(t *testing.T) {
	root, _ := repository(t)
	var received []capture.Snapshot
	var mu sync.Mutex
	server := ackServer(t, nil, &received, &mu)
	initialize(t, root, server.URL)
	base := git(t, root, "rev-parse", "HEAD")
	input := observation("native-before")
	input.ChangedFiles = nil
	invoke(t, root, input, "capture", "--pin-source")
	write(t, root, "native.go", "package native")
	git(t, root, "add", "native.go")
	git(t, root, "commit", "-m", "native one")
	first := git(t, root, "rev-parse", "HEAD")
	write(t, root, "second.go", "package native")
	git(t, root, "add", "second.go")
	git(t, root, "commit", "-m", "native two")
	second := git(t, root, "rev-parse", "HEAD")
	git(t, root, "push")
	if stats(t, root).Eligible != 0 {
		t.Fatal("context should wait for actual native tool observation")
	}
	input.EventID = "native-after"
	input.Messages = append(input.Messages, capture.Message{Role: "tool", ToolName: "bash", Text: "Two commits and push completed"})
	invoke(t, root, input, "capture", "--pin-source", "--observe-head-from", base, "--sync")
	input.EventID = "native-final"
	input.Messages = append(input.Messages, capture.Message{Role: "assistant", Text: "Final native reply after push"})
	invoke(t, root, input, "capture", "--pin-source", "--sync")
	private := observation("private")
	private.SourceSessionID = "unpublished"
	invoke(t, root, private, "capture", "--pin-source", "--sync")
	mu.Lock()
	defer mu.Unlock()
	if len(received) != 2 || !has(received[1].Commits, first) || !has(received[1].Commits, second) || received[1].Messages[len(received[1].Messages)-1].Text != "Final native reply after push" {
		t.Fatal("native late context lost, incomplete commit window, or private session uploaded")
	}
}

func TestStatusProbeDoesNotCreateStateInUninitializedRepository(t *testing.T) {
	root, _ := repository(t)
	if _, err := cli(root, nil, "status"); err == nil {
		t.Fatal("uninitialized repository reported enabled")
	}
	if _, err := os.Stat(filepath.Join(root, ".git", "workgraph")); !os.IsNotExist(err) {
		t.Fatal("read probe created collector files")
	}
}
