package domain_test

import (
	"testing"
	"workgraph.local/platform/internal/collector/domain"
)

func TestConfigurationContainsNoCredentialOrInsecureNonlocalEndpoint(t *testing.T) {
	base := domain.Config{Version: 1, RepositoryID: repoID, ServerURL: "https://workgraph.internal", Remote: "origin", RemoteURL: "git@gitlab.internal:team/app.git", TokenEnv: "WORKGRAPH_TOKEN"}
	if _, err := domain.NormalizeConfig(base); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*domain.Config){
		func(c *domain.Config) { c.ServerURL = "https://user:password@workgraph.internal" },
		func(c *domain.Config) { c.ServerURL = "http://workgraph.internal" },
		func(c *domain.Config) { c.ServerURL = "https://workgraph.internal/root/../other" },
		func(c *domain.Config) { c.ServerURL = "https://workgraph.internal?" },
		func(c *domain.Config) { c.RemoteURL = "https://token@gitlab.internal/app.git" },
		func(c *domain.Config) { c.RemoteURL = "ssh://git:password@gitlab.internal/app.git" },
		func(c *domain.Config) { c.RemoteURL = "https://git:password@gitlab.internal/app%zz.git" },
		func(c *domain.Config) { c.RemoteURL = "https://gitlab.internal/app.git?" },
		func(c *domain.Config) { c.TokenEnv = "HOME" },
	} {
		config := base
		mutate(&config)
		if _, err := domain.NormalizeConfig(config); err == nil {
			t.Error("unsafe configuration accepted")
		}
	}
}

func TestRemoteSupportsSSHUserWithoutPasswordAndLocalGitPaths(t *testing.T) {
	for _, remote := range []string{"ssh://git@gitlab.internal/team/app.git", "git@gitlab.internal:team/app.git", "/srv/git/app.git", "../bare.git"} {
		input := domain.Config{Version: 1, RepositoryID: repoID, ServerURL: "https://workgraph.internal", Remote: "origin", RemoteURL: remote, TokenEnv: "WORKGRAPH_TOKEN"}
		if _, err := domain.NormalizeConfig(input); err != nil {
			t.Errorf("credential-free Git remote rejected: %s", remote)
		}
	}
}
