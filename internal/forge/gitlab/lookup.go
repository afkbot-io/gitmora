package gitlab

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	directory "workgraph.local/platform/internal/directory/domain"
	"workgraph.local/platform/internal/forge/application"
	"workgraph.local/platform/internal/forge/domain"
	"workgraph.local/platform/internal/jsonwire"
)

// LookupRepositoryWithToken resolves a path only below a configured GitLab base.
func (c *Client) LookupRepositoryWithToken(ctx context.Context, raw, token string) (directory.ResolvedRepository, error) {
	base, path, e := c.lookupLocation(raw)
	if e != nil {
		return directory.ResolvedRepository{}, e
	}
	canonical := strings.TrimSuffix(strings.TrimSuffix(raw, "/"), ".git")
	if len(token) < 8 || len(token) > 4096 || strings.ContainsAny(token, "\x00\r\n") {
		return directory.ResolvedRepository{}, domain.ErrInvalid
	}
	request, e := http.NewRequestWithContext(ctx, "GET", base+"/api/v4/projects/"+url.PathEscape(path), nil)
	if e != nil {
		return directory.ResolvedRepository{}, domain.ErrInvalid
	}
	request.Header.Set("PRIVATE-TOKEN", token)
	request.Header.Set("Accept", "application/json")
	response, e := c.http.Do(request)
	if e != nil {
		return directory.ResolvedRepository{}, &application.ProviderError{Code: "provider_unavailable", Retryable: true}
	}
	defer response.Body.Close()
	code := ""
	switch response.StatusCode {
	case 200:
	case 401, 403:
		code = "provider_unauthorized"
	case 404:
		code = "provider_project_not_found"
	case 429:
		code = "provider_rate_limited"
	default:
		code = "provider_unavailable"
	}
	if code != "" {
		return directory.ResolvedRepository{}, &application.ProviderError{Code: code}
	}
	rawBody, e := io.ReadAll(io.LimitReader(response.Body, 2*1024*1024+1))
	if e != nil || len(rawBody) > 2*1024*1024 {
		return directory.ResolvedRepository{}, invalidResponse()
	}
	var value struct {
		ID   int64  `json:"id"`
		URL  string `json:"web_url"`
		Path string `json:"path_with_namespace"`
	}
	if jsonwire.DecodeExternalObject(rawBody, &value) != nil || value.ID < 1 || value.URL != canonical || value.Path != path {
		return directory.ResolvedRepository{}, invalidResponse()
	}
	return directory.ResolvedRepository{URL: base, ID: value.ID, Name: value.Path}, nil
}

// RepositoryEndpoint resolves the exact allowlisted base before a saved token is decrypted.
func (c *Client) RepositoryEndpoint(raw string) (string, error) {
	base, _, err := c.lookupLocation(raw)
	return base, err
}

func (c *Client) lookupLocation(raw string) (string, string, error) {
	u, e := url.Parse(raw)
	if e != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || u.Opaque != "" || u.ForceQuery {
		return "", "", domain.ErrInvalid
	}
	canonical := strings.TrimSuffix(strings.TrimSuffix(raw, "/"), ".git")
	base := ""
	for candidate := range c.allowed {
		if strings.HasPrefix(canonical, candidate+"/") && len(candidate) > len(base) {
			base = candidate
		}
	}
	if base == "" {
		return "", "", &application.ProviderError{Code: "provider_endpoint_not_allowed"}
	}
	path := strings.TrimPrefix(canonical, base+"/")
	if len(path) > 1000 || !strings.Contains(path, "/") || strings.Contains(path, "//") {
		return "", "", domain.ErrInvalid
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." {
			return "", "", domain.ErrInvalid
		}
	}
	return base, path, nil
}
