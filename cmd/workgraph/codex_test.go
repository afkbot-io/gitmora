package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	capture "workgraph.local/platform/internal/capture/domain"
)

func codexProxy(t *testing.T, root, sentinel string) string {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 required for local Codex protocol fixture")
	}
	rootJSON, _ := json.Marshal(root)
	sentinelJSON, _ := json.Marshal(sentinel)
	script := "#!" + python + `
import json, sys
root = ` + string(rootJSON) + `
sentinel = ` + string(sentinelJSON) + `
for line in sys.stdin:
    q = json.loads(line)
    m = q['method']
    if m == 'initialized': continue
    if m == 'initialize': result = {'userAgent':'fixture-codex/1'}
    elif m == 'thread/read': result = {'thread':{'id':'fixture-thread','cwd':root,'createdAt':1793318400,'updatedAt':1793318401}}
    elif m == 'thread/items/list': result = {'data':[
        {'turnId':'turn','item':{'id':'user','type':'userMessage','content':[{'type':'text','text':sentinel}]}},
        {'turnId':'turn','item':{'id':'edit','type':'fileChange','status':'completed','changes':[{'path':root+'/a.go','diff':'+ fixture','kind':{'type':'update'}}]}},
        {'turnId':'turn','item':{'id':'tool','type':'commandExecution','command':'git commit','cwd':root,'status':'completed','aggregatedOutput':'Exact visible result','exitCode':0}},
        {'turnId':'turn','item':{'id':'answer','type':'agentMessage','text':'Exact final answer'}}], 'nextCursor':None}
    else: sys.exit(21)
    print(json.dumps({'id':q['id'],'result':result}), flush=True)
`
	executable := filepath.Join(t.TempDir(), "codex proxy fixture")
	if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return executable
}

func TestCodexHooksBindExactObservedCommitAndDeliverAfterLaterHumanHead(t *testing.T) {
	root, _ := repository(t)
	var received []capture.Snapshot
	var mu sync.Mutex
	server := ackServer(t, nil, &received, &mu)
	initialize(t, root, server.URL)
	exe := codexProxy(t, root, "Exact Codex source sentinel")
	hook := map[string]any{"session_id": "fixture-thread", "cwd": root, "model": "fixture-model", "tool_use_id": "tool"}
	hook["hook_event_name"] = "PreToolUse"
	invoke(t, root, hook, "internal-codex-hook", "--codex", exe)
	write(t, root, "a.go", "fixture")
	git(t, root, "add", "a.go")
	git(t, root, "commit", "-m", "agent commit")
	agentSHA := git(t, root, "rev-parse", "HEAD")
	hook["hook_event_name"] = "PostToolUse"
	invoke(t, root, hook, "internal-codex-hook", "--codex", exe)
	// A failed native history read must retain the observed end across another process and later HEAD.
	hook["hook_event_name"] = "Stop"
	invoke(t, root, hook, "internal-codex-hook", "--codex", filepath.Join(t.TempDir(), "unavailable-codex"))
	write(t, root, "human.go", "human")
	git(t, root, "add", "human.go")
	git(t, root, "commit", "-m", "later human commit")
	humanSHA := git(t, root, "rev-parse", "HEAD")
	hook["hook_event_name"] = "Stop"
	for range 2 {
		invoke(t, root, hook, "internal-codex-hook", "--codex", exe)
	}
	git(t, root, "push", "origin", "main")
	mu.Lock()
	defer mu.Unlock()
	if len(received) == 0 {
		t.Fatal("Codex history not delivered on ordinary push")
	}
	found := false
	for _, snapshot := range received {
		if snapshot.Agent.Name != "codex" {
			continue
		}
		raw, _ := json.Marshal(snapshot)
		if strings.Contains(string(raw), "Exact Codex source sentinel") && has(snapshot.Commits, agentSHA) {
			if len(snapshot.CommitRanges) != 1 || snapshot.CommitRanges[0].CommitSHA != agentSHA || snapshot.CommitRanges[0].Start != 0 || snapshot.CommitRanges[0].End != 3 {
				t.Fatal("native checkpoint included later messages", snapshot.CommitRanges)
			}
			found = true
		}
		if has(snapshot.Commits, humanSHA) {
			t.Fatal("later human HEAD was guessed as agent commit")
		}
	}
	if !found {
		t.Fatal("exact native source/commit association missing")
	}
}

func TestCodexHookSkipsSourceWhenRepositoryCollectionIsDisabled(t *testing.T) {
	root, _ := repository(t)
	initialize(t, root, "http://127.0.0.1:1")
	invoke(t, root, nil, "disable")
	exe := filepath.Join(t.TempDir(), "should-not-execute")
	marker := filepath.Join(t.TempDir(), "executed")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n: > '"+strings.ReplaceAll(marker, "'", "'\"'\"'")+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	out := invoke(t, root, map[string]any{"hook_event_name": "Stop", "session_id": "fixture-thread", "cwd": root}, "internal-codex-hook", "--codex", exe)
	if strings.TrimSpace(out) != "{}" {
		t.Fatal("hook control output was not neutral", out)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("disabled source accessed")
	}
}
