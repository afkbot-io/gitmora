package gitsource

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"workgraph.local/platform/internal/compatibility/domain"
)

func git(t *testing.T, root, input string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", root}, args...)...)
	c.Env = append(isolatedEnvironment(), "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
	c.Stdin = strings.NewReader(input)
	b, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("Git %v: %v %s", args, err, b)
	}
	return strings.TrimSpace(string(b))
}
func repo(t *testing.T, format string) (string, string) {
	t.Helper()
	root := t.TempDir()
	git(t, root, "", "init", "-b", "main", "--object-format="+format)
	if err := os.WriteFile(filepath.Join(root, "code.go"), []byte("one\ntwo\nthree\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, root, "", "add", "code.go")
	git(t, root, "", "commit", "-m", "fixture")
	return root, git(t, root, "", "rev-parse", "HEAD")
}
func blob(t *testing.T, root, raw string) string {
	return git(t, root, raw, "hash-object", "-w", "--stdin")
}
func tree(t *testing.T, root, entries string) string { return git(t, root, entries, "mktree", "-z") }
func attach(t *testing.T, root, ref, object string) string {
	t.Helper()
	commit := git(t, root, "fixture\n", "commit-tree", object)
	git(t, root, "", "update-ref", ref, commit)
	return commit
}
func note(target string, end int) string {
	return fmt.Sprintf("code.go\n  abcdef0 1-%d\n---\n{\"schema_version\":\"authorship/3.0.0\",\"base_commit_sha\":%q,\"prompts\":{\"abcdef0\":{\"agent_id\":{\"tool\":\"fixture\",\"id\":\"session\",\"model\":\"local\"}}}}\n", end, target)
}

func TestGitAIImmutableAttachmentAndTargetRanges(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			root, target := repo(t, format)
			noteID := blob(t, root, note(target, 3))
			inner := tree(t, root, "100644 blob "+noteID+"\t"+target[2:]+"\x00")
			treeID := tree(t, root, "040000 tree "+inner+"\t"+target[:2]+"\x00")
			pinned := attach(t, root, "refs/notes/ai", treeID)
			projections, err := Acquire(context.Background(), root, Request{System: "git-ai", Commit: target})
			if err != nil || len(projections) != 1 {
				t.Fatalf("acquire %d: %v", len(projections), err)
			}
			windows, err := projections[0].Windows()
			if err != nil {
				t.Fatal(err)
			}
			o := windows[0].Origin
			if o.ObjectID != noteID || o.RefObject != pinned || o.TargetCommit != target || len(o.Claims) != 1 {
				t.Fatalf("wrong immutable scope: %+v", o)
			}
			// Moving the source ref cannot redirect an already pinned object lookup.
			r := reader{root: root, remaining: domain.MaxSourceBytes}
			git(t, root, "", "update-ref", "refs/notes/ai", target)
			replayed, err := Acquire(context.Background(), root, Request{System: "git-ai", Commit: target, SourceRef: "refs/notes/ai", SourceObject: pinned})
			if err != nil || len(replayed) != 1 {
				t.Fatalf("immutable replay after ref move: %v", err)
			}
			data, err := r.object(context.Background(), noteID, "blob")
			if err != nil || !bytes.Equal(data, []byte(note(target, 3))) {
				t.Fatalf("pinned blob changed: %v", err)
			}
			bad := blob(t, root, note(target, 4))
			attach(t, root, "refs/notes/ai", tree(t, root, "100644 blob "+bad+"\t"+target+"\x00"))
			if _, err := Acquire(context.Background(), root, Request{System: "git-ai", Commit: target}); !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("out-of-target range accepted: %v", err)
			}
		})
	}
}

func TestNoLazyFetchOfMissingTargetObject(t *testing.T) {
	root, target := repo(t, "sha1")
	id := blob(t, root, note(target, 1))
	attach(t, root, "refs/notes/ai", tree(t, root, "100644 blob "+id+"\t"+target+"\x00"))
	missing := git(t, root, "", "rev-parse", target+":code.go")
	// A local executable fixture would leave a marker if a promisor transport were invoked.
	marker := filepath.Join(root, "transport-ran")
	script := filepath.Join(root, "transport.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\n: > '"+marker+"'\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	git(t, root, "", "config", "extensions.partialClone", "offline")
	git(t, root, "", "config", "remote.offline.promisor", "true")
	git(t, root, "", "config", "remote.offline.url", "ext::"+script)
	git(t, root, "", "config", "protocol.ext.allow", "always")
	if err := os.Remove(filepath.Join(root, ".git", "objects", missing[:2], missing[2:])); err != nil {
		t.Fatal(err)
	}
	if _, err := Acquire(context.Background(), root, Request{System: "git-ai", Commit: target}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("missing object: %v", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("acquisition executed a promisor transport")
	}
}

func TestOutputBoundAndCheckpointTrailer(t *testing.T) {
	root, _ := repo(t, "sha1")
	r := reader{root: root, remaining: 100}
	if _, err := r.run(context.Background(), 1, "version"); !errors.Is(err, domain.ErrLimit) {
		t.Fatalf("stdout limit: %v", err)
	}
	id := "abcdef012345"
	if !checkpointTrailer("Description\n\nReviewed-by: Fixture\nEntire-Checkpoint: "+id+"\n", id) {
		t.Fatal("valid trailer denied")
	}
	if checkpointTrailer("Description\nEntire-Checkpoint: "+id+"\nextra prose", id) || checkpointTrailer("Entire-Checkpoint: "+id+"\n\nLater body", id) {
		t.Fatal("body text became a target proof")
	}
}

func TestGitAIRejectsAmbiguousNotesSymlinksAndReplacements(t *testing.T) {
	root, target := repo(t, "sha1")
	id := blob(t, root, note(target, 2))
	inner := tree(t, root, "100644 blob "+id+"\t"+target[2:]+"\x00")
	attach(t, root, "refs/notes/ai", tree(t, root, "100644 blob "+id+"\t"+target+"\x00"+"040000 tree "+inner+"\t"+target[:2]+"\x00"))
	if _, err := Acquire(context.Background(), root, Request{System: "git-ai", Commit: target}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("ambiguous note: %v", err)
	}
	attach(t, root, "refs/notes/ai", tree(t, root, "120000 blob "+id+"\t"+target+"\x00"))
	if _, err := Acquire(context.Background(), root, Request{System: "git-ai", Commit: target}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("symlink note: %v", err)
	}
	attach(t, root, "refs/notes/ai", tree(t, root, "100644 blob "+id+"\t"+target+"\x00"))
	// Replacement changes target content to one line; acquisition must still use the original object.
	if err := os.WriteFile(filepath.Join(root, "code.go"), []byte("short\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git(t, root, "", "add", ".")
	git(t, root, "", "commit", "-m", "replacement")
	replacement := git(t, root, "", "rev-parse", "HEAD")
	git(t, root, "", "replace", target, replacement)
	t.Setenv("GIT_DIR", filepath.Join(t.TempDir(), "missing"))
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.fsmonitor")
	t.Setenv("GIT_CONFIG_VALUE_0", "false")
	if _, err := Acquire(context.Background(), root, Request{System: "git-ai", Commit: target}); err != nil {
		t.Fatalf("inherited overrides/replacement applied: %v", err)
	}
}

func entireFixture(t *testing.T, root, target, id, storage string, mutate func(map[string]any)) string {
	t.Helper()
	meta := map[string]any{"checkpoint_id": id, "session_id": "source-session", "created_at": "2026-10-29T12:50:26Z", "agent": "fixture", "files_touched": []string{"code.go"}}
	mb, _ := json.Marshal(meta)
	session := tree(t, root, "100644 blob "+blob(t, root, string(mb))+"\tmetadata.json\x00"+"100644 blob "+blob(t, root, "{\"v\":1,\"type\":\"user\",\"content\":\"fixture text\"}\n")+"\ttranscript.jsonl\x00")
	prefix := ""
	ref := "refs/entire/checkpoints/" + strings.ToLower(id[len(id)-2:]) + "/" + id
	if storage == "branch" {
		prefix = id[:2] + "/" + id[2:] + "/"
		ref = "refs/heads/entire/checkpoints/v1"
	}
	summary := map[string]any{"checkpoint_id": id, "commit_sha": target, "sessions": []any{map[string]any{"metadata": "/" + prefix + "0/metadata.json", "compact_transcript": "/" + prefix + "0/transcript.jsonl"}}}
	if mutate != nil {
		mutate(summary)
	}
	raw, _ := json.Marshal(summary)
	checkpointTree := tree(t, root, "100644 blob "+blob(t, root, string(raw))+"\tmetadata.json\x00"+"040000 tree "+session+"\t0\x00")
	if storage == "branch" {
		checkpointTree = tree(t, root, "040000 tree "+checkpointTree+"\t"+id[2:]+"\x00")
		checkpointTree = tree(t, root, "040000 tree "+checkpointTree+"\t"+id[:2]+"\x00")
	}
	return attach(t, root, ref, checkpointTree)
}

func TestEntireLayoutsMembershipAndTargetProof(t *testing.T) {
	for _, storage := range []string{"refs", "branch"} {
		t.Run(storage, func(t *testing.T) {
			root, target := repo(t, "sha1")
			id := "01M63VCB10XSZD4BQ2PQMWGPXV"
			pinned := entireFixture(t, root, target, id, storage, nil)
			p, err := Acquire(context.Background(), root, Request{System: "entire", Commit: target, Checkpoint: id, Storage: storage, Session: "source-session"})
			if err != nil || len(p) != 1 {
				t.Fatalf("layout %d: %v", len(p), err)
			}
			w, err := p[0].Windows()
			if err != nil {
				t.Fatal(err)
			}
			if w[0].Origin.RefObject != pinned || w[0].Origin.SourceSessionID != "source-session" {
				t.Fatal("lost immutable session membership")
			}
			_, err = Acquire(context.Background(), root, Request{System: "entire", Commit: target, Checkpoint: id, Storage: storage, Session: "unknown"})
			if !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("unknown session: %v", err)
			}
			entireFixture(t, root, strings.Repeat("a", 40), id, storage, nil)
			if _, err := Acquire(context.Background(), root, Request{System: "entire", Commit: target, Checkpoint: id, Storage: storage}); !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("unrelated commit: %v", err)
			}
			entireFixture(t, root, target, id, storage, func(summary map[string]any) {
				summary["sessions"] = []any{map[string]any{"metadata": "/../0/metadata.json", "compact_transcript": "/0/transcript.jsonl"}}
			})
			if _, err := Acquire(context.Background(), root, Request{System: "entire", Commit: target, Checkpoint: id, Storage: storage}); !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("escaped checkpoint tree: %v", err)
			}
		})
	}
}

func TestBoundedObjectAndRequests(t *testing.T) {
	root, target := repo(t, "sha1")
	r := reader{root: root, remaining: 2}
	if _, err := r.object(context.Background(), blob(t, root, "oversized"), "blob"); !errors.Is(err, domain.ErrLimit) {
		t.Fatalf("budget ignored: %v", err)
	}
	for _, req := range []Request{{System: "git-ai", Commit: "HEAD"}, {System: "other", Commit: target}, {System: "git-ai", Commit: target, Session: "x"}, {System: "entire", Commit: target, Checkpoint: "../../"}} {
		if _, err := Acquire(context.Background(), root, req); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("invalid request: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Acquire(ctx, root, Request{System: "git-ai", Commit: target}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation hidden: %v", err)
	}
}

func TestObjectHashAndTypeAreVerified(t *testing.T) {
	root, _ := repo(t, "sha1")
	id := blob(t, root, "original")
	var encoded bytes.Buffer
	writer := zlib.NewWriter(&encoded)
	if _, err := writer.Write([]byte("blob 8\x00modified")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, ".git", "objects", id[:2], id[2:])
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	r := reader{root: root, remaining: 1000}
	if _, err := r.object(context.Background(), id, "blob"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("corrupt object accepted: %v", err)
	}
	if _, err := r.commit(context.Background(), id); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("blob became target commit: %v", err)
	}
}

func TestTargetLineCountAndBinaryBoundaries(t *testing.T) {
	for _, test := range []struct {
		name, content string
		end           int
		want          error
	}{
		{"no-final-newline", "one", 1, nil}, {"empty", "", 1, domain.ErrInvalid}, {"trailing-newline", "one\n", 2, domain.ErrInvalid},
		{"empty-last-line", "one\n\n", 2, nil}, {"binary", "one\x00two", 1, domain.ErrUnsupported},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, _ := repo(t, "sha1")
			if err := os.WriteFile(filepath.Join(root, "code.go"), []byte(test.content), 0600); err != nil {
				t.Fatal(err)
			}
			git(t, root, "", "add", ".")
			git(t, root, "", "commit", "-m", "target")
			target := git(t, root, "", "rev-parse", "HEAD")
			noteID := blob(t, root, note(target, test.end))
			attach(t, root, "refs/notes/ai", tree(t, root, "100644 blob "+noteID+"\t"+target+"\x00"))
			_, err := Acquire(context.Background(), root, Request{System: "git-ai", Commit: target})
			if !errors.Is(err, test.want) {
				t.Fatalf("line count: got %v want %v", err, test.want)
			}
		})
	}
}

func TestEntireDoesNotReturnPartialSessionPlan(t *testing.T) {
	root, target := repo(t, "sha1")
	id := "abcdef012345"
	entireFixture(t, root, target, id, "refs", func(s map[string]any) {
		s["sessions"] = []any{map[string]any{"metadata": "/0/metadata.json", "compact_transcript": "/0/transcript.jsonl"}, map[string]any{"metadata": "/1/metadata.json", "compact_transcript": "/1/transcript.jsonl"}}
	})
	plan, err := Acquire(context.Background(), root, Request{System: "entire", Commit: target, Checkpoint: id})
	if !errors.Is(err, domain.ErrInvalid) || len(plan) != 0 {
		t.Fatalf("partial selected checkpoint escaped: %d %v", len(plan), err)
	}
}
