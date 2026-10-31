// Package opencode installs an owned local OpenCode plugin without editing provider configuration.
package opencode

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/gofrs/flock"
	"workgraph.local/platform/internal/collector/domain"
	"workgraph.local/platform/internal/jsonwire"
)

//go:embed assets
var assets embed.FS

var helperNames = []string{"collector.ts", "coordinator.ts", "normalize.ts", "history.ts", "config.json"}
var legacyHelperNames = []string{"collector.ts", "coordinator.ts", "normalize.ts", "config.json"}

const entryName = "plugins/workgraph.ts"

type installation struct {
	Version int               `json:"version"`
	Files   map[string]string `json:"files"`
}

// DefaultDirectory follows OpenCode's XDG path, including on macOS.
func DefaultDirectory() (string, error) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Abs(filepath.Join(base, "opencode"))
}

// Enable installs only names owned by Workgraph; repeated identical installation is safe.
func Enable(ctx context.Context, directory, executable string) error {
	if !filepath.IsAbs(directory) || !filepath.IsAbs(executable) {
		return domain.ErrInvalid
	}
	info, err := os.Stat(executable)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return domain.ErrInvalid
	}
	files := map[string][]byte{}
	files[entryName], err = assets.ReadFile("assets/plugins/workgraph.ts")
	if err != nil {
		return err
	}
	for _, name := range helperNames {
		if name == "config.json" {
			files["workgraph/"+name], err = json.Marshal(struct {
				Version    int    `json:"version"`
				Executable string `json:"executable"`
			}{1, executable})
		} else {
			files["workgraph/"+name], err = assets.ReadFile("assets/workgraph/" + name)
		}
		if err != nil {
			return err
		}
	}
	expected := installation{Version: 1, Files: map[string]string{}}
	for name, data := range files {
		expected.Files[name] = digest(data)
	}
	return locked(ctx, directory, func() error {
		current, exists, err := readInstallation(directory)
		if err != nil {
			return err
		}
		if exists {
			if err := validate(directory, current); err != nil {
				return err
			}
			if len(current.Files) != len(expected.Files) {
				return domain.ErrConflict
			}
			for name, hash := range expected.Files {
				if current.Files[name] != hash {
					return domain.ErrConflict
				}
			}
		} else {
			for _, name := range []string{"workgraph", entryName} {
				if _, err := os.Lstat(filepath.Join(directory, name)); err == nil {
					return domain.ErrConflict
				} else if !os.IsNotExist(err) {
					return err
				}
			}
			stage, err := os.MkdirTemp(directory, ".workgraph-stage-")
			if err != nil {
				return err
			}
			defer os.RemoveAll(stage)
			for _, name := range helperNames {
				if err := os.WriteFile(filepath.Join(stage, name), files["workgraph/"+name], 0600); err != nil {
					return err
				}
			}
			raw, err := json.Marshal(expected)
			if err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(stage, "installation.json"), raw, 0600); err != nil {
				return err
			}
			if err := os.Rename(stage, filepath.Join(directory, "workgraph")); err != nil {
				return err
			}
		}
		if err := os.MkdirAll(filepath.Join(directory, "plugins"), 0700); err != nil {
			return err
		}
		entry := filepath.Join(directory, entryName)
		if _, err := os.Lstat(entry); err == nil {
			return nil
		} else if !os.IsNotExist(err) {
			return err
		}
		temporary, err := os.CreateTemp(directory, ".workgraph-entry-")
		if err != nil {
			return err
		}
		defer os.Remove(temporary.Name())
		_, writeErr := temporary.Write(files[entryName])
		closeErr := temporary.Close()
		if writeErr != nil {
			return writeErr
		}
		if closeErr != nil {
			return closeErr
		}
		return os.Link(temporary.Name(), entry)
	})
}

// Disable removes verified owned files and preserves customized or unrelated configuration.
func Disable(ctx context.Context, directory string) error {
	if !filepath.IsAbs(directory) {
		return domain.ErrInvalid
	}
	if _, err := os.Stat(directory); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	return locked(ctx, directory, func() error {
		current, exists, err := readInstallation(directory)
		if err != nil || !exists {
			return err
		}
		if err := validate(directory, current); err != nil {
			return err
		}
		helpers, err := ownedHelpers(current)
		if err != nil {
			return err
		}
		if err := os.Remove(filepath.Join(directory, entryName)); err != nil && !os.IsNotExist(err) {
			return err
		}
		for _, name := range helpers {
			if err := os.Remove(filepath.Join(directory, "workgraph", name)); err != nil {
				return err
			}
		}
		if err := os.Remove(filepath.Join(directory, "workgraph", "installation.json")); err != nil {
			return err
		}
		return os.Remove(filepath.Join(directory, "workgraph"))
	})
}

func locked(ctx context.Context, directory string, fn func() error) error {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	lock := flock.New(filepath.Join(directory, ".workgraph-install.lock"))
	ok, err := lock.TryLockContext(ctx, 50*time.Millisecond)
	if err != nil {
		return err
	}
	if !ok {
		return ctx.Err()
	}
	defer lock.Unlock()
	return fn()
}
func readInstallation(directory string) (installation, bool, error) {
	var result installation
	private := filepath.Join(directory, "workgraph")
	info, err := os.Lstat(private)
	if os.IsNotExist(err) {
		return result, false, nil
	}
	if err != nil {
		return result, false, err
	}
	if !info.IsDir() {
		return result, false, domain.ErrConflict
	}
	manifest := filepath.Join(private, "installation.json")
	info, err = os.Lstat(manifest)
	if os.IsNotExist(err) {
		return result, false, nil
	}
	if err != nil {
		return result, false, err
	}
	if !info.Mode().IsRegular() {
		return result, false, domain.ErrConflict
	}
	file, err := os.Open(manifest)
	if err != nil {
		return result, false, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, 8193))
	if err != nil {
		return result, false, err
	}
	if len(raw) > 8192 || jsonwire.DecodeObject(raw, &result) != nil || result.Version != 1 {
		return result, false, domain.ErrConflict
	}
	return result, true, nil
}
func validate(directory string, current installation) error {
	helpers, err := ownedHelpers(current)
	if err != nil {
		return err
	}
	names := []string{entryName}
	for _, name := range helpers {
		names = append(names, "workgraph/"+name)
	}
	if len(current.Files) != len(names) {
		return domain.ErrConflict
	}
	for _, name := range names {
		hash, ok := current.Files[name]
		if !ok {
			return domain.ErrConflict
		}
		full := filepath.Join(directory, name)
		info, err := os.Lstat(full)
		if os.IsNotExist(err) && name == entryName {
			continue
		}
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return domain.ErrConflict
		}
		bytes, err := os.ReadFile(full)
		if err != nil {
			return err
		}
		if digest(bytes) != hash {
			return domain.ErrConflict
		}
	}
	entries, err := os.ReadDir(filepath.Join(directory, "workgraph"))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() != "installation.json" && !slices.Contains(helpers, entry.Name()) {
			return domain.ErrConflict
		}
	}
	if len(entries) != len(helpers)+1 {
		return domain.ErrConflict
	}
	return nil
}

// ownedHelpers recognizes only the exact current or pre-pagination layout before inspecting/removing files.
func ownedHelpers(current installation) ([]string, error) {
	for _, layout := range [][]string{helperNames, legacyHelperNames} {
		if len(current.Files) != len(layout)+1 {
			continue
		}
		if _, ok := current.Files[entryName]; !ok {
			continue
		}
		complete := true
		for _, name := range layout {
			if _, ok := current.Files["workgraph/"+name]; !ok {
				complete = false
				break
			}
		}
		if complete {
			return layout, nil
		}
	}
	return nil, domain.ErrConflict
}
func digest(bytes []byte) string { hash := sha256.Sum256(bytes); return hex.EncodeToString(hash[:]) }
