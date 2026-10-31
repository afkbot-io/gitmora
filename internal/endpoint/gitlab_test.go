package endpoint_test

import (
	"testing"

	"workgraph.local/platform/internal/endpoint"
)

func TestGitLabBaseRejectsAmbiguousEndpoints(t *testing.T) {
	for _, value := range []string{
		"https://gitlab.internal?", "https://gitlab.internal?token=private",
		"https://gitlab.internal//nested", "https://gitlab.internal/gitlab/../other",
		"https://user:password@gitlab.internal", "https://gitlab.internal/#fragment",
		"https://gitlab.internal:0", "https://gitlab.internal:65536", "https://gitlab.internal:",
		"http://gitlab.internal", "https:///gitlab", "https://gitlab.internal/%2fhidden",
	} {
		t.Run(value, func(t *testing.T) {
			if _, ok := endpoint.GitLabBase(value); ok {
				t.Fatal("ambiguous provider endpoint accepted")
			}
		})
	}
}

func TestGitLabBaseRetainsCustomPortAndInstallationPrefix(t *testing.T) {
	for input, want := range map[string]string{
		"https://gitlab.internal:8443/gitlab/": "https://gitlab.internal:8443/gitlab",
		"https://gitlab.example.com/":          "https://gitlab.example.com",
		"http://127.0.0.1:8142/gitlab":         "http://127.0.0.1:8142/gitlab",
	} {
		if got, ok := endpoint.GitLabBase(input); !ok || got != want {
			t.Fatalf("endpoint identity changed: %q %v", got, ok)
		}
	}
}
