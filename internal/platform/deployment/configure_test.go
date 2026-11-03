package deployment_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"workgraph.local/platform/internal/platform/deployment"
)

func TestConfigurePreservesIdentitySecretsAndOtherOverlays(t *testing.T) {
	parent := t.TempDir()
	compose := filepath.Join(parent, "compose.yaml")
	overlay := filepath.Join(parent, "extra.yaml")
	for _, p := range []string{compose, overlay} {
		if err := os.WriteFile(p, []byte("services: {}\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	root := filepath.Join(parent, "state")
	original, err := deployment.Create(context.Background(), root, compose, 8080, "gitmora:old")
	if err != nil {
		t.Fatal(err)
	}
	password, err := os.ReadFile(filepath.Join(root, "secrets", "app_password"))
	if err != nil {
		t.Fatal(err)
	}
	change := deployment.Change{Image: "gitmora:new", AddOverlays: []string{overlay}}
	for i := 0; i < 2; i++ {
		next, err := deployment.Configure(context.Background(), root, change)
		if err != nil || next.ID != original.ID || next.Project() != original.Project() || len(next.Overlays) != 1 || next.Image != "gitmora:new" {
			t.Fatal(next, err)
		}
	}
	after, err := os.ReadFile(filepath.Join(root, "secrets", "app_password"))
	if err != nil || string(after) != string(password) {
		t.Fatal("configuration regenerated credentials")
	}
	if _, err := deployment.Configure(context.Background(), root, deployment.Change{Image: "bad image"}); err == nil {
		t.Fatal("invalid image accepted")
	}
	current, err := deployment.Load(root)
	if err != nil || current.Image != "gitmora:new" {
		t.Fatal("failed edit damaged state", err)
	}
}
