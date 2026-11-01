package endpoint_test

import (
	"testing"
	"workgraph.local/platform/internal/endpoint"
)

func TestGitHubRepositoryIdentityRejectsDestinationsAndNormalizesCloneURLs(t *testing.T) {
	for _, input := range []string{"https://github.com/Afkbot-IO/Gitmora", "https://github.com/afkbot-io/gitmora.git"} {
		got, ok := endpoint.GitHubRepository(input)
		if !ok || got != "https://github.com/afkbot-io/gitmora" {
			t.Fatalf("canonical repository identity: %q %v", got, ok)
		}
	}
	for _, input := range []string{"http://github.com/a/b", "https://github.com:443/a/b", "https://github.com@other.test/a/b", "https://api.github.com/a/b", "https://github.com/a/b/extra", "https://github.com/a/%62", "https://github.com/a/b?token=x", "https://github.com/a/b#main", "https://github.com/a/..", "https://github.com//a/b", "https://github.com/a/b/"} {
		if _, ok := endpoint.GitHubRepository(input); ok {
			t.Fatal("unsafe or ambiguous repository accepted", input)
		}
	}
}
