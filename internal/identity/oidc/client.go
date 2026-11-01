// Package oidc adapts a single operator-selected local issuer using standard OAuth/OIDC verification.
package oidc

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	core "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
	"workgraph.local/platform/internal/identity/domain"
	"workgraph.local/platform/internal/jsonwire"
)

type Client struct {
	config   domain.Config
	secret   string
	http     *http.Client
	gate     chan struct{}
	provider *core.Provider
	oauth    oauth2.Config
	until    time.Time
}

// New performs no provider I/O: bearer/cookie operations remain available during an IdP outage.
func New(config domain.Config, secret string) (*Client, error) {
	config, e := domain.NormalizeConfig(config)
	if e != nil || len(secret) < 1 || len(secret) > 4096 || strings.ContainsAny(secret, "\x00\r\n") {
		return nil, domain.ErrInvalid
	}
	issuer, _ := domain.URL(config.Issuer)
	transport := &http.Transport{Proxy: nil, ForceAttemptHTTP2: true, MaxIdleConns: 8, MaxIdleConnsPerHost: 4, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 5 * time.Second, IdleConnTimeout: 30 * time.Second}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, e := net.SplitHostPort(address)
		if e != nil || !strings.EqualFold(host, issuer.Hostname()) {
			return nil, domain.ErrProvider
		}
		ips, e := net.DefaultResolver.LookupIPAddr(ctx, host)
		if e != nil || len(ips) == 0 {
			return nil, domain.ErrProvider
		}
		for _, ip := range ips {
			if !ip.IP.IsPrivate() && !ip.IP.IsLoopback() {
				return nil, domain.ErrProvider
			}
		}
		dial := net.Dialer{Timeout: 5 * time.Second}
		for _, ip := range ips {
			connection, e := dial.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
			if e == nil {
				return connection, nil
			}
		}
		return nil, domain.ErrProvider
	}
	bounded := &localTransport{base: transport, origin: domain.Origin(issuer)}
	return &Client{config: config, secret: secret, gate: make(chan struct{}, 1), http: &http.Client{Transport: bounded, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

type localTransport struct {
	base   http.RoundTripper
	origin string
}

func (t *localTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if domain.Origin(r.URL) != t.origin || r.URL.User != nil || (r.Method != "GET" && r.Method != "POST") {
		return nil, domain.ErrProvider
	}
	response, e := t.base.RoundTrip(r)
	if e != nil {
		return nil, domain.ErrProvider
	}
	defer response.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(response.Body, 128*1024+1))
	if e != nil || len(raw) > 128*1024 {
		return nil, domain.ErrProvider
	}
	if response.StatusCode >= 300 && response.StatusCode < 400 || response.StatusCode >= 500 {
		return nil, domain.ErrProvider
	}
	if response.StatusCode != 200 {
		raw = []byte(`{"error":"provider_request_failed"}`)
	} else {
		var object map[string]json.RawMessage
		if jsonwire.DecodeExternalObject(raw, &object) != nil {
			return nil, domain.ErrProvider
		}
	}
	response.Body = io.NopCloser(bytes.NewReader(raw))
	response.ContentLength = int64(len(raw))
	return response, nil
}

func (c *Client) metadata(ctx context.Context) (oauth2.Config, *core.Provider, error) {
	select {
	case c.gate <- struct{}{}:
		defer func() { <-c.gate }()
	case <-ctx.Done():
		return oauth2.Config{}, nil, domain.ErrProvider
	}
	if c.provider != nil && time.Now().Before(c.until) {
		return c.oauth, c.provider, nil
	}
	request, e := http.NewRequestWithContext(ctx, "GET", strings.TrimSuffix(c.config.Issuer, "/")+"/.well-known/openid-configuration", nil)
	if e != nil {
		return oauth2.Config{}, nil, domain.ErrProvider
	}
	response, e := c.http.Do(request)
	if e != nil {
		return oauth2.Config{}, nil, domain.ErrProvider
	}
	defer response.Body.Close()
	raw, e := io.ReadAll(response.Body)
	if e != nil || response.StatusCode != 200 {
		return oauth2.Config{}, nil, domain.ErrProvider
	}
	var fields map[string]json.RawMessage
	if jsonwire.DecodeExternalObject(raw, &fields) != nil {
		return oauth2.Config{}, nil, domain.ErrProvider
	}
	var metadata core.ProviderConfig
	for _, field := range []struct {
		name   string
		target *string
	}{{"issuer", &metadata.IssuerURL}, {"authorization_endpoint", &metadata.AuthURL}, {"token_endpoint", &metadata.TokenURL}, {"jwks_uri", &metadata.JWKSURL}} {
		if json.Unmarshal(fields[field.name], field.target) != nil {
			return oauth2.Config{}, nil, domain.ErrProvider
		}
	}
	if metadata.IssuerURL != c.config.Issuer {
		return oauth2.Config{}, nil, domain.ErrProvider
	}
	issuer, _ := domain.URL(c.config.Issuer)
	for _, value := range []string{metadata.AuthURL, metadata.TokenURL, metadata.JWKSURL} {
		u, e := domain.URL(value)
		if e != nil || domain.Origin(u) != domain.Origin(issuer) {
			return oauth2.Config{}, nil, domain.ErrProvider
		}
	}
	metadata.Algorithms = []string{core.RS256}
	provider := metadata.NewProvider(core.ClientContext(context.Background(), c.http))
	style := oauth2.AuthStyleInHeader
	if c.config.AuthStyle == "post" {
		style = oauth2.AuthStyleInParams
	}
	oauth := oauth2.Config{ClientID: c.config.ClientID, ClientSecret: c.secret, RedirectURL: c.config.Callback(), Scopes: []string{core.ScopeOpenID, "profile"}, Endpoint: oauth2.Endpoint{AuthURL: metadata.AuthURL, TokenURL: metadata.TokenURL, AuthStyle: style}}
	c.provider = provider
	c.oauth = oauth
	c.until = time.Now().Add(5 * time.Minute)
	return oauth, provider, nil
}

func (c *Client) AuthorizationURL(ctx context.Context, state, verifier, nonce string) (string, error) {
	oauth, _, e := c.metadata(ctx)
	if e != nil {
		return "", e
	}
	return oauth.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), core.Nonce(nonce), oauth2.SetAuthURLParam("response_mode", "query")), nil
}

// Exchange never retains provider access/refresh/ID tokens or maps claims into local authorization.
func (c *Client) Exchange(ctx context.Context, code, verifier, nonce string) (domain.Identity, error) {
	if len(code) < 1 || len(code) > 2048 || strings.ContainsAny(code, "\x00\r\n") {
		return domain.Identity{}, domain.ErrLogin
	}
	oauth, provider, e := c.metadata(ctx)
	if e != nil {
		return domain.Identity{}, e
	}
	token, e := oauth.Exchange(context.WithValue(ctx, oauth2.HTTPClient, c.http), code, oauth2.VerifierOption(verifier))
	if e != nil {
		if errors.Is(e, domain.ErrProvider) || errors.Is(e, context.DeadlineExceeded) || errors.Is(e, context.Canceled) {
			return domain.Identity{}, domain.ErrProvider
		}
		var failure *oauth2.RetrieveError
		if errors.As(e, &failure) && failure.Response != nil && (failure.Response.StatusCode == 401 || failure.Response.StatusCode >= 500) {
			return domain.Identity{}, domain.ErrProvider
		}
		return domain.Identity{}, domain.ErrLogin
	}
	raw, ok := token.Extra("id_token").(string)
	if !ok || len(raw) > 32768 || token.AccessToken == "" || !strings.EqualFold(token.TokenType, "Bearer") {
		return domain.Identity{}, domain.ErrLogin
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return domain.Identity{}, domain.ErrLogin
	}
	claimsRaw, e := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil {
		return domain.Identity{}, domain.ErrLogin
	}
	var fields map[string]json.RawMessage
	if jsonwire.DecodeExternalObject(claimsRaw, &fields) != nil {
		return domain.Identity{}, domain.ErrLogin
	}
	for _, name := range []string{"iss", "sub", "aud", "iat", "exp", "nonce"} {
		if _, ok := fields[name]; !ok {
			return domain.Identity{}, domain.ErrLogin
		}
	}
	for key := range fields {
		lower := strings.ToLower(key)
		if key != lower && (lower == "iss" || lower == "sub" || lower == "aud" || lower == "iat" || lower == "exp" || lower == "nonce" || lower == "azp") {
			return domain.Identity{}, domain.ErrLogin
		}
	}
	verified, e := provider.Verifier(&core.Config{ClientID: c.config.ClientID, SupportedSigningAlgs: []string{core.RS256}}).Verify(ctx, raw)
	if e != nil || verified.Issuer != c.config.Issuer || !domain.Subject(verified.Subject) || verified.IssuedAt.IsZero() || verified.IssuedAt.After(time.Now().Add(time.Minute)) || subtle.ConstantTimeCompare([]byte(verified.Nonce), []byte(nonce)) != 1 {
		return domain.Identity{}, domain.ErrLogin
	}
	if raw, ok := fields["nbf"]; ok {
		var nbf int64
		if json.Unmarshal(raw, &nbf) != nil || time.Unix(nbf, 0).After(time.Now().Add(time.Minute)) {
			return domain.Identity{}, domain.ErrLogin
		}
	}
	var azp string
	if field, exists := fields["azp"]; exists {
		if json.Unmarshal(field, &azp) != nil || azp != c.config.ClientID {
			return domain.Identity{}, domain.ErrLogin
		}
	} else if len(verified.Audience) > 1 {
		return domain.Identity{}, domain.ErrLogin
	}
	if verified.AccessTokenHash != "" && verified.VerifyAccessToken(token.AccessToken) != nil {
		return domain.Identity{}, domain.ErrLogin
	}
	var profile struct {
		Name     string `json:"name"`
		Username string `json:"preferred_username"`
	}
	if verified.Claims(&profile) != nil {
		return domain.Identity{}, domain.ErrLogin
	}
	name := strings.TrimSpace(profile.Name)
	if name == "" {
		name = strings.TrimSpace(profile.Username)
	}
	if name == "" {
		name = verified.Subject
	}
	return domain.Identity{Issuer: verified.Issuer, Subject: verified.Subject, DisplayName: name, Username: profile.Username}, nil
}
