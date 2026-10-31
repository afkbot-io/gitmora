// Package gitlab implements credential-safe requests to explicitly allowed GitLab API bases.
package gitlab

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	capture "workgraph.local/platform/internal/capture/domain"
	"workgraph.local/platform/internal/endpoint"
	"workgraph.local/platform/internal/forge/application"
	"workgraph.local/platform/internal/forge/domain"
	"workgraph.local/platform/internal/jsonwire"
	"workgraph.local/platform/internal/platform/netroute"
)

// Client follows no redirects and sends credentials only to operator-allowlisted API bases.
type Client struct {
	allowed map[string]bool
	http    *http.Client
}

// NewWithProxy adds an explicit private CONNECT hop; original GitLab TLS identity and exact base policy remain enforced.
// An empty proxy preserves direct private-installation compatibility and never enables ambient proxy variables.
func NewWithProxy(bases []string, proxy string) (*Client, error) {
	client := &Client{allowed: map[string]bool{}}
	for _, base := range bases {
		normalized, err := normalizeBase(base)
		if err != nil {
			return nil, err
		}
		if proxy != "" && !strings.HasPrefix(normalized, "https://") {
			return nil, domain.ErrInvalid
		}
		client.allowed[normalized] = true
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.MaxResponseHeaderBytes = 64 * 1024
	if proxy != "" {
		endpoint, e := url.Parse(proxy)
		if e != nil || len(proxy) > 256 || endpoint.Scheme != "http" || endpoint.Host == "" || endpoint.User != nil || endpoint.Path != "" || endpoint.RawPath != "" || endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" || endpoint.Opaque != "" {
			return nil, domain.ErrInvalid
		}
		dial, e := netroute.NewBoundDialer(endpoint.Host, netroute.Private, nil, nil)
		if e != nil {
			return nil, domain.ErrInvalid
		}
		transport.Proxy = http.ProxyURL(endpoint)
		transport.DialContext = dial.DialContext
		transport.DialTLS = nil
		transport.DialTLSContext = nil
		transport.ProxyConnectHeader = nil
		transport.GetProxyConnectHeader = nil
	}
	client.http = &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return client, nil
}
func normalizeBase(base string) (string, error) {
	normalized, ok := endpoint.GitLabBase(base)
	if !ok {
		return "", domain.ErrInvalid
	}
	return normalized, nil
}
func (c *Client) get(ctx context.Context, repo domain.Repository, token, suffix string) (map[string]json.RawMessage, bool, error) {
	raw, _, found, err := c.request(ctx, repo, token, suffix)
	if err != nil || !found {
		return nil, found, err
	}
	var body map[string]json.RawMessage
	if jsonwire.DecodeExternalObject(raw, &body) != nil {
		return nil, false, invalidResponse()
	}
	return body, true, nil
}
func invalidResponse() error {
	return &application.ProviderError{Code: "provider_invalid_response", Retryable: true}
}
func (c *Client) request(ctx context.Context, repo domain.Repository, token, suffix string) ([]byte, http.Header, bool, error) {
	return c.requestBounded(ctx, repo, token, http.MethodGet, suffix, 2*1024*1024)
}

func (c *Client) requestBounded(ctx context.Context, repo domain.Repository, token, method, suffix string, limit int64) ([]byte, http.Header, bool, error) {
	base, err := normalizeBase(repo.BaseURL)
	if err != nil || repo.ProjectID < 1 || len(token) < 8 || len(token) > 4096 || strings.ContainsAny(token, "\x00\r\n") {
		return nil, nil, false, domain.ErrInvalid
	}
	if !c.allowed[base] {
		return nil, nil, false, &application.ProviderError{Code: "provider_endpoint_not_allowed"}
	}
	request, err := http.NewRequestWithContext(ctx, method, base+"/api/v4/projects/"+strconv.FormatInt(repo.ProjectID, 10)+suffix, nil)
	if err != nil {
		return nil, nil, false, domain.ErrInvalid
	}
	request.Header.Set("PRIVATE-TOKEN", token)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "Workgraph/1")
	response, err := c.http.Do(request)
	if err != nil {
		return nil, nil, false, &application.ProviderError{Code: "provider_unavailable", Retryable: true}
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusNotFound:
		return nil, nil, false, nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, nil, false, &application.ProviderError{Code: "provider_unauthorized"}
	case http.StatusTooManyRequests:
		delay := time.Minute
		if seconds, err := strconv.Atoi(response.Header.Get("Retry-After")); err == nil && seconds >= 1 && seconds <= 3600 {
			delay = time.Duration(seconds) * time.Second
		}
		return nil, nil, false, &application.ProviderError{Code: "provider_rate_limited", Retryable: true, RetryAfter: delay}
	case http.StatusOK:
	default:
		return nil, nil, false, &application.ProviderError{Code: "provider_unavailable", Retryable: response.StatusCode >= 500 || response.StatusCode == 408}
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return nil, nil, false, invalidResponse()
	}
	return raw, response.Header, true, nil
}

// VerifyProject verifies both token access and the exact immutable provider project identity.
func (c *Client) VerifyProject(ctx context.Context, repo domain.Repository, token string) error {
	body, found, err := c.get(ctx, repo, token, "")
	if err != nil {
		return err
	}
	if !found {
		return &application.ProviderError{Code: "provider_project_not_found"}
	}
	var id int64
	if json.Unmarshal(body["id"], &id) != nil || id != repo.ProjectID {
		return &application.ProviderError{Code: "provider_invalid_response", Retryable: true}
	}
	return nil
}

// ConfirmCommit verifies an exact object in the configured project; a 404 remains pending.
func (c *Client) ConfirmCommit(ctx context.Context, repo domain.Repository, token, sha string) (bool, error) {
	if !capture.ValidCommit(sha) {
		return false, domain.ErrInvalid
	}
	body, found, err := c.get(ctx, repo, token, "/repository/commits/"+sha+"?stats=false")
	if err != nil || !found {
		return false, err
	}
	var id string
	if json.Unmarshal(body["id"], &id) != nil || id != sha {
		return false, &application.ProviderError{Code: "provider_invalid_response", Retryable: true}
	}
	return true, nil
}
