package github

import (
	"context"
	"strings"

	directory "workgraph.local/platform/internal/directory/domain"
	"workgraph.local/platform/internal/endpoint"
	"workgraph.local/platform/internal/forge/application"
	"workgraph.local/platform/internal/forge/domain"
)

// Router selects only operator-configured web/API pairs before credentials are sent.
type Router struct{ clients map[string]*Client }

// NewRouterWithEnterpriseProxy isolates the public and operator-selected Enterprise API routes.
func NewRouterWithEnterpriseProxy(api, proxy, enterpriseProxy string, enterprise []string, private bool) (*Router, error) {
	if len(enterprise) > 32 {
		return nil, domain.ErrInvalid
	}
	public, e := New(api, proxy)
	if e != nil {
		return nil, e
	}
	r := &Router{clients: map[string]*Client{"https://github.com": public}}
	for _, raw := range enterprise {
		base, ok := endpoint.GitLabBase(strings.TrimSpace(raw))
		if !ok || base == "https://github.com" || !strings.HasPrefix(base, "https://") {
			return nil, domain.ErrInvalid
		}
		if _, exists := r.clients[base]; exists {
			return nil, domain.ErrInvalid
		}
		client, e := newClient(base+"/api/v3", enterpriseProxy, base, private)
		if e != nil {
			return nil, e
		}
		r.clients[base] = client
	}
	return r, nil
}
func (r *Router) selectClient(raw string) (*Client, error) {
	canonical, ok := endpoint.GitHubRepository(raw)
	if !ok {
		return nil, domain.ErrInvalid
	}
	parts := strings.Split(canonical, "/")
	base := strings.Join(parts[:len(parts)-2], "/")
	c := r.clients[base]
	if c == nil {
		return nil, &application.ProviderError{Code: "provider_endpoint_not_allowed"}
	}
	return c, nil
}
func (r *Router) LookupRepository(ctx context.Context, raw string) (directory.ResolvedRepository, error) {
	return r.LookupRepositoryWithToken(ctx, raw, "")
}

// RepositoryEndpoint resolves an operator-approved web/API pair without sending credentials.
func (r *Router) RepositoryEndpoint(raw string) (string, error) {
	c, err := r.selectClient(raw)
	if err != nil {
		return "", err
	}
	return c.webBase, nil
}
func (r *Router) LookupRepositoryWithToken(ctx context.Context, raw, token string) (directory.ResolvedRepository, error) {
	c, e := r.selectClient(raw)
	if e != nil {
		return directory.ResolvedRepository{}, e
	}
	return c.LookupRepositoryWithToken(ctx, raw, token)
}

func (r *Router) VerifyProject(ctx context.Context, repo domain.Repository, token string) error {
	c, e := r.selectClient(repo.BaseURL)
	if e != nil {
		return e
	}
	return c.VerifyProject(ctx, repo, token)
}

func (r *Router) ConfirmCommit(ctx context.Context, repo domain.Repository, token string, sha string) (bool, error) {
	c, e := r.selectClient(repo.BaseURL)
	if e != nil {
		return false, e
	}
	return c.ConfirmCommit(ctx, repo, token, sha)
}

func (r *Router) Changes(ctx context.Context, repo domain.Repository, token string, cursor domain.Cursor) (domain.ChangePage, error) {
	c, e := r.selectClient(repo.BaseURL)
	if e != nil {
		return domain.ChangePage{}, e
	}
	return c.Changes(ctx, repo, token, cursor)
}

func (r *Router) MR(ctx context.Context, repo domain.Repository, token string, iid int64) (domain.MergeRequest, bool, error) {
	c, e := r.selectClient(repo.BaseURL)
	if e != nil {
		return domain.MergeRequest{}, false, e
	}
	return c.MR(ctx, repo, token, iid)
}

func (r *Router) MRCommits(ctx context.Context, repo domain.Repository, token string, mr domain.MergeRequest, page int) (domain.MRCommitPage, error) {
	c, e := r.selectClient(repo.BaseURL)
	if e != nil {
		return domain.MRCommitPage{}, e
	}
	return c.MRCommits(ctx, repo, token, mr, page)
}

func (r *Router) CodeDiff(ctx context.Context, repo domain.Repository, token string, sha string, page int) (domain.DiffPage, error) {
	c, e := r.selectClient(repo.BaseURL)
	if e != nil {
		return domain.DiffPage{}, e
	}
	return c.CodeDiff(ctx, repo, token, sha, page)
}

func (r *Router) CodeFile(ctx context.Context, repo domain.Repository, token string, sha, path string) (domain.FileObservation, error) {
	c, e := r.selectClient(repo.BaseURL)
	if e != nil {
		return domain.FileObservation{}, e
	}
	return c.CodeFile(ctx, repo, token, sha, path)
}
