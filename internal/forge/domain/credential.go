package domain

import (
	"strings"
	"workgraph.local/platform/internal/endpoint"
)

// CredentialEndpoint includes a custom instance's path prefix, not just its hostname.
func (r Repository) CredentialEndpoint() string {
	if r.Provider == "github" {
		canonical, ok := endpoint.GitHubRepository(r.BaseURL)
		if !ok {
			return ""
		}
		parts := strings.Split(canonical, "/")
		return strings.Join(parts[:len(parts)-2], "/")
	}
	base, ok := endpoint.GitLabBase(r.BaseURL)
	if !ok {
		return ""
	}
	return base
}
