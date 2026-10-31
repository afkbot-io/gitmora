package egress_test

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"workgraph.local/platform/internal/platform/egress"
	"workgraph.local/platform/internal/platform/netroute"
)

type dns struct{}

func (dns) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return []netip.Addr{netip.MustParseAddr("1.1.1.1")}, nil
}

type redirect struct{ address string }

func (d redirect) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if address != "1.1.1.1:443" {
		panic("unchecked route")
	}
	return (&net.Dialer{}).DialContext(ctx, network, d.address)
}

func fixture(t *testing.T, backend *httptest.Server, limit int) (*egress.Handler, *httptest.Server) {
	t.Helper()
	route, e := netroute.NewBoundDialer("example.com:443", netroute.PublicIPv4, dns{}, redirect{backend.Listener.Addr().String()})
	if e != nil {
		t.Fatal(e)
	}
	h, e := egress.New(context.Background(), route, egress.Options{MaxTunnels: limit, MaxLifetime: time.Minute})
	if e != nil {
		t.Fatal(e)
	}
	proxy := httptest.NewServer(h)
	t.Cleanup(proxy.Close)
	t.Cleanup(h.Close)
	return h, proxy
}

func TestFixedCONNECTPreservesEndToEndTLSAndRejectsOtherDestinations(t *testing.T) {
	var requests atomic.Int64
	backend := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "Bearer synthetic-private-provider-value" {
			t.Error("TLS payload lost")
		}
		_, _ = io.WriteString(w, "verified upstream")
	}))
	defer backend.Close()
	_, proxy := fixture(t, backend, 2)
	pool := x509.NewCertPool()
	pool.AddCert(backend.Certificate())
	p, _ := url.Parse(proxy.URL)
	transport := &http.Transport{Proxy: http.ProxyURL(p), TLSClientConfig: &tls.Config{RootCAs: pool}, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	r, _ := http.NewRequest("GET", "https://example.com/retained", nil)
	r.Header.Set("Authorization", "Bearer synthetic-private-provider-value")
	response, e := client.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 200 || string(raw) != "verified upstream" || requests.Load() != 1 {
		t.Fatal("real TLS tunnel failed")
	}
	for _, target := range []string{"example.org:443", "example.com:80", "example.com.:443", "user@example.com:443"} {
		request := httptest.NewRequest("CONNECT", "http://proxy.invalid", nil)
		request.RequestURI = target
		request.URL = &url.URL{Host: target}
		request.Host = target
		out := httptest.NewRecorder()
		proxy.Config.Handler.ServeHTTP(out, request)
		if out.Code != 403 || strings.Contains(out.Body.String(), target) {
			t.Fatal("proxy retargeted or echoed caller input")
		}
	}
	for _, name := range []string{"Authorization", "Proxy-Authorization", "PRIVATE-TOKEN", "X-Gitlab-Token", "Cookie"} {
		request := httptest.NewRequest("CONNECT", "http://proxy.invalid", nil)
		request.RequestURI = "example.com:443"
		request.Host = request.RequestURI
		request.URL = &url.URL{Host: request.RequestURI}
		request.Header.Add(name, "")
		request.Header.Add(name, "synthetic-private-marker")
		out := httptest.NewRecorder()
		proxy.Config.Handler.ServeHTTP(out, request)
		if out.Code != 403 || strings.Contains(out.Body.String(), "synthetic-private-marker") {
			t.Fatal("plaintext credential metadata accepted or echoed", name, out.Code)
		}
	}
	transport.TLSClientConfig.ServerName = "wrong.example"
	var hostname x509.HostnameError
	if _, e = client.Do(r); e == nil || !errors.As(e, &hostname) || requests.Load() != 1 {
		t.Fatal("tunnel bypassed TLS identity verification", e)
	}
}

func TestTunnelBudgetAndCloseFenceHijackedSockets(t *testing.T) {
	backend := httptest.NewTLSServer(http.NotFoundHandler())
	defer backend.Close()
	h, proxy := fixture(t, backend, 1)
	conn, e := net.Dial("tcp", proxy.Listener.Addr().String())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	_, _ = io.WriteString(conn, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n")
	reader := bufio.NewReader(conn)
	line, e := reader.ReadString('\n')
	if e != nil || !strings.Contains(line, "200") {
		t.Fatal("tunnel did not open", e)
	}
	for {
		line, e = reader.ReadString('\n')
		if e != nil {
			t.Fatal(e)
		}
		if line == "\r\n" {
			break
		}
	}
	r := httptest.NewRequest("CONNECT", "http://proxy.invalid", nil)
	r.RequestURI = "example.com:443"
	r.URL = &url.URL{Host: r.RequestURI}
	r.Host = r.RequestURI
	out := httptest.NewRecorder()
	h.ServeHTTP(out, r)
	if out.Code != 429 {
		t.Fatal("unbounded concurrent tunnels")
	}
	h.Close()
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	if _, e = reader.ReadByte(); e == nil {
		t.Fatal("closed relay left tunnel active")
	}
	out = httptest.NewRecorder()
	h.ServeHTTP(out, httptest.NewRequest("GET", "http://relay/health/ready", nil))
	if out.Code != 503 {
		t.Fatal("closed relay remained ready")
	}
}

func TestIdleTunnelCannotOutliveItsHardBudget(t *testing.T) {
	backend := httptest.NewTLSServer(http.NotFoundHandler())
	defer backend.Close()
	route, e := netroute.NewBoundDialer("example.com:443", netroute.PublicIPv4, dns{}, redirect{backend.Listener.Addr().String()})
	if e != nil {
		t.Fatal(e)
	}
	h, e := egress.New(context.Background(), route, egress.Options{MaxTunnels: 1, MaxLifetime: time.Second})
	if e != nil {
		t.Fatal(e)
	}
	defer h.Close()
	proxy := httptest.NewServer(h)
	defer proxy.Close()
	conn, e := net.Dial("tcp", proxy.Listener.Addr().String())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	_, _ = io.WriteString(conn, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n")
	reader := bufio.NewReader(conn)
	line, e := reader.ReadString('\n')
	if e != nil || !strings.Contains(line, "200") {
		t.Fatal("idle tunnel did not open", e)
	}
	for {
		line, e = reader.ReadString('\n')
		if e != nil {
			t.Fatal(e)
		}
		if line == "\r\n" {
			break
		}
	}
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, e = reader.ReadByte(); e == nil {
		t.Fatal("idle tunnel received unexpected payload")
	} else {
		var timeout net.Error
		if errors.As(e, &timeout) && timeout.Timeout() {
			t.Fatal("relay did not enforce its own lifetime")
		}
	}
}
