package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHelpDoesNotRequireDatabaseAndFlagsAreCommandSpecific(t *testing.T) {
	t.Setenv("WORKGRAPH_DATABASE_URL", "")
	var output bytes.Buffer
	if err := run(context.Background(), []string{"--help"}, &output); err != nil {
		t.Fatal("root help required database", err)
	}
	if err := run(context.Background(), []string{"serve", "--help"}, &output); err != nil {
		t.Fatalf("help required database: %v", err)
	}
	if !strings.Contains(output.String(), "listen") {
		t.Fatal("help omitted supported flag")
	}
	if err := run(context.Background(), []string{"serve", "--gitlab-url", "https://unused.internal"}, &output); err == nil || !strings.Contains(err.Error(), "flags") {
		t.Fatal("bootstrap-only flag silently accepted by server")
	}
}

func TestInvalidDatabaseConfigurationDoesNotLeakSecret(t *testing.T) {
	t.Setenv("WORKGRAPH_DATABASE_URL", "postgres://name:super-secret@%ZZ")
	var output bytes.Buffer
	err := run(context.Background(), []string{"migrate"}, &output)
	if err == nil || strings.Contains(err.Error(), "super-secret") || strings.Contains(output.String(), "super-secret") {
		t.Fatal("database parse error exposed credential")
	}
}

func TestMalformedProviderEncryptionConfigurationDoesNotExposeKeyMaterial(t *testing.T) {
	t.Setenv("WORKGRAPH_ENCRYPTION_KEYS", `{"primary":"secret-key-material-must-not-be-reported"}`)
	t.Setenv("WORKGRAPH_ENCRYPTION_ACTIVE_KEY", "primary")
	t.Setenv("WORKGRAPH_GITLAB_ALLOWLIST", "https://gitlab.internal")
	if _, _, err := forgeServices(nil); err == nil || strings.Contains(err.Error(), "secret-key-material") {
		t.Fatal("encryption configuration error leaked key material")
	}
}

func TestProviderSecretsAcceptFilesWithoutEnvironmentMutation(t *testing.T) {
	clear := func(name string) {
		t.Helper()
		previous, present := os.LookupEnv(name)
		_ = os.Unsetenv(name)
		t.Cleanup(func() {
			if present {
				_ = os.Setenv(name, previous)
			} else {
				_ = os.Unsetenv(name)
			}
		})
	}
	clear("WORKGRAPH_ENCRYPTION_KEYS")
	root := t.TempDir()
	keys := filepath.Join(root, "keyring")
	if e := os.WriteFile(keys, []byte(`{"primary":"`+base64.StdEncoding.EncodeToString(make([]byte, 32))+`"}`), 0o600); e != nil {
		t.Fatal(e)
	}
	t.Setenv("WORKGRAPH_ENCRYPTION_KEYS_FILE", keys)
	t.Setenv("WORKGRAPH_ENCRYPTION_ACTIVE_KEY", "primary")
	if _, e := configuredVault(); e != nil {
		t.Fatal("provider keyring file was ignored", e)
	}
	if _, present := os.LookupEnv("WORKGRAPH_ENCRYPTION_KEYS"); present {
		t.Fatal("file secret was exported into environment")
	}
}

func TestContainerReadinessWorksWithoutDatabaseConfigurationAndRejectsUnsafeOrMisleadingResponses(t *testing.T) {
	t.Setenv("WORKGRAPH_DATABASE_URL", "")
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/health/ready" {
			_, _ = w.Write([]byte(`{"status":"ready"}`))
		} else {
			w.Header().Set("Location", "/health/ready")
			w.WriteHeader(302)
		}
	}))
	defer s.Close()
	var output bytes.Buffer
	if e := run(context.Background(), []string{"healthcheck", "--url", s.URL + "/health/ready"}, &output); e != nil {
		t.Fatal("scratch healthcheck requires database/shell", e)
	}
	for _, url := range []string{s.URL + "/redirect", "http://untrusted.invalid/health/ready", s.URL + "/health/ready?secret=private", strings.Replace(s.URL, "http://", "http://private@", 1) + "/health/ready"} {
		if e := run(context.Background(), []string{"healthcheck", "--url", url}, &output); e == nil || strings.Contains(e.Error(), "private") {
			t.Fatal("unsafe health probe accepted or leaked", e)
		}
	}
	if e := run(context.Background(), []string{"healthcheck", "--help"}, &output); e != nil {
		t.Fatal(e)
	}
}

func TestPreparedEncryptionKeysDoNotRequireEnablingAForge(t *testing.T) {
	for _, key := range []string{"WORKGRAPH_GITLAB_ALLOWLIST", "WORKGRAPH_GITLAB_PROXY_URL", "WORKGRAPH_GITHUB_ENABLED", "WORKGRAPH_GITHUB_API_URL", "WORKGRAPH_GITHUB_PROXY_URL", "WORKGRAPH_GITHUB_ALLOWLIST", "WORKGRAPH_GITHUB_PRIVATE", "WORKGRAPH_GITHUB_ENTERPRISE_PROXY_URL", "WORKGRAPH_TASKTOPIA_ALLOWLIST"} {
		t.Setenv(key, "")
	}
	t.Setenv("WORKGRAPH_ENCRYPTION_KEYS_FILE", "")
	if err := os.Unsetenv("WORKGRAPH_ENCRYPTION_KEYS_FILE"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WORKGRAPH_ENCRYPTION_KEYS", `{"primary":"`+base64.StdEncoding.EncodeToString(make([]byte, 32))+`"}`)
	t.Setenv("WORKGRAPH_ENCRYPTION_ACTIVE_KEY", "primary")
	forge, code, err := forgeServices(nil)
	if err != nil || forge != nil || code != nil {
		t.Fatal("An unconnected installation must start without source clients", err)
	}
	t.Setenv("WORKGRAPH_ENCRYPTION_KEYS", `{"primary":"invalid"}`)
	if _, _, err = forgeServices(nil); err == nil {
		t.Fatal("Malformed prepared keys were accepted")
	}
}
