package gitrepo

import (
	"errors"
	"os"
	"path/filepath"
)

// CollectionDisabled is independent of SQLite so consent survives a damaged or restored ledger.
// A present marker of any kind disables collection; unexpected I/O errors fail closed at callers.
func (r *Repository) CollectionDisabled() (bool, error) {
	_, err := os.Lstat(filepath.Join(r.CommonDir, "workgraph", "collection-disabled"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func (r *Repository) SetCollectionDisabled(disabled bool) error {
	path := filepath.Join(r.CommonDir, "workgraph", "collection-disabled")
	if !disabled {
		err := os.Remove(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		return syncCollectionDirectory(filepath.Dir(path))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if err := syncCollectionDirectory(r.CommonDir); err != nil {
		return err
	}
	// O_EXCL never follows an existing symlink. An existing marker already denies collection.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if errors.Is(err, os.ErrExist) {
		return syncCollectionDirectory(filepath.Dir(path))
	}
	if err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return syncCollectionDirectory(filepath.Dir(path))
}

func syncCollectionDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
