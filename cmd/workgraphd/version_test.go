package main

import (
	"bytes"
	"context"
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	"workgraph.local/platform/internal/distribution/buildinfo"
)

func TestServerVersionNeedsNoDatabaseCredentialOrRole(t *testing.T) {
	t.Setenv("WORKGRAPH_DATABASE_URL", "private-invalid-database")
	t.Setenv("WORKGRAPH_ENCRYPTION_KEYS_FILE", "/nonexistent/private-keyring")
	t.Setenv("WORKGRAPH_OIDC_CLIENT_SECRET_FILE", "/nonexistent/private-identity-key")
	for _, command := range []string{"version", "--version"} {
		var out bytes.Buffer
		if err := run(context.Background(), []string{command}, &out); err != nil {
			t.Fatal(err)
		}
		var info buildinfo.Info
		if err := json.Unmarshal(out.Bytes(), &info); err != nil || info != buildinfo.Current() || info.OS != runtime.GOOS || info.Arch != runtime.GOARCH || strings.Contains(out.String(), "private-") {
			t.Fatal("server version is not pure typed release metadata", out.String(), err)
		}
		out.Reset()
		if err := run(context.Background(), []string{command, "unexpected"}, &out); err == nil || out.Len() != 0 {
			t.Fatal("invalid version args produced output")
		}
	}
	var help bytes.Buffer
	if err := run(context.Background(), []string{"version", "--help"}, &help); err != nil || !strings.Contains(help.String(), "version") {
		t.Fatal("version help unavailable", err)
	}
}
