// Package egress provides a bounded credential-free CONNECT channel to one operator-selected TLS endpoint.
package egress

import (
	"context"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"workgraph.local/platform/internal/platform/netroute"
)

// Options bounds concurrent sockets and total tunnel lifetime independently of client behavior.
type Options struct {
	MaxTunnels  int
	MaxLifetime time.Duration
}
type tunnel struct{ client, origin net.Conn }

// Handler sees only CONNECT metadata and opaque TLS records; the caller still verifies the provider certificate.
type Handler struct {
	ctx     context.Context
	cancel  context.CancelFunc
	route   *netroute.BoundDialer
	options Options
	slots   chan struct{}
	mu      sync.Mutex
	closed  bool
	active  map[*tunnel]struct{}
	wait    sync.WaitGroup
}

// New constructs one immutable checked host/port route, without database, provider credentials or dynamic destinations.
func New(parent context.Context, route *netroute.BoundDialer, options Options) (*Handler, error) {
	if route == nil || options.MaxTunnels < 1 || options.MaxTunnels > 128 || options.MaxLifetime < time.Second || options.MaxLifetime > 2*time.Minute {
		return nil, netroute.ErrInvalid
	}
	ctx, cancel := context.WithCancel(parent)
	return &Handler{ctx: ctx, cancel: cancel, route: route, options: options, slots: make(chan struct{}, options.MaxTunnels), active: map[*tunnel]struct{}{}}, nil
}

// Close fences new tunnels, interrupts pending routes and closes/waits for every hijacked socket.
func (h *Handler) Close() {
	h.cancel()
	h.mu.Lock()
	h.closed = true
	for t := range h.active {
		_ = t.client.Close()
		_ = t.origin.Close()
	}
	h.mu.Unlock()
	h.wait.Wait()
}

func unavailable(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "close")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, `{"error":{"code":"`+code+`","message":"Egress unavailable"}}`)
}

func sensitiveMetadata(r *http.Request) bool {
	for _, name := range []string{"Authorization", "Proxy-Authorization", "Private-Token", "X-Gitlab-Token", "Cookie"} {
		if len(r.Header.Values(name)) != 0 {
			return true
		}
	}
	return false
}

// ServeHTTP accepts only bounded exact-authority CONNECT and local process health; caller headers/bodies are never forwarded.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.ctx.Err() != nil {
		unavailable(w, 503, "egress_stopped")
		return
	}
	if r.Method == http.MethodGet && r.URL.RawQuery == "" && (r.URL.Path == "/health/ready" || r.URL.Path == "/health/live") {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path == "/health/live" {
			_, _ = io.WriteString(w, `{"status":"alive"}`)
		} else {
			_, _ = io.WriteString(w, `{"status":"ready"}`)
		}
		return
	}
	if r.Method != http.MethodConnect || r.RequestURI != h.route.Endpoint() || r.Host != h.route.Endpoint() || r.URL.Host != h.route.Endpoint() || r.URL.Scheme != "" || r.URL.User != nil || r.URL.Path != "" || r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.Fragment != "" || r.ContentLength > 0 || len(r.TransferEncoding) > 0 || sensitiveMetadata(r) {
		unavailable(w, 403, "egress_denied")
		return
	}
	select {
	case h.slots <- struct{}{}:
		defer func() { <-h.slots }()
	default:
		unavailable(w, 429, "egress_busy")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	stop := context.AfterFunc(h.ctx, cancel)
	defer stop()
	origin, e := h.route.DialContext(ctx, "tcp", h.route.Endpoint())
	if e != nil {
		unavailable(w, 502, "egress_route_unavailable")
		return
	}
	defer origin.Close()
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		unavailable(w, 502, "egress_protocol_unavailable")
		return
	}
	client, buffer, e := hijacker.Hijack()
	if e != nil {
		return
	}
	defer client.Close()
	t := &tunnel{client: client, origin: origin}
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.active[t] = struct{}{}
	h.wait.Add(1)
	h.mu.Unlock()
	defer func() { h.mu.Lock(); delete(h.active, t); h.mu.Unlock(); h.wait.Done() }()
	interrupt := context.AfterFunc(h.ctx, func() { _ = client.Close(); _ = origin.Close() })
	defer interrupt()
	deadline := time.Now().Add(h.options.MaxLifetime)
	if client.SetDeadline(deadline) != nil || origin.SetDeadline(deadline) != nil {
		return
	}
	if _, e = buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); e != nil {
		return
	}
	if e = buffer.Flush(); e != nil {
		return
	}
	var copied sync.WaitGroup
	copied.Add(2)
	copyHalf := func(dst net.Conn, src io.Reader) {
		defer copied.Done()
		_, _ = io.Copy(dst, src)
		if half, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = half.CloseWrite()
		} else {
			_ = dst.Close()
		}
	}
	go copyHalf(origin, buffer.Reader)
	go copyHalf(client, origin)
	copied.Wait()
}
