package codex_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"workgraph.local/platform/internal/agent/codex"
)

func TestOwnedCodexHooksPreserveForeignConfigurationAndRequireTrust(t *testing.T) {
	root := t.TempDir()
	exe, _ := os.Executable()
	initial := `{"description":"Foreign settings","hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo foreign"}]}]}}`
	file := filepath.Join(root, "hooks.json")
	if err := os.WriteFile(file, []byte(initial), 0600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := codex.Enable(context.Background(), root, exe, exe); err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := os.ReadFile(file)
	if strings.Count(string(raw), "internal-codex-hook") != 4 || !strings.Contains(string(raw), "echo foreign") {
		t.Fatal("owned hooks missing or foreign overwritten")
	}
	// Another program appends unrelated metadata between enable and disable.
	var config map[string]any
	json.Unmarshal(raw, &config)
	config["description"] = "New foreign metadata"
	raw, _ = json.Marshal(config)
	os.WriteFile(file, raw, 0600)
	if err := codex.Disable(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(file)
	if strings.Contains(string(raw), "internal-codex-hook") || !strings.Contains(string(raw), "echo foreign") || !strings.Contains(string(raw), "New foreign metadata") {
		t.Fatal("disable removed foreign configuration")
	}
	if err := codex.Disable(context.Background(), root); err != nil {
		t.Fatal(err)
	}
}

func TestCodexInstallationDoesNotAdoptUnownedOrRewriteChangedCommands(t *testing.T) {
	root := t.TempDir()
	exe, _ := os.Executable()
	if err := codex.Enable(context.Background(), root, exe, exe); err != nil {
		t.Fatal(err)
	}
	if err := codex.Enable(context.Background(), root, exe, "/changed/codex"); err == nil {
		t.Fatal("changed executable adopted silently")
	}
	file := filepath.Join(root, "hooks.json")
	raw, _ := os.ReadFile(file)
	modified := strings.ReplaceAll(string(raw), "internal-codex-hook", "internal-codex-hook-customized")
	os.WriteFile(file, []byte(modified), 0600)
	if err := codex.Disable(context.Background(), root); err == nil {
		t.Fatal("customized hook not reported")
	}
	after, _ := os.ReadFile(file)
	if string(after) != modified {
		t.Fatal("customized hook was changed")
	}
}
