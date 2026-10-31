package codex_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"workgraph.local/platform/internal/agent/codex"
)

func readerFixture(t *testing.T, mode string) (string, string) {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 required for isolated proxy fixture")
	}
	root := t.TempDir()
	if out, err := exec.Command("git", "init", root).CombinedOutput(); err != nil {
		t.Fatalf("fixture Git: %v %s", err, out)
	}
	rootJSON, _ := json.Marshal(root)
	modeJSON, _ := json.Marshal(mode)
	script := `#!` + python + `
import json, sys
root = ` + string(rootJSON) + `
mode = ` + string(modeJSON) + `
page = 0
for line in sys.stdin:
    q = json.loads(line)
    method = q['method']
    if method == 'initialized': continue
    if method == 'initialize': result = {'userAgent':'fixture-codex/1'}
    elif method == 'thread/read': result = {'thread':{'id':q['params']['threadId'],'cwd': root if mode != 'wrong-repo' else '/other','createdAt':1793318400,'updatedAt':1793318401}}
    elif method == 'thread/items/list':
        page += 1
        if mode == 'approval':
            print(json.dumps({'id':999,'method':'item/commandExecution/requestApproval','params':{}}), flush=True)
            continue
        if mode == 'error':
            print(json.dumps({'id':q['id'],'error':{'code':1,'message':'SECRET_FROM_PROVIDER'}}), flush=True)
            continue
        if mode == 'slow':
            import time
            time.sleep(30)
        result = {'data':[{'turnId':'turn','item':{'id':str(page),'type':'agentMessage','text':'Exact page '+str(page)}}], 'nextCursor':'next' if page == 1 or mode == 'cycle' else None}
    else: sys.exit(21)
    print(json.dumps({'id':q['id'],'result':result}), flush=True)
`
	executable := filepath.Join(root, "codex fixture")
	if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return executable, root
}

func TestReaderUsesOnlyMetadataAndPagedHistoryWithoutStartingTurns(t *testing.T) {
	exe, root := readerFixture(t, "normal")
	physical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	thread, entries, err := (codex.Reader{Executable: exe}).Read(context.Background(), "thread", root)
	if err != nil || thread.CWD != physical || len(entries) != 2 || !strings.Contains(string(entries[1].Item), "Exact page 2") {
		t.Fatal("paged local history lost", err, len(entries))
	}
}

func TestReaderRejectsCrossRepositoryHistoryCyclesAndServerRequests(t *testing.T) {
	for _, mode := range []string{"wrong-repo", "cycle", "approval", "error"} {
		t.Run(mode, func(t *testing.T) {
			exe, root := readerFixture(t, mode)
			_, _, err := (codex.Reader{Executable: exe}).Read(context.Background(), "thread", root)
			if err == nil || strings.Contains(err.Error(), "SECRET_FROM_PROVIDER") {
				t.Fatal("unsafe source response accepted", err)
			}
		})
	}
}

func TestReaderCancellationTerminatesBlockedProxy(t *testing.T) {
	exe, root := readerFixture(t, "slow")
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	_, _, err := (codex.Reader{Executable: exe}).Read(ctx, "thread", root)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("deadline lost", err)
	}
}
