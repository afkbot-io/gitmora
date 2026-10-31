// Package nativeapi implements bounded JSON API access for an already authenticated native client.
package nativeapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/identity/native"
	"workgraph.local/platform/internal/jsonwire"
)

var ErrInvalid = errors.New("invalid native API request")
var ErrUnavailable = errors.New("Gitmora API unavailable")
var ErrProtocol = errors.New("invalid Gitmora API response")

type ResponseError struct{ Status int }

func (e ResponseError) Error() string {
	return fmt.Sprintf("Gitmora rejected the request (HTTP %d)", e.Status)
}

type Response struct {
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body,omitempty"`
}
type Client struct {
	server string
	http   *http.Client
}

func New(server string) (*Client, error) {
	canonical, e := native.CanonicalServer(server)
	if e != nil {
		return nil, ErrInvalid
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.MaxResponseHeaderBytes = 65536
	return &Client{canonical, &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func Validate(method, route string, body []byte) error {
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE":
	default:
		return ErrInvalid
	}
	u, e := url.Parse(route)
	if e != nil || len(route) > 8192 || u.IsAbs() || u.Host != "" || u.User != nil || u.Fragment != "" || u.Opaque != "" || u.RawPath != "" || !strings.HasPrefix(u.Path, "/v1/") || path.Clean(u.Path) != u.Path || strings.ContainsAny(u.Path, "\\\x00\r\n") {
		return ErrInvalid
	}
	if len(body) > 3*1024*1024 || method == "GET" && len(body) > 0 {
		return ErrInvalid
	}
	if len(body) > 0 {
		var object map[string]json.RawMessage
		if jsonwire.DecodeExternalObject(body, &object) != nil {
			return ErrInvalid
		}
	}
	return nil
}

// Do sends one exact intent with no automatic mutation retry, ambient proxy, cookies or credential-bearing redirect.
func (c *Client) Do(ctx context.Context, method, route string, body []byte, key string) (Response, error) {
	if Validate(method, route, body) != nil || access.CredentialHash(key) == "" {
		return Response{}, ErrInvalid
	}
	request, e := http.NewRequestWithContext(ctx, method, c.server+route, bytes.NewReader(body))
	if e != nil {
		return Response{}, ErrInvalid
	}
	request.Header.Set("Authorization", "Bearer "+key)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "Gitmora/1")
	if len(body) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	response, e := c.http.Do(request)
	if e != nil {
		if ctx.Err() != nil {
			return Response{}, ctx.Err()
		}
		return Response{}, ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Response{}, ResponseError{response.StatusCode}
	}
	if response.StatusCode == 204 {
		return Response{Status: 204}, nil
	}
	kind, _, e := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if e != nil || kind != "application/json" {
		return Response{}, ErrProtocol
	}
	raw, e := io.ReadAll(io.LimitReader(response.Body, 8*1024*1024+1))
	if e != nil || len(raw) > 8*1024*1024 {
		if ctx.Err() != nil {
			return Response{}, ctx.Err()
		}
		return Response{}, ErrProtocol
	}
	var object map[string]json.RawMessage
	if jsonwire.DecodeExternalObject(raw, &object) != nil {
		return Response{}, ErrProtocol
	}
	return Response{response.StatusCode, json.RawMessage(raw)}, nil
}
