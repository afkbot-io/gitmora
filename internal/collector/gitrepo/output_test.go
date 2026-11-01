package gitrepo

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGitQueryRejectsOversizedStdout(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\nexec dd if=/dev/zero bs=1048576 count=17 2>/dev/null\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	output, err := runEnvironment(ctx, bin, nil, nil, "status")
	if err == nil || len(output) != 0 {
		t.Fatal("oversized query returned partial or accepted output")
	}
	if ctx.Err() != nil {
		t.Fatal("query exhausted deadline instead of stopping at its byte budget")
	}
}
