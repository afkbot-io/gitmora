package gitlocal_test

import (
	"strings"
	"testing"

	"workgraph.local/platform/internal/gitlocal"
)

func TestReadOnlyEnvironmentDeniesAllTransportsAndDiscardsInheritedGitOverrides(t *testing.T) {
	t.Setenv("GIT_ALLOW_PROTOCOL", "ext:https")
	t.Setenv("GIT_DIR", "/another/repository")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	values := map[string]string{}
	for _, entry := range gitlocal.Environment() {
		key, value, _ := strings.Cut(entry, "=")
		if _, duplicate := values[key]; duplicate {
			t.Fatal("ambiguous child environment", key)
		}
		values[key] = value
	}
	if value, set := values["GIT_ALLOW_PROTOCOL"]; !set || value != "" {
		t.Fatal("all transports must be explicitly denied")
	}
	if _, exists := values["GIT_DIR"]; exists {
		t.Fatal("inherited repository selection survived")
	}
	if _, exists := values["GIT_CONFIG_COUNT"]; exists {
		t.Fatal("inherited configuration injection survived")
	}
	if values["GIT_NO_LAZY_FETCH"] != "1" || values["GIT_NO_REPLACE_OBJECTS"] != "1" || values["GIT_CONFIG_GLOBAL"] != "/dev/null" {
		t.Fatal("read-only source policy missing")
	}
}
