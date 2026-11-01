package main

import (
	"context"

	directory "workgraph.local/platform/internal/directory/domain"
	"workgraph.local/platform/internal/forge/application"
	"workgraph.local/platform/internal/forge/github"
	"workgraph.local/platform/internal/forge/gitlab"
)

type repositoryResolver struct {
	github *github.Router
	gitlab *gitlab.Client
}

func (r repositoryResolver) LookupRepository(ctx context.Context, url string) (directory.ResolvedRepository, error) {
	return r.LookupRepositoryWithToken(ctx, url, "github", "")
}
func (r repositoryResolver) LookupRepositoryWithToken(ctx context.Context, url, provider, token string) (directory.ResolvedRepository, error) {
	if provider == "github" && r.github != nil {
		return r.github.LookupRepositoryWithToken(ctx, url, token)
	}
	if provider == "gitlab" && r.gitlab != nil {
		return r.gitlab.LookupRepositoryWithToken(ctx, url, token)
	}
	return directory.ResolvedRepository{}, &application.ProviderError{Code: "provider_endpoint_not_allowed"}
}
