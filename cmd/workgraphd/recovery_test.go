package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestOfflineRecoveryCommandsRejectUnsafeInputBeforeOpeningAnyDatabase(t *testing.T) {
	t.Setenv("WORKGRAPH_DATABASE_URL", "invalid-private-database-url")
	var out bytes.Buffer
	if e := recoveryCommand(context.Background(), []string{"--help"}, strings.NewReader(""), &out); e != nil || !strings.Contains(out.String(), "quarantine") {
		t.Fatal("offline recovery help required database", e)
	}
	for _, args := range [][]string{{"unknown"}, {"quarantine", "unexpected"}, {"metadata", "unexpected"}} {
		out.Reset()
		if e := recoveryCommand(context.Background(), args, strings.NewReader("{}"), &out); e == nil || strings.Contains(e.Error(), "private") || out.Len() != 0 {
			t.Fatal("invalid command leaked configuration", e)
		}
	}
	for _, body := range []string{"{}", `{"secret":"private-key"}`, strings.Repeat("x", 8193), `{"version":1,"version":1}`} {
		out.Reset()
		if e := recoveryCommand(context.Background(), []string{"quarantine"}, strings.NewReader(body), &out); e == nil || strings.Contains(e.Error(), "private") || out.Len() != 0 {
			t.Fatal("invalid plan exposed input or database diagnostics", e)
		}
	}
}
