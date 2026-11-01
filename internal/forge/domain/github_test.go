package domain_test

import (
	"testing"
	"workgraph.local/platform/internal/forge/domain"
)

func TestPublicGitHubModeDoesNotBroadenGitLabOrWebhookAuthority(t *testing.T) {
	public := domain.Secrets{Anonymous: true, WebhookMode: "disabled"}
	if public.ValidateForProvider("github") != nil {
		t.Fatal("explicit public mode rejected")
	}
	for _, provider := range []string{"", "gitlab", "unknown"} {
		if public.ValidateForProvider(provider) == nil {
			t.Fatal("public mode crossed provider", provider)
		}
	}
	public.APIToken = "fixture-only-token"
	if public.ValidateForProvider("github") == nil {
		t.Fatal("ambiguous anonymous/token selection")
	}
	token := domain.Secrets{APIToken: "fixture-only-token", WebhookMode: "disabled"}
	if token.ValidateForProvider("github") != nil || token.ValidateForProvider("") != nil {
		t.Fatal("explicit token mode rejected")
	}
	if (domain.Repository{ID: "id", Provider: "github"}).CredentialScope() == (domain.Repository{ID: "id"}).CredentialScope() {
		t.Fatal("provider envelopes share scope")
	}
}
