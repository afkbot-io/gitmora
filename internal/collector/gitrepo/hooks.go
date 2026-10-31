package gitrepo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/gofrs/flock"
	"workgraph.local/platform/internal/collector/domain"
	"workgraph.local/platform/internal/jsonwire"
)

// Installation records only Workgraph-owned wrappers and the original scoped Git setting.
type Installation struct {
	Version         int               `json:"version"`
	ManagedDir      string            `json:"managed_dir"`
	ManifestPath    string            `json:"manifest_path"`
	Executable      string            `json:"executable"`
	Scope           string            `json:"scope"`
	OriginalSetting *string           `json:"original_setting"`
	OriginalDir     string            `json:"original_dir"`
	Hashes          map[string]string `json:"hashes"`
}

var hookNames = []string{"applypatch-msg", "pre-applypatch", "post-applypatch", "pre-commit", "pre-merge-commit", "prepare-commit-msg", "commit-msg", "post-commit", "pre-rebase", "post-checkout", "post-merge", "pre-push", "pre-receive", "update", "proc-receive", "post-receive", "post-update", "reference-transaction", "push-to-checkout", "pre-auto-gc", "post-rewrite", "sendemail-validate", "fsmonitor-watchman", "p4-changelist", "p4-prepare-changelist", "p4-post-changelist", "p4-pre-submit", "post-index-change"}

// InstallHooks creates owned dispatchers without changing existing hook files, then selects them.
func (r *Repository) InstallHooks(ctx context.Context, executable string) (Installation, error) {
	var result Installation
	err := r.hookLock(ctx, func() error {
		absolute, err := filepath.Abs(executable)
		if err != nil {
			return err
		}
		absolute, err = filepath.EvalSymlinks(absolute)
		if err != nil {
			return err
		}
		if info, err := os.Stat(absolute); err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
			return domain.ErrInvalid
		}
		if existing, found, err := r.existingInstallation(ctx); err != nil {
			return err
		} else if found {
			if existing.Executable != absolute {
				return domain.ErrConflict
			}
			if err := r.verifyInstallation(ctx, existing); err != nil {
				return err
			}
			result = existing
			return nil
		}
		scope := "--local"
		if extension, _ := r.configuration(ctx, "", "extensions.worktreeConfig"); extension != nil && *extension == "true" {
			scope = "--worktree"
		}
		original, err := r.configuration(ctx, scope, "core.hooksPath")
		if err != nil {
			return err
		}
		effective, err := r.configuration(ctx, "", "core.hooksPath")
		if err != nil {
			return err
		}
		originalDir := filepath.Join(r.CommonDir, "hooks")
		if effective != nil {
			originalDir = *effective
		}
		managed := filepath.Join(r.GitDir, "workgraph-hooks")
		manifest := filepath.Join(r.GitDir, "workgraph-hook-install.json")
		if strings.ContainsAny(originalDir, "\x00\r\n") || filepath.Clean(originalDir) == managed {
			return domain.ErrInvalid
		}
		if err := os.Mkdir(managed, 0700); err != nil {
			return domain.ErrConflict
		}
		installation := Installation{Version: 1, ManagedDir: managed, ManifestPath: manifest, Executable: absolute, Scope: scope, OriginalSetting: original, OriginalDir: originalDir, Hashes: map[string]string{}}
		names := slices.Clone(hookNames)
		resolved := originalDir
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(r.Root, resolved)
		}
		if entries, err := os.ReadDir(resolved); err == nil {
			for _, entry := range entries {
				if !entry.IsDir() && entry.Name() == strings.ToLower(entry.Name()) && !strings.ContainsAny(entry.Name(), "\x00\r\n") {
					names = append(names, entry.Name())
				}
			}
		} else if !os.IsNotExist(err) && !errors.Is(err, syscall.ENOTDIR) {
			return err
		}
		slices.Sort(names)
		names = slices.Compact(names)
		for _, name := range names {
			originalHook := filepath.Join(originalDir, name)
			wrapper := "#!/bin/sh\n# workgraph-managed-v1\nif test -x " + quote(originalHook) + "; then exec " + quote(originalHook) + " \"$@\"; fi\nexit 0\n"
			if name == "post-commit" || name == "pre-push" || name == "post-rewrite" {
				wrapper = "#!/bin/sh\n# workgraph-managed-v1\nexec " + quote(absolute) + " internal-hook --installation " + quote(manifest) + " --name " + quote(name) + " -- \"$@\"\n"
			}
			if err := writeOwned(filepath.Join(managed, name), []byte(wrapper), 0700); err != nil {
				return err
			}
			sum := sha256.Sum256([]byte(wrapper))
			installation.Hashes[name] = hex.EncodeToString(sum[:])
		}
		raw, err := json.Marshal(installation)
		if err != nil {
			return err
		}
		if err := writeOwned(manifest, raw, 0600); err != nil {
			return err
		}
		if err := syncDirectory(managed); err != nil {
			return err
		}
		if err := syncDirectory(r.GitDir); err != nil {
			return err
		}
		if _, err := r.run(ctx, nil, "config", scope, "core.hooksPath", managed); err != nil {
			return err
		}
		result = installation
		return nil
	})
	return result, err
}

// UninstallHooks restores the original setting only when user configuration/files are unchanged.
func (r *Repository) UninstallHooks(ctx context.Context) error {
	return r.hookLock(ctx, func() error {
		installation, found, err := r.existingInstallation(ctx)
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
		if err := r.verifyInstallation(ctx, installation); err != nil {
			return err
		}
		if installation.OriginalSetting == nil {
			if _, err := r.run(ctx, nil, "config", installation.Scope, "--unset", "core.hooksPath"); err != nil {
				return err
			}
		} else {
			if _, err := r.run(ctx, nil, "config", installation.Scope, "core.hooksPath", *installation.OriginalSetting); err != nil {
				return err
			}
		}
		for name := range installation.Hashes {
			if err := os.Remove(filepath.Join(installation.ManagedDir, name)); err != nil {
				return err
			}
		}
		if err := os.Remove(installation.ManagedDir); err != nil {
			return err
		}
		return os.Remove(installation.ManifestPath)
	})
}

func (r *Repository) existingInstallation(ctx context.Context) (Installation, bool, error) {
	path := filepath.Join(r.GitDir, "workgraph-hook-install.json")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		effective, err := r.configuration(ctx, "", "core.hooksPath")
		if err != nil {
			return Installation{}, false, err
		}
		if effective == nil || filepath.Base(*effective) != "workgraph-hooks" {
			return Installation{}, false, nil
		}
		path = filepath.Join(filepath.Dir(*effective), "workgraph-hook-install.json")
	}
	installation, err := ReadInstallation(path)
	if err != nil {
		return Installation{}, false, err
	}
	return installation, true, nil
}

func (r *Repository) verifyInstallation(ctx context.Context, installation Installation) error {
	setting, err := r.configuration(ctx, "", "core.hooksPath")
	if err != nil {
		return err
	}
	if setting == nil || *setting != installation.ManagedDir {
		return domain.ErrConflict
	}
	entries, err := os.ReadDir(installation.ManagedDir)
	if err != nil || len(entries) != len(installation.Hashes) {
		return domain.ErrConflict
	}
	for name, expected := range installation.Hashes {
		path := filepath.Join(installation.ManagedDir, name)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
			return domain.ErrConflict
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != expected {
			return domain.ErrConflict
		}
	}
	return nil
}

// ReadInstallation validates private hook metadata before dispatch or restoration.
func ReadInstallation(path string) (Installation, error) {
	file, err := os.Open(path)
	if err != nil {
		return Installation{}, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, 128*1024+1))
	if err != nil || len(raw) > 128*1024 {
		return Installation{}, domain.ErrInvalid
	}
	var installation Installation
	// Manifest hash keys contain Git's hyphenated lowercase hook filenames, also valid in this object contract.
	if jsonwire.DecodeObject(raw, &installation) != nil || installation.Version != 1 || !filepath.IsAbs(installation.ManagedDir) || installation.ManifestPath != path || (installation.Scope != "--local" && installation.Scope != "--worktree") || len(installation.Hashes) == 0 {
		return Installation{}, domain.ErrInvalid
	}
	for name := range installation.Hashes {
		if filepath.Base(name) != name || name == "." || name == ".." {
			return Installation{}, domain.ErrInvalid
		}
	}
	return installation, nil
}

// HookExit preserves the exit code of an existing hook without retaining unsafe process diagnostics.
type HookExit struct{ Code int }

func (e *HookExit) Error() string { return "existing Git hook rejected the operation" }

// RunOriginal forwards the exact arguments, streams and working directory of an existing hook.
func RunOriginal(ctx context.Context, installation Installation, directory, name string, args []string, input io.Reader, output, diagnostic io.Writer) error {
	if filepath.Base(name) != name {
		return domain.ErrInvalid
	}
	path := filepath.Join(installation.OriginalDir, name)
	if !filepath.IsAbs(path) {
		path = filepath.Join(directory, path)
	}
	info, err := os.Stat(path)
	if os.IsNotExist(err) || errors.Is(err, syscall.ENOTDIR) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode().Perm()&0111 == 0 {
		return nil
	}
	command := exec.CommandContext(ctx, path, args...)
	command.Dir = directory
	command.Stdin = input
	command.Stdout = output
	command.Stderr = diagnostic
	if err := command.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return &HookExit{Code: exit.ExitCode()}
		}
		return &HookExit{Code: 127}
	}
	return nil
}

func (r *Repository) configuration(ctx context.Context, scope, key string) (*string, error) {
	args := []string{"config"}
	if scope != "" {
		args = append(args, scope)
	}
	if key == "extensions.worktreeConfig" {
		args = append(args, "--bool")
	}
	args = append(args, "--get", key)
	output, err := r.command(ctx, args...).Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return nil, nil
		}
		return nil, errors.New("cannot read Git hook configuration")
	}
	value := strings.TrimSuffix(string(output), "\n")
	return &value, nil
}

func (r *Repository) hookLock(ctx context.Context, fn func() error) error {
	directory := filepath.Join(r.CommonDir, "workgraph")
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	lock := flock.New(filepath.Join(directory, "hook-install.lock"))
	defer lock.Close()
	locked, err := lock.TryLockContext(ctx, 10*time.Millisecond)
	if err != nil {
		return err
	}
	if !locked {
		return errors.New("hook installation is busy")
	}
	defer lock.Unlock()
	return fn()
}

func writeOwned(path string, raw []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := file.Write(raw); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}
func quote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
