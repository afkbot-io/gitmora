// Package github implements bounded, read-only GitHub.com REST acquisition.
package github

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	directory "workgraph.local/platform/internal/directory/domain"
	"workgraph.local/platform/internal/endpoint"
	"workgraph.local/platform/internal/forge/application"
	"workgraph.local/platform/internal/forge/domain"
	"workgraph.local/platform/internal/identifier"
	"workgraph.local/platform/internal/jsonwire"
	"workgraph.local/platform/internal/platform/netroute"
)

const pageSize = 50

// Client has a fixed API authority; repository/source URLs never select an HTTP destination.
type Client struct {
	base string
	http *http.Client
}

// New accepts the public API or an explicit loopback development API. Proxy DNS/dial remains private.
func New(base, proxy string) (*Client, error) {
	normalized, ok := endpoint.GitLabBase(base)
	if !ok {
		return nil, domain.ErrInvalid
	}
	api, _ := url.Parse(normalized)
	loopback := api.Hostname() == "localhost"
	if ip := net.ParseIP(api.Hostname()); ip != nil && ip.IsLoopback() {
		loopback = true
	}
	if normalized != "https://api.github.com" && (!loopback || api.Path != "") {
		return nil, domain.ErrInvalid
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.MaxResponseHeaderBytes = 64 * 1024
	authority := api.Host
	if api.Port() == "" {
		authority = net.JoinHostPort(api.Hostname(), "443")
		if api.Scheme == "http" {
			authority = net.JoinHostPort(api.Hostname(), "80")
		}
	}
	visibility := netroute.PublicIPv4
	if loopback {
		visibility = netroute.Private
	}
	if proxy != "" {
		p, e := url.Parse(proxy)
		if e != nil || len(proxy) > 256 || p.Scheme != "http" || p.Host == "" || p.User != nil || p.Path != "" || p.RawPath != "" || p.RawQuery != "" || p.ForceQuery || p.Fragment != "" || p.Opaque != "" || api.Scheme != "https" {
			return nil, domain.ErrInvalid
		}
		authority = p.Host
		visibility = netroute.Private
		transport.Proxy = http.ProxyURL(p)
		transport.DialTLS = nil
		transport.DialTLSContext = nil
		transport.ProxyConnectHeader = nil
		transport.GetProxyConnectHeader = nil
	}
	dial, err := netroute.NewBoundDialer(authority, visibility, nil, nil)
	if err != nil {
		return nil, domain.ErrInvalid
	}
	transport.DialContext = dial.DialContext
	return &Client{base: normalized, http: &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func invalid() error {
	return &application.ProviderError{Code: "provider_invalid_response", Retryable: true}
}
func tokenValid(token string) bool {
	if token == "" {
		return true
	}
	if len(token) < 8 || len(token) > 4096 {
		return false
	}
	for _, b := range []byte(token) {
		if b < 33 || b > 126 {
			return false
		}
	}
	return true
}
func (c *Client) request(ctx context.Context, token, path string, limit int64) ([]byte, http.Header, bool, error) {
	if !tokenValid(token) || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return nil, nil, false, domain.ErrInvalid
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return nil, nil, false, domain.ErrInvalid
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "Gitmora/1")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := c.http.Do(req)
	if err != nil {
		return nil, nil, false, &application.ProviderError{Code: "provider_unavailable", Retryable: true}
	}
	defer response.Body.Close()
	switch {
	case response.StatusCode == 404:
		return nil, response.Header, false, nil
	case response.StatusCode == 429 || response.StatusCode == 403 && response.Header.Get("X-RateLimit-Remaining") == "0" || response.StatusCode == 403 && response.Header.Get("Retry-After") != "":
		retry := time.Minute
		if seconds, e := strconv.ParseInt(response.Header.Get("Retry-After"), 10, 64); e == nil && seconds > 0 && seconds <= 3600 {
			retry = time.Duration(seconds) * time.Second
		} else if stamp, e := strconv.ParseInt(response.Header.Get("X-RateLimit-Reset"), 10, 64); e == nil {
			if d := time.Until(time.Unix(stamp, 0)); d > 0 && d <= time.Hour {
				retry = d
			}
		}
		return nil, nil, false, &application.ProviderError{Code: "provider_rate_limited", Retryable: true, RetryAfter: retry}
	case response.StatusCode == 401:
		return nil, nil, false, &application.ProviderError{Code: "provider_unauthorized"}
	case response.StatusCode == 403:
		return nil, nil, false, &application.ProviderError{Code: "provider_forbidden"}
	case response.StatusCode >= 500:
		return nil, nil, false, &application.ProviderError{Code: "provider_unavailable", Retryable: true}
	case response.StatusCode != 200:
		return nil, nil, false, invalid()
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return nil, nil, false, invalid()
	}
	return raw, response.Header, true, nil
}
func repositoryItemURL(value, base, suffix string) (string, bool) {
	if !strings.HasSuffix(value, suffix) {
		return "", false
	}
	canonical, ok := endpoint.GitHubRepository(strings.TrimSuffix(value, suffix))
	if !ok || canonical != base {
		return "", false
	}
	return canonical + suffix, true
}

func repoPath(repo domain.Repository) (string, error) {
	canonical, ok := endpoint.GitHubRepository(repo.BaseURL)
	if !ok || canonical != repo.BaseURL || repo.Provider != "github" || repo.ProjectID < 1 {
		return "", domain.ErrInvalid
	}
	return "/repos/" + strings.TrimPrefix(canonical, "https://github.com/"), nil
}

type repositoryMetadata struct {
	ID      int64  `json:"id"`
	Private *bool  `json:"private"`
	URL     string `json:"html_url"`
	Name    string `json:"name"`
}

func (c *Client) repository(ctx context.Context, token, path string) (repositoryMetadata, error) {
	raw, _, found, err := c.request(ctx, token, path, 2*1024*1024)
	if err != nil {
		return repositoryMetadata{}, err
	}
	if !found {
		return repositoryMetadata{}, &application.ProviderError{Code: "provider_project_not_found"}
	}
	var value repositoryMetadata
	canonical, ok := "", false
	if jsonwire.DecodeExternalObject(raw, &value) == nil {
		canonical, ok = endpoint.GitHubRepository(value.URL)
	}
	if !ok || value.ID < 1 || value.Private == nil || len(value.Name) < 1 || len(value.Name) > 200 {
		return value, invalid()
	}
	value.URL = canonical
	if token == "" && *value.Private {
		return value, &application.ProviderError{Code: "provider_private_repository"}
	}
	return value, nil
}

// LookupRepository resolves only public GitHub identities after directory preauthorization.
func (c *Client) LookupRepository(ctx context.Context, input string) (directory.ResolvedRepository, error) {
	canonical, ok := endpoint.GitHubRepository(input)
	if !ok {
		return directory.ResolvedRepository{}, domain.ErrInvalid
	}
	value, err := c.repository(ctx, "", "/repos/"+strings.TrimPrefix(canonical, "https://github.com/"))
	if err != nil {
		return directory.ResolvedRepository{}, directory.ErrProviderUnavailable
	}
	if value.URL != canonical {
		return directory.ResolvedRepository{}, directory.ErrInvalid
	}
	return directory.ResolvedRepository{Provider: "github", URL: canonical, ID: value.ID, Name: value.Name}, nil
}

// VerifyProject checks immutable numeric identity and current URL before credentials/source acquisition.
func (c *Client) VerifyProject(ctx context.Context, repo domain.Repository, token string) error {
	if _, err := repoPath(repo); err != nil {
		return err
	}
	value, err := c.repository(ctx, token, "/repositories/"+strconv.FormatInt(repo.ProjectID, 10))
	if err != nil {
		return err
	}
	if value.ID != repo.ProjectID || value.URL != repo.BaseURL {
		return invalid()
	}
	return nil
}

// ConfirmCommit checks an exact SHA rather than trusting an agent or webhook assertion.
func (c *Client) ConfirmCommit(ctx context.Context, repo domain.Repository, token, sha string) (bool, error) {
	root, err := repoPath(repo)
	if err != nil || !identifier.ValidCommit(sha) {
		return false, domain.ErrInvalid
	}
	raw, _, found, err := c.request(ctx, token, root+"/commits/"+sha+"?per_page=1", 8*1024*1024)
	if err != nil || !found {
		return false, err
	}
	var result struct {
		SHA string `json:"sha"`
	}
	if jsonwire.DecodeExternalObject(raw, &result) != nil || result.SHA != sha {
		return false, invalid()
	}
	return true, nil
}

// nextPage never follows provider links; it validates their next ordinal and reconstructs requests locally.
func (c *Client) nextPage(header http.Header, path string, page int, repo domain.Repository) (int, error) {
	root, err := repoPath(repo)
	if err != nil {
		return 0, err
	}
	if len(header.Values("Link")) > 1 {
		return 0, invalid()
	}
	next := 0
	for _, part := range strings.Split(header.Get("Link"), ",") {
		bits := strings.Split(strings.TrimSpace(part), ";")
		if len(bits) < 2 {
			continue
		}
		isNext := false
		for _, relation := range bits[1:] {
			if strings.TrimSpace(relation) == `rel="next"` {
				isNext = true
			}
		}
		if !isNext {
			continue
		}
		if next != 0 {
			return 0, invalid()
		}
		text := strings.TrimSpace(bits[0])
		if !strings.HasPrefix(text, "<") || !strings.HasSuffix(text, ">") {
			return 0, invalid()
		}
		u, err := url.Parse(text[1 : len(text)-1])
		expected, _ := url.Parse(c.base + path)
		// GitHub emits equivalent /repositories/{numeric-id} pagination links for /repos/{owner}/{name}.
		// Only this catalog's immutable ID is accepted, and the next request is still reconstructed locally.
		canonicalPath := "/repositories/" + strconv.FormatInt(repo.ProjectID, 10) + strings.TrimPrefix(expected.Path, root)
		if !strings.HasPrefix(expected.Path, root+"/") || err != nil || u.Scheme != expected.Scheme || u.Host != expected.Host || (u.Path != expected.Path && u.Path != canonicalPath) || u.User != nil || u.Fragment != "" || u.RawPath != "" {
			return 0, invalid()
		}
		q, err := url.ParseQuery(u.RawQuery)
		if err != nil || len(q["page"]) != 1 {
			return 0, invalid()
		}
		number, err := strconv.Atoi(q.Get("page"))
		if err != nil || number != page+1 || number > 1000000 {
			return 0, invalid()
		}
		q.Del("page")
		original := expected.Query()
		original.Del("page")
		if q.Encode() != original.Encode() {
			return 0, invalid()
		}
		next = number
	}
	return next, nil
}

// decodeObject preserves strict duplicate/type checks while allowing additive provider fields.
func decodeObject(raw []byte, dst any) error {
	if jsonwire.DecodeExternalObject(raw, dst) != nil {
		return invalid()
	}
	return nil
}
