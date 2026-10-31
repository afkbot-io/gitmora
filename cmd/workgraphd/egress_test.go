package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"workgraph.local/platform/internal/platform/deployment"
)

func TestEgressRoleParsesBeforeDatabaseAndRejectsInvalidAuthority(t *testing.T) {
	t.Setenv("WORKGRAPH_DATABASE_URL", "invalid-private-connection")
	var out bytes.Buffer
	if e := run(context.Background(), []string{"egress", "--help"}, &out); e != nil || !strings.Contains(out.String(), "443") {
		t.Fatal("egress help required database", e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for _, target := range []string{"gitlab.com:0", "user:private@gitlab.com:443", "127.0.0.1:443"} {
		if e := run(ctx, []string{"egress", "--target", target}, &out); e == nil || e == context.DeadlineExceeded || strings.Contains(e.Error(), "private") {
			t.Fatal("unsafe egress target accepted or leaked")
		}
	}
	for _, args := range [][]string{
		{"egress", "--visibility", "any"},
		{"egress", "--visibility", "private", "--target", "1.1.1.1:8443"},
	} {
		if e := run(ctx, args, &out); e == nil || e == context.DeadlineExceeded {
			t.Fatal("egress address policy was not enforced before startup")
		}
	}
}

func TestDeploymentRetainsBothReviewedPrivateNetworkOverlays(t *testing.T) {
	root := t.TempDir()
	paths := []string{filepath.Join(root, "compose.yaml"), filepath.Join(root, "tasktopia.yaml"), filepath.Join(root, "gitlab.yaml")}
	for _, path := range paths {
		if e := os.WriteFile(path, []byte("services: {}\n"), 0o600); e != nil {
			t.Fatal(e)
		}
	}
	var out bytes.Buffer
	if e := run(context.Background(), []string{"deployment", "init", "--directory", filepath.Join(root, "state"), "--compose", paths[0], "--overlay", paths[1], "--overlay", paths[2]}, &out); e != nil {
		t.Fatal(e)
	}
	var state deployment.Installation
	if e := json.Unmarshal(out.Bytes(), &state); e != nil || len(state.Overlays) != 2 || state.Overlays[0] != paths[1] || state.Overlays[1] != paths[2] {
		t.Fatal("reviewed connectivity overlay was silently lost", e)
	}
}
