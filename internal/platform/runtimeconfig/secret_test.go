package runtimeconfig_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"workgraph.local/platform/internal/platform/runtimeconfig"
)

func unset(t *testing.T, key string) {
	t.Helper()
	previous, present := os.LookupEnv(key)
	if e := os.Unsetenv(key); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if present {
			_ = os.Setenv(key, previous)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

func TestSecretFileIsBoundedAndMutuallyExclusiveWithoutChangingTheEnvironment(t *testing.T) {
	name := string(runtimeconfig.DatabaseURL)
	unset(t, name)
	unset(t, name+"_FILE")
	path := filepath.Join(t.TempDir(), "database")
	private := "postgres://fixture:private-deployment-test@postgres/workgraph?sslmode=disable"
	if e := os.WriteFile(path, []byte(private+"\r\n"), 0o600); e != nil {
		t.Fatal(e)
	}
	t.Setenv(name+"_FILE", path)
	got, e := runtimeconfig.Secret(runtimeconfig.DatabaseURL)
	if e != nil || got.Reveal() != private {
		t.Fatal("file injection failed", e)
	}
	if _, present := os.LookupEnv(name); present {
		t.Fatal("secret was copied into process environment")
	}
	encoded, _ := json.Marshal(got)
	if strings.Contains(fmt.Sprintf("%s %+v %#v", got, got, got)+string(encoded), private) {
		t.Fatal("generic secret representation exposed plaintext")
	}
	t.Setenv(name, "")
	if _, e = runtimeconfig.Secret(runtimeconfig.DatabaseURL); e == nil || strings.Contains(e.Error(), private) || strings.Contains(e.Error(), path) {
		t.Fatal("ambiguous secret sources accepted or exposed", e)
	}
}

func TestSecretRejectsMissingEmptyOversizedOrSpecialFilesAndUnknownNames(t *testing.T) {
	name := string(runtimeconfig.EncryptionKeys)
	unset(t, name)
	unset(t, name+"_FILE")
	if got, e := runtimeconfig.Secret(runtimeconfig.EncryptionKeys); e != nil || got.Reveal() != "" {
		t.Fatal("optional unconfigured value", e)
	}
	root := t.TempDir()
	for _, value := range []string{"", "\n", strings.Repeat("p", 65537), "secret\x00suffix"} {
		file := filepath.Join(root, "secret")
		if e := os.WriteFile(file, []byte(value), 0o600); e != nil {
			t.Fatal(e)
		}
		t.Setenv(name+"_FILE", file)
		if _, e := runtimeconfig.Secret(runtimeconfig.EncryptionKeys); e == nil {
			t.Fatal("invalid secret file accepted")
		}
	}
	for _, file := range []string{root, filepath.Join(root, "missing"), "relative-secret", "/dev/null"} {
		t.Setenv(name+"_FILE", file)
		if _, e := runtimeconfig.Secret(runtimeconfig.EncryptionKeys); e == nil || strings.Contains(e.Error(), file) {
			t.Fatal("unusable file accepted or path exposed", e)
		}
	}
	if _, e := runtimeconfig.Secret(runtimeconfig.Name("WORKGRAPH_UNTRUSTED_secret")); e == nil {
		t.Fatal("unregistered secret key accepted")
	}
}

func TestDirectSecretsRemainExactAndMultilineKeyringFilesKeepTheirContents(t *testing.T) {
	unset(t, string(runtimeconfig.OIDCClientSecret)+"_FILE")
	t.Setenv(string(runtimeconfig.OIDCClientSecret), " exact secret ")
	got, e := runtimeconfig.Secret(runtimeconfig.OIDCClientSecret)
	if e != nil || got.Reveal() != " exact secret " {
		t.Fatal("direct secret was normalized", e)
	}
	unset(t, string(runtimeconfig.EncryptionKeys))
	path := filepath.Join(t.TempDir(), "keys")
	jsonKeyring := "{\n  \"primary\": \"synthetic\"\n}"
	if e = os.WriteFile(path, []byte(jsonKeyring+"\n"), 0o600); e != nil {
		t.Fatal(e)
	}
	t.Setenv(string(runtimeconfig.EncryptionKeys)+"_FILE", path)
	got, e = runtimeconfig.Secret(runtimeconfig.EncryptionKeys)
	if e != nil || got.Reveal() != jsonKeyring {
		t.Fatal("multiline payload was damaged", e)
	}
}
