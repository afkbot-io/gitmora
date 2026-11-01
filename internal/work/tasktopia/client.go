// Package tasktopia adapts the local Tasktopia MCP contract using the official MCP Go SDK.
package tasktopia

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"workgraph.local/platform/internal/identifier"
	"workgraph.local/platform/internal/jsonwire"
	"workgraph.local/platform/internal/platform/netroute"
	"workgraph.local/platform/internal/work/application"
	"workgraph.local/platform/internal/work/domain"
)

const responseLimit = 4 * 1024 * 1024

// Client sends a service credential only to an exact operator-allowlisted MCP endpoint.
type Client struct {
	allowed   map[string]bool
	transport *http.Transport
}

// New permits HTTPS endpoints and loopback-only HTTP for isolated local tests.
func New(endpoints []string) (*Client, error) {
	return NewWithProxy(endpoints, "")
}

// NewWithProxy confines optional external routing to an explicit private CONNECT proxy.
// TLS and the exact endpoint check remain end-to-end; ambient proxy settings are ignored.
func NewWithProxy(endpoints []string, proxy string) (*Client, error) {
	c := &Client{allowed: map[string]bool{}}
	for _, e := range endpoints {
		if !validEndpoint(e) {
			return nil, domain.ErrInvalid
		}
		if proxy != "" && !strings.HasPrefix(e, "https://") {
			return nil, domain.ErrInvalid
		}
		c.allowed[e] = true
	}
	c.transport = http.DefaultTransport.(*http.Transport).Clone()
	c.transport.Proxy = nil
	c.transport.MaxResponseHeaderBytes = 64 * 1024
	if proxy != "" {
		u, err := url.Parse(proxy)
		if err != nil || len(proxy) > 256 || u.Scheme != "http" || u.Host == "" || u.User != nil || u.Path != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
			return nil, domain.ErrInvalid
		}
		dial, err := netroute.NewBoundDialer(u.Host, netroute.Private, nil, nil)
		if err != nil {
			return nil, domain.ErrInvalid
		}
		c.transport.Proxy = http.ProxyURL(u)
		c.transport.DialContext = dial.DialContext
		c.transport.DialTLS = nil
		c.transport.DialTLSContext = nil
		c.transport.ProxyConnectHeader = nil
		c.transport.GetProxyConnectHeader = nil
	}
	return c, nil
}
func validEndpoint(e string) bool {
	u, err := url.Parse(e)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || u.Opaque != "" || !strings.HasSuffix(u.Path, "/mcp") || path.Clean(u.Path) != u.Path {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	return u.Scheme == "https" || (u.Scheme == "http" && (u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())))
}
func failure(code string, retry bool) error {
	return &application.ProviderError{Code: code, Retryable: retry}
}

type authTransport struct {
	base            http.RoundTripper
	endpoint, token string
	mu              sync.Mutex
	last            error
}

func (t *authTransport) record(e error) error { t.mu.Lock(); defer t.mu.Unlock(); t.last = e; return e }
func (t *authTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.String() != t.endpoint {
		return nil, t.record(failure("provider_endpoint_not_allowed", false))
	}
	clone := r.Clone(r.Context())
	clone.Header = r.Header.Clone()
	clone.Header.Set("Authorization", "Bearer "+t.token)
	clone.Header.Set("User-Agent", "Workgraph/1")
	response, err := t.base.RoundTrip(clone)
	if err != nil {
		return nil, t.record(failure("provider_unavailable", true))
	}
	if response.StatusCode == 401 || response.StatusCode == 403 {
		response.Body.Close()
		return nil, t.record(failure("provider_unauthorized", false))
	}
	if response.StatusCode == 429 {
		response.Body.Close()
		delay := time.Minute
		if sec, e := strconv.Atoi(response.Header.Get("Retry-After")); e == nil && sec > 0 && sec <= 3600 {
			delay = time.Duration(sec) * time.Second
		}
		return nil, t.record(&application.ProviderError{Code: "provider_rate_limited", Retryable: true, RetryAfter: delay})
	}
	if response.StatusCode >= 300 && response.StatusCode != 404 && response.StatusCode != 405 {
		response.Body.Close()
		return nil, t.record(failure("provider_unavailable", response.StatusCode >= 500 || response.StatusCode == 408))
	}
	// The SDK also bounds individual SSE events. Bounding the complete response prevents
	// oversized JSON and unbounded sequences from accumulating on this request-only client.
	response.Body = &boundedBody{ReadCloser: response.Body, remaining: responseLimit + 1}
	return response, nil
}

type boundedBody struct {
	io.ReadCloser
	remaining int
}

func (b *boundedBody) Read(p []byte) (int, error) {
	if b.remaining <= 0 {
		return 0, errors.New("MCP response limit exceeded")
	}
	if len(p) > b.remaining {
		p = p[:b.remaining]
	}
	n, e := b.ReadCloser.Read(p)
	b.remaining -= n
	return n, e
}

func (c *Client) call(parent context.Context, config domain.Config, name string, args any) (json.RawMessage, error) {
	if config.Validate() != nil {
		return nil, domain.ErrInvalid
	}
	if !c.allowed[config.Endpoint] {
		return nil, failure("provider_endpoint_not_allowed", false)
	}
	ctx, cancel := context.WithTimeout(parent, 12*time.Second)
	defer cancel()
	transport := &authTransport{base: c.transport, endpoint: config.Endpoint, token: config.Token}
	httpClient := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	client := mcp.NewClient(&mcp.Implementation{Name: "Workgraph", Version: "1"}, &mcp.ClientOptions{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: config.Endpoint, HTTPClient: httpClient, MaxRetries: -1, DisableStandaloneSSE: true, MaxEventSize: responseLimit}, nil)
	if err == nil {
		defer session.Close()
	}
	var result *mcp.CallToolResult
	if err == nil {
		result, err = session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	}
	if err != nil {
		transport.mu.Lock()
		last := transport.last
		transport.mu.Unlock()
		if last != nil {
			return nil, last
		}
		return nil, failure("provider_unavailable", true)
	}
	if result.IsError {
		for _, content := range result.Content {
			if value, ok := content.(*mcp.TextContent); ok {
				var data struct {
					Code string `json:"code"`
				}
				if jsonwire.DecodeExternalObject([]byte(value.Text), &data) == nil {
					switch data.Code {
					case "NOT_FOUND", "TASK_NOT_FOUND":
						return nil, failure("task_not_found", true)
					case "COUNTRY_ACCESS_DENIED":
						return nil, failure("country_access_denied", false)
					case "FORBIDDEN_SCOPE", "MCP_SCOPE_DENIED", "FORBIDDEN", "UNAUTHORIZED":
						return nil, failure("provider_unauthorized", false)
					case "CONFLICT":
						return nil, failure("publication_conflict", false)
					}
				}
			}
		}
		return nil, failure("provider_tool_error", false)
	}
	if result.StructuredContent == nil {
		return nil, failure("provider_invalid_response", true)
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil || len(raw) > responseLimit {
		return nil, failure("provider_invalid_response", true)
	}
	var envelope struct {
		Result json.RawMessage `json:"result"`
	}
	if jsonwire.DecodeExternalObject(raw, &envelope) != nil || len(envelope.Result) == 0 || string(envelope.Result) == "null" {
		return nil, failure("provider_invalid_response", true)
	}
	return envelope.Result, nil
}

// Verify checks that the service identity can see the configured country, without exporting its world.
func (c *Client) Verify(ctx context.Context, config domain.Config) error {
	raw, err := c.call(ctx, config, "country.list", struct{}{})
	if err != nil {
		return err
	}
	var countries []struct {
		ID string `json:"id"`
	}
	if jsonwire.DecodeExternalArray(raw, &countries) != nil || len(countries) > 10000 {
		return failure("provider_invalid_response", true)
	}
	for _, country := range countries {
		if country.ID == config.CountryID {
			return nil
		}
	}
	return failure("country_access_denied", false)
}

// Task retrieves only an explicitly identified task and validates the returned human URL scope.
func (c *Client) Task(ctx context.Context, config domain.Config, id string) (domain.Task, error) {
	if !identifier.Valid(id) {
		return domain.Task{}, domain.ErrInvalid
	}
	raw, err := c.call(ctx, config, "task.get", map[string]string{"countryId": config.CountryID, "taskId": id})
	if err != nil {
		return domain.Task{}, err
	}
	var task domain.Task
	if jsonwire.DecodeExternalObject(raw, &task) != nil || task.ID != id {
		return domain.Task{}, failure("provider_invalid_response", true)
	}
	endpoint, _ := url.Parse(config.Endpoint)
	human, e := url.Parse(task.URL)
	if e != nil || human.Host != endpoint.Host || human.Scheme != endpoint.Scheme || human.Query().Get("countryId") != config.CountryID || human.Query().Get("taskId") != id {
		return domain.Task{}, failure("provider_invalid_response", true)
	}
	task.CountryID = config.CountryID
	task, err = domain.NormalizeTask(task)
	if err != nil {
		return domain.Task{}, failure("provider_invalid_response", true)
	}
	return task, nil
}

// Publish retains the same external idempotency identity and exact payload across uncertain retries.
func (c *Client) Publish(ctx context.Context, config domain.Config, task, id string, doc domain.EvidenceDocument) error {
	if !identifier.Valid(task) || !identifier.Valid(id) || doc.FileName != "workgraph-evidence-"+id+".md" || len(doc.Content) > 64000 || doc.Title != "Workgraph evidence" {
		return domain.ErrInvalid
	}
	raw, err := c.call(ctx, config, "task.document_upsert", map[string]string{"countryId": config.CountryID, "taskId": task, "fileName": doc.FileName, "title": doc.Title, "content": doc.Content, "idempotencyKey": "workgraph:publication:" + id})
	if err != nil {
		return err
	}
	var ack struct {
		TaskID   string `json:"taskId"`
		FileName string `json:"fileName"`
		Content  string `json:"content"`
	}
	if jsonwire.DecodeExternalObject(raw, &ack) != nil || ack.TaskID != task || ack.FileName != doc.FileName || ack.Content != doc.Content {
		return failure("provider_invalid_response", true)
	}
	return nil
}

// AddLink sends one stable country/task/URL operation; the caller also reads back current task state.
func (c *Client) AddLink(ctx context.Context, config domain.Config, task, id string, link domain.MRLink) error {
	if !identifier.Valid(task) || !identifier.Valid(id) || link.Validate() != nil {
		return domain.ErrInvalid
	}
	raw, err := c.call(ctx, config, "task.link_add", map[string]string{"countryId": config.CountryID, "taskId": task, "url": link.URL, "title": link.Title, "idempotencyKey": "workgraph:mr-link:" + id})
	if err != nil {
		return err
	}
	var ack struct {
		Links []domain.Link `json:"mergeRequests"`
		URL   string        `json:"url"`
	}
	if jsonwire.DecodeExternalObject(raw, &ack) != nil || len(ack.Links) > 256 {
		return failure("provider_invalid_response", true)
	}
	u, e := url.Parse(ack.URL)
	endpoint, _ := url.Parse(config.Endpoint)
	if e != nil || u.Scheme != endpoint.Scheme || u.Host != endpoint.Host || u.Query().Get("countryId") != config.CountryID || u.Query().Get("taskId") != task || !(domain.Task{Links: ack.Links}).HasLink(link.URL) {
		return failure("provider_invalid_response", true)
	}
	return nil
}
