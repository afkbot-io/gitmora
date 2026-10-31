package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInterruptedInstallDoesNotSuggestAnAuthenticationOperation(t *testing.T) {
	message := interruptionMessage([]string{"self", "install"})
	if !strings.Contains(message, "installation") || !strings.Contains(message, "same installer") || strings.Contains(message, "auth") {
		t.Fatal("interrupted installation has unrelated recovery guidance", message)
	}
	for _, item := range []struct{ command, expected string }{{"auth", "auth login"}, {"import", "immutable source"}} {
		if message := interruptionMessage([]string{item.command}); !strings.Contains(message, item.expected) {
			t.Fatal("existing command recovery changed", item.command, message)
		}
	}
	if strings.Contains(interruptionMessage([]string{"capture"}), "auth") || strings.Contains(interruptionMessage(nil), "auth") {
		t.Fatal("generic cancellation claims an authentication operation")
	}
}

func TestVersionNeedsNoRepositoryOrAuthentication(t *testing.T) {
	var output, diagnostic bytes.Buffer
	if err := run(context.Background(), []string{"version"}, nil, &output, &diagnostic); err != nil {
		t.Fatal(err)
	}
	var body struct {
		SchemaVersion int    `json:"schema_version"`
		Version       string `json:"version"`
		GOOS          string `json:"os"`
		GOARCH        string `json:"arch"`
	}
	if err := json.Unmarshal(output.Bytes(), &body); err != nil || body.SchemaVersion != 1 || body.Version == "" || body.GOOS != runtime.GOOS || body.GOARCH != runtime.GOARCH || diagnostic.Len() != 0 {
		t.Fatal(output.String(), err)
	}
	output.Reset()
	if err := run(context.Background(), []string{"version", "unexpected"}, nil, &output, &diagnostic); err == nil || output.Len() != 0 {
		t.Fatal("invalid version args accepted")
	}
}

func TestSelfInstallRequiresExactArgumentsAndKeepsOutputTyped(t *testing.T) {
	dir := t.TempDir()
	var output, diagnostic bytes.Buffer
	for _, args := range [][]string{{"self"}, {"self", "install"}, {"self", "install", "--directory", dir, "surprise"}, {"self", "install", "--directory", dir, "--replace-sha256", "bad"}} {
		if err := run(context.Background(), args, nil, &output, &diagnostic); err == nil || output.Len() != 0 {
			t.Fatal("invalid install args accepted", args)
		}
	}
	if err := run(context.Background(), []string{"self", "install", "--directory", dir}, nil, &output, &diagnostic); err != nil {
		t.Fatal(err)
	}
	var receipt struct {
		Path, SHA256 string
		Changed      bool
	}
	canonical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(output.Bytes(), &receipt); err != nil || !receipt.Changed || receipt.Path != filepath.Join(canonical, "gitmora") || len(receipt.SHA256) != 64 {
		t.Fatal(output.String(), err)
	}
	if _, err := os.Stat(receipt.Path); err != nil {
		t.Fatal(err)
	}
}
