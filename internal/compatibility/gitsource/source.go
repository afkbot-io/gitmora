// Package gitsource acquires bounded, hash-checked local Git evidence without fetching or executing a producer.
package gitsource

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"

	capture "workgraph.local/platform/internal/capture/domain"
	"workgraph.local/platform/internal/compatibility/domain"
	"workgraph.local/platform/internal/gitlocal"
	"workgraph.local/platform/internal/jsonwire"
)

// Request selects an existing local source and a full target commit. Empty Entire storage means refs.
type Request struct {
	System, Commit, Checkpoint, Storage, Session string
	// SourceRef/SourceObject optionally replay a previously selected immutable source snapshot.
	SourceRef, SourceObject string
}

type entry struct{ mode, oid string }
type reader struct {
	root      string
	remaining int
	trees     map[string]map[string]entry
}
type commit struct {
	tree, message string
	recorded      time.Time
}

// Acquire finishes all object, membership and target validation before returning any projection.
// Git replacements, inherited GIT_* overrides, lazy network fetch and optional hooks are disabled.
func Acquire(ctx context.Context, root string, request Request) ([]*domain.Projection, error) {
	if !capture.ValidCommit(request.Commit) {
		return nil, domain.ErrInvalid
	}
	if (request.SourceRef == "") != (request.SourceObject == "") || (request.SourceObject != "" && !capture.ValidCommit(request.SourceObject)) {
		return nil, domain.ErrInvalid
	}
	switch request.System {
	case "git-ai":
		if request.Checkpoint != "" || request.Storage != "" || request.Session != "" {
			return nil, domain.ErrInvalid
		}
		if request.SourceRef != "" && request.SourceRef != "refs/notes/ai" {
			return nil, domain.ErrInvalid
		}
	case "entire":
		if !domain.ValidEntireID(request.Checkpoint) || (request.Storage != "" && request.Storage != "refs" && request.Storage != "branch") || len(request.Session) > 1024 {
			return nil, domain.ErrInvalid
		}
		if request.SourceRef != "" && ((request.Storage == "branch" && request.SourceRef != "refs/heads/entire/checkpoints/v1") || (request.Storage != "branch" && !entireRef(request.SourceRef, request.Checkpoint))) {
			return nil, domain.ErrInvalid
		}
	default:
		return nil, domain.ErrInvalid
	}
	bounded, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	r := reader{root: root, remaining: domain.MaxSourceBytes}
	target, err := r.commit(bounded, request.Commit)
	if err != nil {
		return nil, err
	}
	if request.System == "git-ai" {
		return r.gitAI(bounded, request, target)
	}
	return r.entire(bounded, request, target)
}

func isolatedEnvironment() []string {
	return gitlocal.Environment()
}

type boundedOutput struct {
	buffer bytes.Buffer
	limit  int
}

func (w *boundedOutput) Len() int      { return w.buffer.Len() }
func (w *boundedOutput) Bytes() []byte { return w.buffer.Bytes() }

func (w *boundedOutput) Write(p []byte) (int, error) {
	if len(p) > w.limit-w.Len() {
		return 0, domain.ErrLimit
	}
	return w.buffer.Write(p)
}
func (r *reader) run(ctx context.Context, limit int, args ...string) ([]byte, error) {
	child, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	c := exec.CommandContext(child, "git", append([]string{"--no-replace-objects", "--no-optional-locks", "-c", "core.fsmonitor=false", "-C", r.root}, args...)...)
	c.Env = isolatedEnvironment()
	c.Stderr = io.Discard
	output := &boundedOutput{limit: limit}
	c.Stdout = output
	err := c.Run()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if child.Err() != nil {
		return nil, child.Err()
	}
	if err != nil {
		if output.Len() >= limit || errors.Is(err, domain.ErrLimit) {
			return nil, domain.ErrLimit
		}
		return nil, domain.ErrInvalid
	}
	return output.Bytes(), nil
}
func (r *reader) object(ctx context.Context, oid, kind string) ([]byte, error) {
	if !capture.ValidCommit(oid) || (kind != "blob" && kind != "tree" && kind != "commit") {
		return nil, domain.ErrInvalid
	}
	sizeRaw, err := r.run(ctx, 32, "cat-file", "-s", oid)
	if err != nil {
		return nil, err
	}
	size, err := strconv.Atoi(strings.TrimSpace(string(sizeRaw)))
	if err != nil || size < 0 {
		return nil, domain.ErrInvalid
	}
	if size > r.remaining {
		return nil, domain.ErrLimit
	}
	raw, err := r.run(ctx, size+1, "cat-file", kind, oid)
	if err != nil {
		return nil, err
	}
	if len(raw) != size {
		return nil, domain.ErrInvalid
	}
	var digest hash.Hash = sha1.New()
	if len(oid) == 64 {
		digest = sha256.New()
	}
	_, _ = fmt.Fprintf(digest, "%s %d\x00", kind, size)
	_, _ = digest.Write(raw)
	if hex.EncodeToString(digest.Sum(nil)) != oid {
		return nil, domain.ErrInvalid
	}
	r.remaining -= size
	return raw, nil
}
func (r *reader) commit(ctx context.Context, oid string) (commit, error) {
	raw, err := r.object(ctx, oid, "commit")
	if err != nil {
		return commit{}, err
	}
	parts := bytes.SplitN(raw, []byte("\n\n"), 2)
	if len(parts) != 2 {
		return commit{}, domain.ErrInvalid
	}
	result := commit{message: string(parts[1])}
	for _, line := range strings.Split(string(parts[0]), "\n") {
		if strings.HasPrefix(line, "tree ") {
			if result.tree != "" {
				return commit{}, domain.ErrInvalid
			}
			result.tree = strings.TrimPrefix(line, "tree ")
		}
		if strings.HasPrefix(line, "committer ") {
			fields := strings.Fields(line)
			if len(fields) < 3 || !result.recorded.IsZero() {
				return commit{}, domain.ErrInvalid
			}
			unix, e := strconv.ParseInt(fields[len(fields)-2], 10, 64)
			if e != nil {
				return commit{}, domain.ErrInvalid
			}
			result.recorded = time.Unix(unix, 0).UTC()
		}
	}
	if !capture.ValidCommit(result.tree) || len(result.tree) != len(oid) || result.recorded.IsZero() {
		return commit{}, domain.ErrInvalid
	}
	return result, nil
}
func (r *reader) tree(ctx context.Context, oid string) (map[string]entry, error) {
	if value, ok := r.trees[oid]; ok {
		return value, nil
	}
	raw, err := r.object(ctx, oid, "tree")
	if err != nil {
		return nil, err
	}
	entries := map[string]entry{}
	hashBytes := len(oid) / 2
	for len(raw) > 0 {
		space, nul := bytes.IndexByte(raw, ' '), bytes.IndexByte(raw, 0)
		if space <= 0 || nul <= space+1 || len(raw) < nul+1+hashBytes || len(entries) >= 100000 {
			return nil, domain.ErrInvalid
		}
		mode, name := string(raw[:space]), string(raw[space+1:nul])
		if strings.Contains(name, "/") || name == "." || name == ".." {
			return nil, domain.ErrInvalid
		}
		if _, exists := entries[name]; exists {
			return nil, domain.ErrInvalid
		}
		entries[name] = entry{mode: mode, oid: hex.EncodeToString(raw[nul+1 : nul+1+hashBytes])}
		raw = raw[nul+1+hashBytes:]
	}
	if r.trees == nil {
		r.trees = map[string]map[string]entry{}
	}
	r.trees[oid] = entries
	return entries, nil
}
func (r *reader) entry(ctx context.Context, tree, path string) (entry, error) {
	if !capture.ValidFilePath(path) {
		return entry{}, domain.ErrInvalid
	}
	parts := strings.Split(path, "/")
	for index, name := range parts {
		entries, err := r.tree(ctx, tree)
		if err != nil {
			return entry{}, err
		}
		value, ok := entries[name]
		if !ok {
			return entry{}, domain.ErrInvalid
		}
		if index == len(parts)-1 {
			return value, nil
		}
		if value.mode != "40000" {
			return entry{}, domain.ErrInvalid
		}
		tree = value.oid
	}
	return entry{}, domain.ErrInvalid
}
func regular(e entry) bool { return e.mode == "100644" || e.mode == "100755" }
func (r *reader) file(ctx context.Context, tree, path string) ([]byte, error) {
	e, err := r.entry(ctx, tree, path)
	if err != nil {
		return nil, err
	}
	if !regular(e) {
		return nil, domain.ErrInvalid
	}
	return r.object(ctx, e.oid, "blob")
}
func (r *reader) pin(ctx context.Context, ref string) (string, error) {
	raw, err := r.run(ctx, 256, "show-ref", "--verify", "--hash", ref)
	if err != nil {
		return "", err
	}
	oid := strings.TrimSpace(string(raw))
	if !capture.ValidCommit(oid) {
		return "", domain.ErrInvalid
	}
	return oid, nil
}

func (r *reader) gitAI(ctx context.Context, request Request, target commit) ([]*domain.Projection, error) {
	ref := "refs/notes/ai"
	pinned, err := request.SourceObject, error(nil)
	if pinned == "" {
		pinned, err = r.pin(ctx, ref)
	}
	if err != nil {
		return nil, err
	}
	source, err := r.commit(ctx, pinned)
	if err != nil {
		return nil, err
	}
	current := source.tree
	var found []entry
	for offset := 0; offset < len(request.Commit); offset += 2 {
		entries, err := r.tree(ctx, current)
		if err != nil {
			return nil, err
		}
		if value, ok := entries[request.Commit[offset:]]; ok {
			if !regular(value) {
				return nil, domain.ErrInvalid
			}
			found = append(found, value)
		}
		if offset+2 >= len(request.Commit) {
			break
		}
		next, ok := entries[request.Commit[offset:offset+2]]
		if !ok {
			break
		}
		if next.mode != "40000" {
			return nil, domain.ErrInvalid
		}
		current = next.oid
	}
	if len(found) != 1 {
		return nil, domain.ErrInvalid
	}
	raw, err := r.object(ctx, found[0].oid, "blob")
	if err != nil {
		return nil, err
	}
	p, err := domain.ParseGitAI(domain.Scope{ObjectID: found[0].oid, Ref: ref, RefObject: pinned, TargetCommit: request.Commit, Time: target.recorded}, raw)
	if err != nil {
		return nil, err
	}
	windows, err := p.Windows()
	if err != nil {
		return nil, err
	}
	ends := map[string]int{}
	for _, w := range windows {
		for _, claim := range w.Origin.Claims {
			ends[claim.Path] = max(ends[claim.Path], claim.End)
		}
	}
	for path, end := range ends {
		raw, err := r.file(ctx, target.tree, path)
		if err != nil {
			return nil, err
		}
		if bytes.IndexByte(raw, 0) >= 0 {
			return nil, domain.ErrUnsupported
		}
		lines := bytes.Count(raw, []byte{'\n'})
		if len(raw) > 0 && raw[len(raw)-1] != '\n' {
			lines++
		}
		if end > lines {
			return nil, domain.ErrInvalid
		}
	}
	return []*domain.Projection{p}, nil
}

type summary struct {
	Checkpoint string `json:"checkpoint_id"`
	Commit     string `json:"commit_sha"`
	Sessions   []struct {
		Metadata string `json:"metadata"`
		Compact  string `json:"compact_transcript"`
	} `json:"sessions"`
	Links []struct {
		SHA string `json:"sha"`
	} `json:"linked_commits"`
}

func (r *reader) entire(ctx context.Context, request Request, target commit) ([]*domain.Projection, error) {
	ref, prefix := "refs/heads/entire/checkpoints/v1", request.Checkpoint[:2]+"/"+request.Checkpoint[2:]+"/"
	var pinned string
	var err error
	if request.SourceObject != "" {
		ref, pinned = request.SourceRef, request.SourceObject
		if request.Storage != "branch" {
			prefix = ""
		}
	} else if request.Storage != "branch" {
		prefix = ""
		raw, e := r.run(ctx, 2048, "for-each-ref", "--count=3", "--format=%(refname) %(objectname)", "refs/entire/checkpoints/*/"+request.Checkpoint)
		if e != nil {
			return nil, e
		}
		lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
		if len(lines) != 1 {
			return nil, domain.ErrInvalid
		}
		fields := strings.Fields(lines[0])
		if len(fields) != 2 || !capture.ValidCommit(fields[1]) {
			return nil, domain.ErrInvalid
		}
		if !entireRef(fields[0], request.Checkpoint) {
			return nil, domain.ErrInvalid
		}
		ref, pinned = fields[0], fields[1]
	} else {
		pinned, err = r.pin(ctx, ref)
		if err != nil {
			return nil, err
		}
	}
	source, err := r.commit(ctx, pinned)
	if err != nil {
		return nil, err
	}
	raw, err := r.file(ctx, source.tree, prefix+"metadata.json")
	if err != nil {
		return nil, err
	}
	var meta summary
	if jsonwire.DecodeExternalObject(raw, &meta) != nil || meta.Checkpoint != request.Checkpoint || len(meta.Sessions) == 0 || len(meta.Sessions) > 64 || len(meta.Links) > 10000 {
		return nil, domain.ErrInvalid
	}
	linked := meta.Commit == request.Commit || checkpointTrailer(target.message, request.Checkpoint)
	for _, link := range meta.Links {
		if !capture.ValidCommit(link.SHA) {
			return nil, domain.ErrInvalid
		}
		linked = linked || link.SHA == request.Commit
	}
	if meta.Commit != "" && !capture.ValidCommit(meta.Commit) {
		return nil, domain.ErrInvalid
	}
	result := []*domain.Projection{}
	windowCount, itemCount := 0, 0
	seenPath, seenSession := map[string]bool{}, map[string]bool{}
	for _, session := range meta.Sessions {
		// Only the compact v1 path in the exact indexed session folder is supported.
		if !strings.HasPrefix(session.Metadata, "/"+prefix) || !strings.HasSuffix(session.Metadata, "/metadata.json") {
			return nil, domain.ErrInvalid
		}
		folder := strings.TrimSuffix(strings.TrimPrefix(session.Metadata, "/"+prefix), "/metadata.json")
		if folder == "" || strings.Contains(folder, "/") || !capture.ValidFilePath(folder) || seenPath[folder] {
			return nil, domain.ErrInvalid
		}
		seenPath[folder] = true
		if session.Compact != "/"+prefix+folder+"/transcript.jsonl" {
			return nil, domain.ErrUnsupported
		}
		sessionEntry, err := r.entry(ctx, source.tree, prefix+folder)
		if err != nil {
			return nil, err
		}
		if sessionEntry.mode != "40000" {
			return nil, domain.ErrInvalid
		}
		metadata, err := r.file(ctx, sessionEntry.oid, "metadata.json")
		if err != nil {
			return nil, err
		}
		var identity struct {
			Session string `json:"session_id"`
			Commit  string `json:"commit_sha"`
		}
		if jsonwire.DecodeExternalObject(metadata, &identity) != nil || identity.Session == "" || seenSession[identity.Session] {
			return nil, domain.ErrInvalid
		}
		if identity.Commit != "" && !capture.ValidCommit(identity.Commit) {
			return nil, domain.ErrInvalid
		}
		seenSession[identity.Session] = true
		if request.Session != "" && request.Session != identity.Session {
			continue
		}
		if !linked && identity.Commit != request.Commit {
			return nil, domain.ErrInvalid
		}
		transcript, err := r.file(ctx, sessionEntry.oid, "transcript.jsonl")
		if err != nil {
			return nil, err
		}
		p, err := domain.ParseEntire(domain.Scope{ObjectID: sessionEntry.oid, Ref: ref, RefObject: pinned, TargetCommit: request.Commit}, request.Checkpoint, metadata, transcript)
		if err != nil {
			return nil, err
		}
		windows, err := p.Windows()
		if err != nil {
			return nil, err
		}
		windowCount += len(windows)
		for _, w := range windows {
			itemCount += len(w.Messages)
		}
		if windowCount > 256 || itemCount > domain.MaxItems {
			return nil, domain.ErrLimit
		}
		result = append(result, p)
	}
	if len(result) == 0 {
		return nil, domain.ErrInvalid
	}
	return result, nil
}

func entireRef(ref, id string) bool {
	parts := strings.Split(ref, "/")
	return domain.ValidEntireID(id) && len(parts) == 5 && parts[0] == "refs" && parts[1] == "entire" && parts[2] == "checkpoints" && parts[4] == id && strings.EqualFold(parts[3], id[len(id)-2:])
}

func checkpointTrailer(message, id string) bool {
	paragraphs := strings.Split(strings.TrimSpace(message), "\n\n")
	last := paragraphs[len(paragraphs)-1]
	found := false
	for _, line := range strings.Split(last, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok || key == "" || strings.ContainsAny(key, " \t\r") {
			return false
		}
		if strings.EqualFold(key, "Entire-Checkpoint") && strings.TrimSpace(value) == id {
			found = true
		}
	}
	return found
}
