package opencode_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"workgraph.local/platform/internal/agent/opencode"
	"workgraph.local/platform/internal/collector/domain"
)

func TestInstallIsPrivateIdempotentAndDisablePreservesOtherOpenCodeConfiguration(t *testing.T) {
	root := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"provider":{"local":{}}}`)
	if err := os.WriteFile(filepath.Join(root, "opencode.json"), original, 0600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := opencode.Enable(context.Background(), root, exe); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{"workgraph/config.json", "workgraph/coordinator.ts", "plugins/workgraph.ts"} {
		info, err := os.Stat(filepath.Join(root, file))
		if err != nil || info.Mode().Perm()&0077 != 0 {
			t.Fatal("plugin installation is missing or not private")
		}
	}
	if err := opencode.Disable(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "opencode.json"))
	if err != nil || string(data) != string(original) {
		t.Fatal("existing OpenCode config changed")
	}
	if _, err := os.Stat(filepath.Join(root, "plugins/workgraph.ts")); !os.IsNotExist(err) {
		t.Fatal("owned entry retained")
	}
}

func TestPagedHistoryHelperIsPrivateAndLegacyOwnedLayoutCanBeDisabled(t *testing.T) {
	root := t.TempDir()
	exe, _ := os.Executable()
	if err := opencode.Enable(context.Background(), root, exe); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(root, "workgraph", "history.ts")
	info, err := os.Stat(name)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		t.Fatal("paged reader missing or public")
	}
	legacyInstallation(t, root)
	if err := opencode.Disable(context.Background(), root); err != nil {
		t.Fatal("new binary cannot disable verified legacy layout", err)
	}
}

func legacyInstallation(t *testing.T, root string) {
	t.Helper()
	name := filepath.Join(root, "workgraph", "installation.json")
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Version int               `json:"version"`
		Files   map[string]string `json:"files"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	delete(manifest.Files, "workgraph/history.ts")
	raw, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "workgraph", "history.ts")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestLegacyLayoutWithUnownedNewHelperIsPreserved(t *testing.T) {
	root := t.TempDir()
	exe, _ := os.Executable()
	if err := opencode.Enable(context.Background(), root, exe); err != nil {
		t.Fatal(err)
	}
	legacyInstallation(t, root)
	name := filepath.Join(root, "workgraph", "history.ts")
	if err := os.WriteFile(name, []byte("user-owned helper"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := opencode.Disable(context.Background(), root); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("unowned helper was removed", err)
	}
	if raw, err := os.ReadFile(name); err != nil || string(raw) != "user-owned helper" {
		t.Fatal("unowned helper changed")
	}
}

func TestInstallAndDisablePreserveUnownedOrModifiedFiles(t *testing.T) {
	root := t.TempDir()
	exe, _ := os.Executable()
	if err := os.Mkdir(filepath.Join(root, "plugins"), 0700); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(root, "plugins/workgraph.ts")
	if err := os.WriteFile(entry, []byte("user plugin"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := opencode.Enable(context.Background(), root, exe); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("unowned plugin overwritten")
	}
	if err := os.Remove(entry); err != nil {
		t.Fatal(err)
	}
	if err := opencode.Enable(context.Background(), root, exe); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry, []byte("customized"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := opencode.Disable(context.Background(), root); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("customized plugin removed")
	}
	if data, _ := os.ReadFile(entry); string(data) != "customized" {
		t.Fatal("customization lost")
	}
}

func TestDisableRejectsAnOwnedDirectoryReplacedByASymlink(t *testing.T) {
	root := t.TempDir()
	exe, _ := os.Executable()
	if err := opencode.Enable(context.Background(), root, exe); err != nil {
		t.Fatal(err)
	}
	saved := filepath.Join(t.TempDir(), "preserved")
	if err := os.Rename(filepath.Join(root, "workgraph"), saved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(saved, filepath.Join(root, "workgraph")); err != nil {
		t.Fatal(err)
	}
	if err := opencode.Disable(context.Background(), root); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("symlink directory followed during disable")
	}
	if _, err := os.Stat(filepath.Join(saved, "config.json")); err != nil {
		t.Fatal("external target was modified")
	}
}
