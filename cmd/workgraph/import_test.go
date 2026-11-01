package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func importNote(t *testing.T, root, target string, end int) {
	t.Helper()
	raw := fmt.Sprintf("base.txt\n  abcdef0 1-%d\n---\n{\"schema_version\":\"authorship/3.0.0\",\"base_commit_sha\":%q,\"prompts\":{\"abcdef0\":{\"agent_id\":{\"tool\":\"fixture\",\"id\":\"source-session\",\"model\":\"local\"}}}}", end, target)
	git(t, root, "notes", "--ref=ai", "add", "-f", "-m", raw, target)
}

type importReply struct {
	Status   string   `json:"status"`
	Sessions []string `json:"session_ids"`
	Windows  int      `json:"windows"`
}

func TestImportCLIIsDurablePrivateAndReplaysOriginalSession(t *testing.T) {
	root, _ := repository(t)
	initialize(t, root, "http://127.0.0.1:1")
	target := git(t, root, "rev-parse", "HEAD")
	importNote(t, root, target, 1)
	var first, second importReply
	if err := json.Unmarshal([]byte(invoke(t, root, nil, "import", "git-ai", "--commit", target)), &first); err != nil {
		t.Fatal(err)
	}
	if first.Status != "recorded" || len(first.Sessions) != 1 || first.Windows != 1 {
		t.Fatalf("bad import receipt: %+v", first)
	}
	// Independent CLI process must retain the immutable import's initial pin.
	if err := json.Unmarshal([]byte(invoke(t, root, nil, "import", "git-ai", "--commit", target, "--sync")), &second); err != nil {
		t.Fatal(err)
	}
	if strings.Join(first.Sessions, ",") != strings.Join(second.Sessions, ",") {
		t.Fatal("retry forked source session")
	}
	status := invoke(t, root, nil, "status")
	queue := stats(t, root)
	if queue.Recordings != 1 || queue.Eligible != 0 || queue.Pending != 0 {
		t.Fatalf("import granted publication: %s", status)
	}
	before := status
	importNote(t, root, target, 2)
	if raw, err := cli(root, nil, "import", "git-ai", "--commit", target); err == nil || strings.Contains(raw, "source-session") {
		t.Fatalf("bad source accepted/exposed: %v %s", err, raw)
	}
	if after := invoke(t, root, nil, "status"); before != after {
		t.Fatal("invalid complete plan changed ledger")
	}
}
func TestImportHelpAndValidationHaveNoRepositorySideEffects(t *testing.T) {
	root := t.TempDir()
	if raw, err := cli(root, nil, "import", "--help"); err != nil || !strings.Contains(raw, "git-ai") {
		t.Fatalf("namespace help: %v %s", err, raw)
	}
	if raw, err := cli(root, nil, "import", "entire", "--help"); err != nil || !strings.Contains(raw, "-checkpoint") {
		t.Fatalf("source help: %v %s", err, raw)
	}
	for _, args := range [][]string{{"import", "git-ai", "--commit", "HEAD"}, {"import", "entire", "--commit", strings.Repeat("a", 40)}, {"import", "git-ai", "--commit", strings.Repeat("a", 40), "--storage", "refs"}} {
		if _, err := cli(root, nil, args...); err == nil {
			t.Fatalf("invalid input: %v", args)
		}
	}
	files, err := os.ReadDir(root)
	if err != nil || len(files) != 0 {
		t.Fatal("help/invalid input created repository state")
	}
}
