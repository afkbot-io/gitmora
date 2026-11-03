package deployment

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"

	"github.com/gofrs/flock"
)

// Change edits only executable/config paths. Installation identity, ports, secrets
// and persistent volume namespace remain pinned to their original installation.
type Change struct {
	Image, ComposeFile          string
	AddOverlays, RemoveOverlays []string
}

// Configure persists a validated atomic operator edit without starting containers.
// Repeating the same edit is harmless; existing ordered overlays are preserved.
func Configure(ctx context.Context, root string, change Change) (Installation, error) {
	if _, err := Load(root); err != nil {
		return Installation{}, err
	}
	lockPath := filepath.Join(root, ".operations.lock")
	if info, err := os.Lstat(lockPath); err == nil && (!info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0) {
		return Installation{}, ErrFilesystem
	} else if err != nil && !os.IsNotExist(err) {
		return Installation{}, ErrFilesystem
	}
	lock := flock.New(lockPath)
	owned, err := lock.TryLock()
	if err != nil {
		return Installation{}, ErrFilesystem
	}
	if !owned {
		return Installation{}, ErrBusy
	}
	defer lock.Unlock()
	i, err := Load(root)
	if err != nil {
		return Installation{}, err
	}
	if change.Image != "" {
		i.Image = change.Image
	}
	if change.ComposeFile != "" {
		i.ComposeFile = change.ComposeFile
	}
	for _, p := range change.RemoveOverlays {
		if !absolute(p) {
			return Installation{}, ErrInvalid
		}
		i.Overlays = slices.DeleteFunc(i.Overlays, func(v string) bool { return v == p })
	}
	for _, p := range change.AddOverlays {
		if !slices.Contains(i.Overlays, p) {
			i.Overlays = append(i.Overlays, p)
		}
	}
	if i.Validate() != nil {
		return Installation{}, ErrInvalid
	}
	for _, p := range append([]string{i.ComposeFile}, i.Overlays...) {
		if info, err := os.Stat(p); err != nil || !info.Mode().IsRegular() {
			return Installation{}, ErrInvalid
		}
	}
	if err := ctx.Err(); err != nil {
		return Installation{}, err
	}
	raw, _ := json.MarshalIndent(i, "", "  ")
	f, err := os.CreateTemp(root, ".installation-*.tmp")
	if err != nil {
		return Installation{}, ErrFilesystem
	}
	path := f.Name()
	defer os.Remove(path)
	if _, err = f.Write(append(raw, '\n')); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return Installation{}, ErrFilesystem
	}
	if os.Rename(path, filepath.Join(root, "installation.json")) != nil || syncDir(root) != nil {
		return Installation{}, ErrFilesystem
	}
	return i, nil
}
