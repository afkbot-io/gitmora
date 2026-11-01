package domain_test

import (
	"testing"
	"workgraph.local/platform/internal/directory/domain"
)

func TestGitHubCatalogUsesExplicitProviderIdentityWithoutGitLabFields(t *testing.T) {
	name := "Gitmora"
	input := domain.Command{RequestID: "11111111-1111-4111-8111-111111111111", EntityID: "22222222-2222-4222-8222-222222222222", ProjectID: "33333333-3333-4333-8333-333333333333", Action: domain.CreateRepository, Name: &name, Provider: "github", ProviderURL: "https://github.com/Afkbot-IO/Gitmora", ProviderRepositoryID: 42}
	got, err := domain.Normalize(input)
	if err != nil || got.ProviderURL != "https://github.com/afkbot-io/gitmora" || got.GitLabURL != "" || got.GitLabProjectID != 0 {
		t.Fatal("GitHub catalog identity", got, err)
	}
	input.GitLabURL = "https://gitlab.example.com"
	if _, err = domain.Normalize(input); err == nil {
		t.Fatal("mixed provider identities accepted")
	}
	input.GitLabURL = ""
	input.Provider = "unknown"
	if _, err = domain.Normalize(input); err == nil {
		t.Fatal("unknown provider accepted")
	}
	input.Provider = "github"
	input.ProviderRepositoryID = 0
	if _, err = domain.Normalize(input); err == nil {
		t.Fatal("missing immutable provider ID accepted")
	}
}
