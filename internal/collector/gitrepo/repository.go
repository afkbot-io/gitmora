// Package gitrepo adapts local Git queries and reversible hook installation to the collector.
package gitrepo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"strings"

	capture "workgraph.local/platform/internal/capture/domain"
	"workgraph.local/platform/internal/collector/domain"
	"workgraph.local/platform/internal/gitlocal"
)

// Repository identifies one worktree and its shared Git common directory.
type Repository struct {
	Root      string
	GitDir    string
	CommonDir string
	isolated  bool
}

// Discover resolves paths through Git rather than assuming that .git is a directory.
func Discover(ctx context.Context, directory string) (*Repository, error) {
	return discover(ctx, directory, nil)
}

// DiscoverLocal resolves the physical worktree without inherited Git directory/config overrides.
// Import commands use this boundary so their ledger and isolated source reader select the same repository.
func DiscoverLocal(ctx context.Context, directory string) (*Repository, error) {
	return discover(ctx, directory, gitlocal.Environment())
}

func discover(ctx context.Context, directory string, environment []string) (*Repository, error) {
	query := func(flag string) (string, error) {
		output, err := runEnvironment(ctx, directory, nil, environment, "rev-parse", "--path-format=absolute", flag)
		if err != nil {
			return "", err
		}
		value := strings.TrimSuffix(string(output), "\n")
		if strings.ContainsAny(value, "\r\n") || value == "" {
			return "", domain.ErrInvalid
		}
		return filepath.EvalSymlinks(value)
	}
	root, err := query("--show-toplevel")
	if err != nil {
		return nil, err
	}
	gitDir, err := query("--git-dir")
	if err != nil {
		return nil, err
	}
	common, err := query("--git-common-dir")
	if err != nil {
		return nil, err
	}
	return &Repository{Root: root, GitDir: gitDir, CommonDir: common, isolated: environment != nil}, nil
}

// WorktreeKey is stable across working-directory aliases and distinct for linked worktrees.
func (r *Repository) WorktreeKey() string {
	sum := sha256.Sum256([]byte(r.GitDir))
	return hex.EncodeToString(sum[:])
}

// LedgerPath locates durable repository-scoped delivery state.
func (r *Repository) LedgerPath() string {
	return filepath.Join(r.CommonDir, "workgraph", "state.sqlite")
}

// RemoteURL returns the actual push URL without printing credential-bearing Git diagnostics.
func (r *Repository) RemoteURL(ctx context.Context, name string) (string, error) {
	if name == "" || strings.HasPrefix(name, "-") || strings.ContainsAny(name, "\x00\r\n") {
		return "", domain.ErrInvalid
	}
	output, err := r.run(ctx, nil, "remote", "get-url", "--push", name)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

// HeadID reports an immutable HEAD or an empty string for an unborn worktree.
func (r *Repository) HeadID(ctx context.Context) (string, error) {
	command := r.command(ctx, "rev-parse", "--verify", "--quiet", "HEAD^{commit}")
	output, err := command.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 && ctx.Err() == nil {
			return "", nil
		}
		return "", errors.New("Git HEAD query failed")
	}
	sha := strings.TrimSpace(string(output))
	if !capture.ValidCommit(sha) {
		return "", domain.ErrInvalid
	}
	return sha, nil
}

// VerifyCommit requires an actual full commit object without traversing its entire history.
func (r *Repository) VerifyCommit(ctx context.Context, sha string) error {
	if !capture.ValidCommit(sha) {
		return domain.ErrInvalid
	}
	_, err := r.run(ctx, nil, "cat-file", "-e", sha+"^{commit}")
	return err
}

// HeadWindow resolves only the supplied interval; it never substitutes the current HEAD.
// Intermediate commits are retained; rewritten history yields its new tip as context, not authorship.
func (r *Repository) HeadWindow(ctx context.Context, before, after string) ([]string, error) {
	if (before != "" && !capture.ValidCommit(before)) || (after != "" && !capture.ValidCommit(after)) {
		return nil, domain.ErrInvalid
	}
	if before == after || after == "" {
		return []string{}, nil
	}
	if _, err := r.run(ctx, nil, "cat-file", "-e", after+"^{commit}"); err != nil {
		return nil, err
	}
	revisions := []string{after}
	if before != "" {
		if _, err := r.run(ctx, nil, "cat-file", "-e", before+"^{commit}"); err != nil {
			return nil, err
		}
		command := r.command(ctx, "merge-base", "--is-ancestor", before, after)
		if err := command.Run(); err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) && exit.ExitCode() == 1 && ctx.Err() == nil {
				return []string{after}, nil
			}
			return nil, errors.New("Git ancestry query failed")
		}
		revisions = append(revisions, "^"+before)
	}
	output, err := r.run(ctx, strings.NewReader(strings.Join(revisions, "\n")+"\n"), "rev-list", "--reverse", "--max-count=2001", "--stdin")
	if err != nil {
		return nil, err
	}
	commits := strings.Fields(string(output))
	if len(commits) > 2000 {
		return nil, domain.ErrInvalid
	}
	for _, sha := range commits {
		if !capture.ValidCommit(sha) {
			return nil, domain.ErrInvalid
		}
	}
	return commits, nil
}

// CommitInfo separates actual commit paths from still-dirty paths after a partial commit.
type CommitInfo struct {
	SHA     string
	Changed []string
	Dirty   []string
}

// Head observes the current immutable commit and remaining worktree/index changes.
func (r *Repository) Head(ctx context.Context) (CommitInfo, error) {
	output, err := r.run(ctx, nil, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return CommitInfo{}, err
	}
	sha := strings.TrimSpace(string(output))
	if !capture.ValidCommit(sha) {
		return CommitInfo{}, domain.ErrInvalid
	}
	changed, err := r.run(ctx, nil, "diff-tree", "--root", "--no-commit-id", "--no-renames", "--name-only", "-z", "-r", "--first-parent", sha)
	if err != nil {
		return CommitInfo{}, err
	}
	dirty, err := r.run(ctx, nil, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return CommitInfo{}, err
	}
	remaining := []string{}
	parts := bytes.Split(dirty, []byte{0})
	for i := 0; i < len(parts); i++ {
		part := parts[i]
		if len(part) == 0 {
			continue
		}
		if len(part) < 4 {
			return CommitInfo{}, domain.ErrInvalid
		}
		remaining = append(remaining, string(part[3:]))
		if bytes.ContainsAny(part[:2], "RC") {
			i++
			if i >= len(parts) || len(parts[i]) == 0 {
				return CommitInfo{}, domain.ErrInvalid
			}
			remaining = append(remaining, string(parts[i]))
		}
	}
	paths := []string{}
	for _, path := range bytes.Split(changed, []byte{0}) {
		if len(path) > 0 {
			paths = append(paths, string(path))
		}
	}
	return CommitInfo{SHA: sha, Changed: paths, Dirty: remaining}, nil
}

// Reachable resolves every pushed commit; missing remote objects fall back to local reachable history.
func (r *Repository) Reachable(ctx context.Context, input io.Reader) ([]string, error) {
	raw, err := io.ReadAll(io.LimitReader(input, 4*1024*1024+1))
	if err != nil || len(raw) > 4*1024*1024 {
		return nil, domain.ErrInvalid
	}
	revisions := []string{}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if strings.TrimSpace(string(raw)) == "" {
		return []string{}, nil
	}
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) != 4 {
			return nil, domain.ErrInvalid
		}
		local, remote := fields[1], fields[3]
		if isZero(local) {
			continue
		}
		if !capture.ValidCommit(local) || (!isZero(remote) && !capture.ValidCommit(remote)) {
			return nil, domain.ErrInvalid
		}
		revisions = append(revisions, local)
		if !isZero(remote) {
			if _, err := r.run(ctx, nil, "cat-file", "-e", remote+"^{commit}"); err == nil {
				revisions = append(revisions, "^"+remote)
			}
		}
	}
	if len(revisions) == 0 {
		return []string{}, nil
	}
	output, err := r.run(ctx, strings.NewReader(strings.Join(revisions, "\n")+"\n"), "rev-list", "--stdin")
	if err != nil {
		return nil, err
	}
	commits := strings.Fields(string(output))
	for _, sha := range commits {
		if !capture.ValidCommit(sha) {
			return nil, domain.ErrInvalid
		}
	}
	return commits, nil
}

func isZero(value string) bool {
	return (len(value) == 40 || len(value) == 64) && strings.Trim(value, "0") == ""
}

// A physical source selection remains isolated for every subsequent query, not only discovery.
func (r *Repository) environment() []string {
	if r.isolated {
		return gitlocal.Environment()
	}
	return nil
}
func (r *Repository) run(ctx context.Context, input io.Reader, args ...string) ([]byte, error) {
	return runEnvironment(ctx, r.Root, input, r.environment(), args...)
}
func (r *Repository) command(ctx context.Context, args ...string) *exec.Cmd {
	return commandEnvironment(ctx, r.Root, r.environment(), args...)
}
func commandEnvironment(ctx context.Context, directory string, environment []string, args ...string) *exec.Cmd {
	prefix := []string{"-c", "core.fsmonitor=false", "-C", directory}
	if environment != nil {
		prefix = append([]string{"--no-replace-objects", "--no-optional-locks"}, prefix...)
	}
	command := exec.CommandContext(ctx, "git", append(prefix, args...)...)
	command.Env = environment
	return command
}
func runEnvironment(ctx context.Context, directory string, input io.Reader, environment []string, args ...string) ([]byte, error) {
	command := commandEnvironment(ctx, directory, environment, args...)
	command.Stdin = input
	output, err := command.Output()
	if err != nil {
		return nil, errors.New("Git query failed")
	}
	return output, nil
}
