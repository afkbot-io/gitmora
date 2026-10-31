// Package httpclient delivers sanitized revisions over a bounded redirect-free HTTP connection.
package httpclient

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"time"

	"workgraph.local/platform/internal/access"
	capture "workgraph.local/platform/internal/capture/domain"
	"workgraph.local/platform/internal/collector/application"
	"workgraph.local/platform/internal/collector/domain"
	"workgraph.local/platform/internal/jsonwire"
)

// TokenSource supplies a credential at send time; values never enter persisted collector config.
type TokenSource func(context.Context) (string, error)

// Client only acknowledges the exact repository/session/revision/digest tuple it sent.
type Client struct {
	config domain.Config
	token  TokenSource
	http   *http.Client
}

// New validates the endpoint and disables every HTTP redirect, including same-host redirects.
func New(config domain.Config, token TokenSource) (*Client, error) {
	normalized, err := domain.NormalizeConfig(config)
	if err != nil || token == nil {
		return nil, domain.ErrInvalid
	}
	return &Client{config: normalized, token: token, http: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// Send uploads immutable canonical evidence and discards unsafe remote diagnostic text.
func (c *Client) Send(ctx context.Context, session capture.Session) error {
	snapshot := session.Snapshot()
	if snapshot.RepositoryID != c.config.RepositoryID || session.Digest() == "" {
		return failed("invalid_capture", false)
	}
	secret, err := c.token(ctx)
	if err != nil || access.CredentialHash(secret) == "" {
		return failed("authentication_required", true)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.config.ServerURL+"/v1/captures", bytes.NewReader(session.CanonicalJSON()))
	if err != nil {
		return failed("invalid_request", false)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+secret)
	request.Header.Set("User-Agent", "Workgraph/1")
	response, err := c.http.Do(request)
	if err != nil {
		return failed("unavailable", true)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 && response.StatusCode != 201 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		switch response.StatusCode {
		case 401:
			return failed("authentication_required", true)
		case 403:
			return failed("forbidden", true)
		case 409:
			return failed("conflict", false)
		case 400, 404, 405, 413, 415, 422:
			return failed("invalid_request", false)
		}
		return failed("http_error", response.StatusCode == 408 || response.StatusCode == 429 || response.StatusCode >= 500)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 16*1024+1))
	if err != nil || len(raw) > 16*1024 {
		return failed("invalid_ack", true)
	}
	var ack struct {
		RepositoryID string `json:"repository_id"`
		SessionID    string `json:"session_id"`
		Revision     int64  `json:"revision"`
		Digest       string `json:"digest"`
		Created      *bool  `json:"created"`
	}
	if jsonwire.DecodeObject(raw, &ack) != nil || ack.RepositoryID != snapshot.RepositoryID || ack.SessionID != snapshot.SessionID || ack.Revision != snapshot.Revision || ack.Digest != session.Digest() || ack.Created == nil || *ack.Created != (response.StatusCode == 201) {
		return failed("invalid_ack", true)
	}
	return nil
}

func failed(code string, retry bool) error {
	return &application.DeliveryError{Code: code, Retryable: retry}
}
