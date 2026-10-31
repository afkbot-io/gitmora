package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestCoreServerRejectsRetiredWorkersBeforeDatabase(t *testing.T) {
	t.Setenv("WORKGRAPH_DATABASE_URL", "")
	for _, command := range []string{"inference-worker", "review-worker"} {
		var out bytes.Buffer
		err := run(context.Background(), []string{command, "--help"}, &out)
		if err == nil || out.Len() != 0 || !strings.Contains(err.Error(), "unknown command") {
			t.Fatal("retired worker still registered", command, err)
		}
	}
}
