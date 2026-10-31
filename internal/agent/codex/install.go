package codex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"
	"workgraph.local/platform/internal/collector/domain"
	"workgraph.local/platform/internal/jsonwire"
)

var hookEvents = []string{"SessionStart", "PreToolUse", "PostToolUse", "Stop"}

const manifestName = ".gitmora-hooks.json"

type hookInstallation struct {
	Version int                        `json:"version"`
	Groups  map[string]json.RawMessage `json:"groups"`
}

func DefaultDirectory() (string, error) {
	if directory := os.Getenv("CODEX_HOME"); directory != "" {
		return filepath.Abs(directory)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex"), nil
}

func quote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
func canonical(raw []byte) ([]byte, error) {
	var value map[string]any
	if jsonwire.DecodeExternalObject(raw, &value) != nil {
		return nil, domain.ErrConflict
	}
	return json.Marshal(value)
}
func privateRead(file string) ([]byte, bool, error) {
	info, err := os.Lstat(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() || info.Size() > 1024*1024 {
		return nil, false, domain.ErrConflict
	}
	raw, err := os.ReadFile(file)
	return raw, true, err
}
func atomicWrite(file string, raw []byte) error {
	f, err := os.CreateTemp(filepath.Dir(file), ".gitmora-hooks-stage-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(raw); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), file); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(file))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
func installLocked(ctx context.Context, directory string, action func() error) error {
	if !filepath.IsAbs(directory) {
		return domain.ErrInvalid
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	lock := flock.New(filepath.Join(directory, ".gitmora-hooks.lock"))
	ok, err := lock.TryLockContext(ctx, 25*time.Millisecond)
	if err != nil {
		return err
	}
	if !ok {
		return domain.ErrConflict
	}
	defer lock.Unlock()
	return action()
}

func loadHooks(directory string) (map[string]json.RawMessage, map[string][]json.RawMessage, []byte, error) {
	raw, exists, err := privateRead(filepath.Join(directory, "hooks.json"))
	if err != nil {
		return nil, nil, nil, err
	}
	config := map[string]json.RawMessage{}
	hooks := map[string][]json.RawMessage{}
	if exists && jsonwire.DecodeExternalObject(raw, &config) != nil {
		return nil, nil, nil, domain.ErrConflict
	}
	if value, present := config["hooks"]; present && jsonwire.DecodeExternalObject(value, &hooks) != nil {
		return nil, nil, nil, domain.ErrConflict
	}
	return config, hooks, raw, nil
}
func saveHooks(directory string, config map[string]json.RawMessage, hooks map[string][]json.RawMessage, before []byte) error {
	current, _, err := privateRead(filepath.Join(directory, "hooks.json"))
	if err != nil {
		return err
	}
	if !bytes.Equal(current, before) {
		return domain.ErrConflict
	}
	config["hooks"], err = json.Marshal(hooks)
	if err != nil {
		return err
	}
	raw, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(directory, "hooks.json"), append(raw, '\n'))
}

// Enable merges owned hook groups; Codex's separate /hooks trust review is always required.
func Enable(ctx context.Context, directory, executable, codexExecutable string) error {
	for _, value := range []string{executable, codexExecutable} {
		if !filepath.IsAbs(value) || strings.ContainsAny(value, "\x00\r\n") {
			return domain.ErrInvalid
		}
		info, err := os.Stat(value)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
			return domain.ErrInvalid
		}
	}
	expected := hookInstallation{Version: 1, Groups: map[string]json.RawMessage{}}
	for _, event := range hookEvents {
		timeout := 5
		if event == "Stop" || event == "SessionStart" {
			timeout = 20
		}
		raw, _ := json.Marshal(map[string]any{"hooks": []any{map[string]any{
			"type": "command", "command": quote(executable) + " internal-codex-hook --codex " + quote(codexExecutable), "timeout": timeout,
		}}})
		expected.Groups[event] = raw
	}
	return installLocked(ctx, directory, func() error {
		config, hooks, before, err := loadHooks(directory)
		if err != nil {
			return err
		}
		manifest, exists, err := privateRead(filepath.Join(directory, manifestName))
		if err != nil {
			return err
		}
		wanted, _ := json.Marshal(expected)
		wanted, _ = canonical(wanted)
		if exists {
			actual, e := canonical(manifest)
			if e != nil || !bytes.Equal(actual, wanted) {
				return domain.ErrConflict
			}
		} else {
			for _, groups := range hooks {
				for _, group := range groups {
					if bytes.Contains(group, []byte("internal-codex-hook")) {
						return domain.ErrConflict
					}
				}
			}
			// Record ownership intent first; a retry can finish an interrupted installation.
			if err = atomicWrite(filepath.Join(directory, manifestName), wanted); err != nil {
				return err
			}
		}
		for _, event := range hookEvents {
			matches := 0
			for _, group := range hooks[event] {
				value, e := canonical(group)
				if e != nil {
					return e
				}
				if bytes.Equal(value, expected.Groups[event]) {
					matches++
				} else if bytes.Contains(value, []byte("internal-codex-hook")) {
					return domain.ErrConflict
				}
			}
			if matches > 1 {
				return domain.ErrConflict
			}
			if matches == 0 {
				hooks[event] = append(hooks[event], expected.Groups[event])
			}
		}
		return saveHooks(directory, config, hooks, before)
	})
}

// Disable removes only exact owned definitions, preserving other hooks and metadata.
func Disable(ctx context.Context, directory string) error {
	if _, err := os.Stat(directory); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return installLocked(ctx, directory, func() error {
		raw, exists, err := privateRead(filepath.Join(directory, manifestName))
		if err != nil || !exists {
			return err
		}
		var manifest hookInstallation
		if jsonwire.DecodeExternalObject(raw, &manifest) != nil || manifest.Version != 1 || len(manifest.Groups) != len(hookEvents) {
			return domain.ErrConflict
		}
		config, hooks, before, err := loadHooks(directory)
		if err != nil {
			return err
		}
		for _, event := range hookEvents {
			expected, e := canonical(manifest.Groups[event])
			if e != nil || !bytes.Contains(expected, []byte("internal-codex-hook")) {
				return domain.ErrConflict
			}
			remaining := []json.RawMessage{}
			for _, group := range hooks[event] {
				value, e := canonical(group)
				if e != nil {
					return e
				}
				if bytes.Equal(value, expected) {
					continue
				}
				if bytes.Contains(value, []byte("internal-codex-hook")) {
					return domain.ErrConflict
				}
				remaining = append(remaining, group)
			}
			if len(remaining) == 0 {
				delete(hooks, event)
			} else {
				hooks[event] = remaining
			}
		}
		if err = saveHooks(directory, config, hooks, before); err != nil {
			return err
		}
		return os.Remove(filepath.Join(directory, manifestName))
	})
}
