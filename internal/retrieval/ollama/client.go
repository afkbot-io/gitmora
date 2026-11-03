// Package ollama adapts a fixed local embedding endpoint; it never generates answers.
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"workgraph.local/platform/internal/retrieval/domain"
)

var ErrUnavailable = errors.New("local embedding service unavailable")

// Config is operator-owned. URLs are never supplied by browser/API users.
type Config struct {
	URL, Model, Revision       string
	Dimensions                 int
	QueryPrefix, PassagePrefix string
}
type Client struct {
	config  Config
	profile string
	http    *http.Client
	mu      sync.Mutex
	queries map[string]cachedQuery
}

type cachedQuery struct {
	vector  []float32
	expires time.Time
}

// New rejects credential-bearing URLs and ambient proxies/redirects. The service
// must be on a private operator-controlled network; no cloud key is supported.
func New(c Config) (*Client, error) {
	u, e := url.Parse(c.URL)
	if e != nil || u.Host == "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || u.Scheme != "http" && u.Scheme != "https" || strings.TrimSpace(c.Model) != c.Model || c.Model == "" || len(c.Model) > 200 || !domain.Digest(c.Revision) || c.Dimensions < 1 || c.Dimensions > 2048 || len(c.QueryPrefix) > 200 || len(c.PassagePrefix) > 200 {
		return nil, domain.ErrInvalid
	}
	raw, _ := json.Marshal(struct {
		Version int
		Config  Config
	}{1, c})
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	t.MaxResponseHeaderBytes = 16384
	return &Client{config: c, profile: domain.Hash(raw), http: &http.Client{Transport: t, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, queries: map[string]cachedQuery{}}, nil
}
func (c *Client) Profile() string { return c.profile }

// Embed uses the pinned operator model for a bounded batch and validates every vector.
// Truncation is disabled: an oversized input is retried/diagnosed, never silently indexed incompletely.
func (c *Client) Embed(ctx context.Context, texts []string, query bool) ([][]float32, error) {
	if len(texts) < 1 || len(texts) > 8 || query && len(texts) != 1 {
		return nil, domain.ErrInvalid
	}
	for _, s := range texts {
		if len([]rune(s)) > 2048 || strings.TrimSpace(s) == "" {
			return nil, domain.ErrInvalid
		}
	}
	if err := c.verifyRevision(ctx); err != nil {
		return nil, err
	}
	cacheKey := ""
	if query {
		cacheKey = domain.Hash([]byte(texts[0]))
		c.mu.Lock()
		cached, ok := c.queries[cacheKey]
		c.mu.Unlock()
		if ok && time.Now().Before(cached.expires) {
			return [][]float32{append([]float32(nil), cached.vector...)}, nil
		}
	}
	formatted := make([]string, len(texts))
	prefix := c.config.PassagePrefix
	if query {
		prefix = c.config.QueryPrefix
	}
	for i, text := range texts {
		formatted[i] = prefix + text
	}
	raw, _ := json.Marshal(struct {
		Model     string         `json:"model"`
		Input     []string       `json:"input"`
		Truncate  bool           `json:"truncate"`
		KeepAlive string         `json:"keep_alive"`
		Options   map[string]int `json:"options"`
	}{c.config.Model, formatted, false, "30m", map[string]int{"num_thread": 2}})
	req, e := http.NewRequestWithContext(ctx, "POST", c.config.URL+"/api/embed", bytes.NewReader(raw))
	if e != nil {
		return nil, ErrUnavailable
	}
	req.Header.Set("Content-Type", "application/json")
	r, e := c.http.Do(req)
	if e != nil {
		return nil, ErrUnavailable
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return nil, ErrUnavailable
	}
	raw, e = io.ReadAll(io.LimitReader(r.Body, 512*1024+1))
	if e != nil || len(raw) > 512*1024 {
		return nil, ErrUnavailable
	}
	var v struct {
		Model      string      `json:"model"`
		Embeddings [][]float32 `json:"embeddings"`
	}
	if json.Unmarshal(raw, &v) != nil || v.Model != c.config.Model || len(v.Embeddings) != len(texts) {
		return nil, ErrUnavailable
	}
	for i := range v.Embeddings {
		v.Embeddings[i], e = domain.NormalizeVector(v.Embeddings[i], c.config.Dimensions)
		if e != nil {
			return nil, ErrUnavailable
		}
	}
	if query {
		c.mu.Lock()
		if len(c.queries) >= 256 {
			oldest := ""
			var deadline time.Time
			for key, value := range c.queries {
				if oldest == "" || value.expires.Before(deadline) {
					oldest, deadline = key, value.expires
				}
			}
			delete(c.queries, oldest)
		}
		c.queries[cacheKey] = cachedQuery{append([]float32(nil), v.Embeddings[0]...), time.Now().Add(5 * time.Minute)}
		c.mu.Unlock()
	}
	return v.Embeddings, nil
}

// verifyRevision prevents a moved model tag from mixing incompatible vectors.
func (c *Client) verifyRevision(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.config.URL+"/api/tags", nil)
	if err != nil {
		return ErrUnavailable
	}
	r, err := c.http.Do(req)
	if err != nil {
		return ErrUnavailable
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return ErrUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1024*1024+1))
	if err != nil || len(raw) > 1024*1024 {
		return ErrUnavailable
	}
	var list struct {
		Models []struct{ Name, Digest string }
	}
	if json.Unmarshal(raw, &list) != nil {
		return ErrUnavailable
	}
	for _, m := range list.Models {
		if m.Name == c.config.Model && strings.TrimPrefix(m.Digest, "sha256:") == c.config.Revision {
			return nil
		}
	}
	return ErrUnavailable
}
