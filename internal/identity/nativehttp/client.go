// Package nativehttp implements the native identity port over bounded, exact-destination JSON HTTP.
package nativehttp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"slices"
	"time"

	"workgraph.local/platform/internal/access"
	directory "workgraph.local/platform/internal/directory/domain"
	"workgraph.local/platform/internal/identifier"
	"workgraph.local/platform/internal/identity/domain"
	"workgraph.local/platform/internal/identity/native"
	"workgraph.local/platform/internal/jsonwire"
)

// Client has no cookie jar, ambient proxy, redirect following or remote diagnostic propagation.
type Client struct {
	server string
	http   *http.Client
}

// New canonicalizes a HTTPS/loopback destination; the system trust store still governs TLS verification.
func New(server string) (*Client, error) {
	canonical, e := native.CanonicalServer(server)
	if e != nil {
		return nil, e
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &Client{server: canonical, http: &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// Server identifies the exact credential namespace, including any configured application path.
func (c *Client) Server() string { return c.server }

func (c *Client) request(ctx context.Context, method, path, key string, input, output any, wanted int) error {
	var body io.Reader
	if input != nil {
		raw, e := json.Marshal(input)
		if e != nil || len(raw) > 8192 {
			return native.ErrInvalid
		}
		body = bytes.NewReader(raw)
	}
	if key != "" && access.CredentialHash(key) == "" {
		return native.ErrInvalid
	}
	r, e := http.NewRequestWithContext(ctx, method, c.server+path, body)
	if e != nil {
		return native.ErrInvalid
	}
	r.Header.Set("Accept", "application/json")
	r.Header.Set("User-Agent", "Workgraph/1")
	if input != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
	response, e := c.http.Do(r)
	if e != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return native.ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode != wanted {
		switch response.StatusCode {
		case 401:
			return native.ErrUnauthorized
		case 403:
			return native.ErrForbidden
		case 404:
			return native.ErrEnrollmentGone
		case 409:
			return native.ErrConflict
		case 408, 429:
			return native.ErrUnavailable
		default:
			if response.StatusCode >= 500 {
				return native.ErrUnavailable
			}
			return native.ErrProtocol
		}
	}
	media, _, e := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if e != nil || media != "application/json" {
		return native.ErrProtocol
	}
	raw, e := io.ReadAll(io.LimitReader(response.Body, 32769))
	if e != nil || len(raw) > 32768 || jsonwire.DecodeObject(raw, output) != nil {
		return native.ErrProtocol
	}
	return nil
}

// Prepare sends hash commitments only. The native application validates the public destination and deadline.
func (c *Client) Prepare(ctx context.Context, input domain.CLIRequest) (native.Prepared, error) {
	i, e := input.Normalize()
	if e != nil {
		return native.Prepared{}, native.ErrInvalid
	}
	var v native.Prepared
	e = c.request(ctx, "POST", "/v1/auth/cli/prepare", "", i, &v, 202)
	return v, e
}

func (c *Client) proofs(ctx context.Context, action, id, device, proof string) (domain.CLIView, error) {
	if !identifier.Valid(id) || domain.SecretHash(domain.CLIDevicePrefix, device) == "" || domain.SecretHash(domain.CLIProofPrefix, proof) == "" {
		return domain.CLIView{}, native.ErrInvalid
	}
	input := struct {
		ID     string `json:"id"`
		Device string `json:"device_secret"`
		Proof  string `json:"proof"`
	}{id, device, proof}
	var v domain.CLIView
	e := c.request(ctx, "POST", "/v1/auth/cli/"+action, "", input, &v, 200)
	return v, e
}

// Poll discloses both device proofs only in a bounded request body at the original destination.
func (c *Client) Poll(ctx context.Context, id, device, proof string) (domain.CLIView, error) {
	return c.proofs(ctx, "poll", id, device, proof)
}

// Cancel closes a pending flow; an approval race is an explicit conflict requiring bearer-authenticated revocation.
func (c *Client) Cancel(ctx context.Context, id, device, proof string) (domain.CLIView, error) {
	return c.proofs(ctx, "cancel", id, device, proof)
}

// Me verifies the exact candidate through the ordinary authenticated account endpoint.
func (c *Client) Me(ctx context.Context, key string) (native.Account, error) {
	if access.CredentialHash(key) == "" {
		return native.Account{}, native.ErrInvalid
	}
	var v directory.Me
	if e := c.request(ctx, "GET", "/v1/me", key, nil, &v, 200); e != nil {
		return native.Account{}, e
	}
	a := native.Account{UserID: v.User.ID, TokenID: v.TokenID, DisplayName: v.User.DisplayName, Scopes: native.CoreScopes(v.Scopes), ExpiresAt: v.ExpiresAt}
	slices.Sort(a.Scopes)
	if !v.User.Enabled || v.User.DeletedAt != nil || a.Validate() != nil || len(slices.Compact(slices.Clone(a.Scopes))) != len(a.Scopes) {
		return native.Account{}, native.ErrProtocol
	}
	return a, nil
}

// Revoke uses the existing self-owned token command and verifies its exact immutable receipt.
func (c *Client) Revoke(ctx context.Context, key, token, request string) error {
	if access.CredentialHash(key) == "" || !identifier.Valid(token) || !identifier.Valid(request) {
		return native.ErrInvalid
	}
	input := struct {
		RequestID string `json:"request_id"`
	}{request}
	var v directory.Receipt
	if e := c.request(ctx, "POST", "/v1/me/tokens/"+token+"/revoke", key, input, &v, 200); e != nil {
		return e
	}
	if v.RequestID != request || v.EntityID != token || v.Kind != "token" || v.Action != directory.RevokeToken || !v.Deleted || !identifier.Valid(v.ActorID) || v.Version <= 0 || v.Sequence <= 0 || v.At.IsZero() {
		return native.ErrProtocol
	}
	return nil
}

var _ native.Remote = (*Client)(nil)
