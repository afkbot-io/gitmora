//go:build unix

package local_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"workgraph.local/platform/internal/distribution/domain"
	"workgraph.local/platform/internal/distribution/local"
)

func artifact(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "download")
	if err := os.WriteFile(p, []byte(content), 0700); err != nil {
		t.Fatal(err)
	}
	return p
}
func hash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func TestInstallUpdatePreservesStableHookPathAndForeignFiles(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(dir, "unrelated"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	a, b := artifact(t, "first-client"), artifact(t, "second-client")
	first, err := local.Install(ctx, dir, a, "")
	if err != nil || first.SHA256 != hash("first-client") || !first.Changed {
		t.Fatal(first, err)
	}
	canonical, canonicalErr := filepath.EvalSymlinks(dir)
	if canonicalErr != nil || first.Path != filepath.Join(canonical, "gitmora") {
		t.Fatal(first)
	}
	if _, err = local.Install(ctx, dir, b, ""); !errors.Is(err, domain.ErrConflict) {
		t.Fatal(err)
	}
	next, err := local.Install(ctx, dir, b, first.SHA256)
	if err != nil || !next.Changed || next.PreviousSHA256 != first.SHA256 || next.Path != first.Path {
		t.Fatal(next, err)
	}
	replay, err := local.Install(ctx, dir, b, first.SHA256)
	if err != nil || replay.Changed {
		t.Fatal(replay, err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "unrelated")); string(data) != "keep" {
		t.Fatal("foreign file changed")
	}
	if info, err := os.Stat(first.Path); err != nil || info.Mode().Perm() != 0700 {
		t.Fatal("private executable mode", info, err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 3 {
		t.Fatal("left staging files", entries)
	}
}
func TestUnsafeDestinationsAndSourceNeverBlockOrMutate(t *testing.T) {
	for _, kind := range []string{"symlink", "symlink-inside", "hardlink", "fifo", "directory", "writable-parent", "lock-symlink", "lock-symlink-inside", "source-fifo"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			source := artifact(t, "new")
			target := filepath.Join(dir, "gitmora")
			foreign := artifact(t, "foreign")
			var err error
			switch kind {
			case "symlink":
				err = os.Symlink(foreign, target)
			case "symlink-inside":
				err = os.WriteFile(filepath.Join(dir, "foreign"), []byte("foreign"), 0700)
				if err == nil {
					err = os.Symlink("foreign", target)
				}
			case "hardlink":
				err = os.Link(foreign, target)
			case "fifo":
				err = syscall.Mkfifo(target, 0600)
			case "directory":
				err = os.Mkdir(target, 0700)
			case "writable-parent":
				err = os.Chmod(dir, 0777)
			case "lock-symlink":
				err = os.Symlink(foreign, filepath.Join(dir, ".gitmora-install.lock"))
			case "lock-symlink-inside":
				err = os.WriteFile(filepath.Join(dir, "empty"), nil, 0600)
				if err == nil {
					err = os.Symlink("empty", filepath.Join(dir, ".gitmora-install.lock"))
				}
			case "source-fifo":
				source = filepath.Join(t.TempDir(), "source")
				err = syscall.Mkfifo(source, 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			start := time.Now()
			if _, err := local.Install(ctx, dir, source, ""); err == nil {
				t.Fatal("unsafe file accepted")
			}
			if time.Since(start) > time.Second {
				t.Fatal("special file blocked")
			}
			if data, _ := os.ReadFile(foreign); string(data) != "foreign" {
				t.Fatal("followed unsafe path")
			}
		})
	}
}
func TestConcurrentDifferentInstallsAndCancelledRequest(t *testing.T) {
	dir := t.TempDir()
	a, b := artifact(t, "alpha"), artifact(t, "beta")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := local.Install(ctx, dir, a, ""); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "gitmora")); !os.IsNotExist(err) {
		t.Fatal("cancelled install changed target")
	}
	if err := os.WriteFile(filepath.Join(dir, "gitmora"), []byte("preserved"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := local.Install(ctx, dir, a, hash("preserved")); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(filepath.Join(dir, "gitmora")); string(raw) != "preserved" {
		t.Fatal("cancelled update changed old bytes")
	}
	if err := os.Remove(filepath.Join(dir, "gitmora")); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	outcomes := make(chan error, 2)
	for _, source := range []string{a, b} {
		wg.Go(func() { _, err := local.Install(context.Background(), dir, source, ""); outcomes <- err })
	}
	wg.Wait()
	close(outcomes)
	succeeded := 0
	for err := range outcomes {
		if err == nil {
			succeeded++
		} else if !errors.Is(err, domain.ErrBusy) && !errors.Is(err, domain.ErrConflict) {
			t.Fatal(err)
		}
	}
	if succeeded != 1 {
		t.Fatal("conflicting installers", succeeded)
	}
}

func TestBoundedSourceAndDestinationInputs(t *testing.T) {
	dir := t.TempDir()
	source := artifact(t, "incoming")
	for _, tc := range []struct{ directory, source, expected string }{
		{"relative", source, ""}, {dir, "relative", ""}, {dir, source, "BAD"},
	} {
		if _, err := local.Install(context.Background(), tc.directory, tc.source, tc.expected); !errors.Is(err, domain.ErrInvalid) {
			t.Fatal(err)
		}
	}
	large, err := os.OpenFile(filepath.Join(t.TempDir(), "large"), os.O_CREATE|os.O_WRONLY, 0700)
	if err != nil {
		t.Fatal(err)
	}
	if err = large.Truncate(129 << 20); err != nil {
		t.Fatal(err)
	}
	if err = large.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := local.Install(context.Background(), dir, large.Name(), ""); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "gitmora")); !os.IsNotExist(err) {
		t.Fatal("invalid source activated")
	}
}
