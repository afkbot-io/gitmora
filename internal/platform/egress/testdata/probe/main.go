// Operator verification probe: public metadata only, no provider token input or certificate bypass.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"workgraph.local/platform/internal/platform/netroute"
)

func probe() error {
	if len(os.Args) != 2 {
		return errors.New("select offline or live verification")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	authority := "gitlab-egress:8080"
	if os.Args[1] == "offline-zai" {
		authority = "zai-egress:8080"
	}
	route, e := netroute.NewBoundDialer(authority, netroute.Private, nil, nil)
	if e != nil {
		return e
	}
	if os.Args[1] == "offline" || os.Args[1] == "offline-zai" {
		routes, e := os.ReadFile("/proc/net/route")
		if e != nil {
			return e
		}
		for _, line := range strings.Split(string(routes), "\n") {
			fields := strings.Fields(line)
			if len(fields) > 7 && fields[1] == "00000000" && fields[7] == "00000000" {
				return errors.New("private client has a default IPv4 route")
			}
		}
		c, e := route.DialContext(ctx, "tcp", authority)
		if e != nil {
			return e
		}
		_ = c.SetDeadline(time.Now().Add(3 * time.Second))
		_, e = io.WriteString(c, "CONNECT forbidden.invalid:443 HTTP/1.1\r\nHost: forbidden.invalid:443\r\n\r\n")
		if e != nil {
			c.Close()
			return e
		}
		r, e := http.ReadResponse(bufio.NewReader(c), &http.Request{Method: "CONNECT"})
		c.Close()
		if e != nil || r.StatusCode != 403 {
			return errors.New("different authority was accepted")
		}
		direct, stop := context.WithTimeout(ctx, time.Second)
		defer stop()
		conn, e := (&net.Dialer{}).DialContext(direct, "tcp", "1.1.1.1:443")
		if e == nil {
			conn.Close()
			return errors.New("private client has direct external access")
		}
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"status": "private route and denied authority verified"})
	}
	if os.Args[1] != "live" {
		return errors.New("unknown verification mode")
	}
	u, _ := url.Parse("http://gitlab-egress:8080")
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(u)
	transport.DialContext = route.DialContext
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 12 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r, e := client.Get("https://gitlab.com/api/v4/projects/gitlab-org%2Fgitlab")
	if e != nil {
		// This probe has no credential input and one fixed public URL; retain its non-secret transport diagnosis.
		return fmt.Errorf("verified GitLab HTTPS route unavailable: %w", e)
	}
	defer r.Body.Close()
	if r.StatusCode < 200 || r.StatusCode >= 500 {
		return errors.New("GitLab metadata endpoint unavailable")
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"status": "verified GitLab TLS route", "http_status": r.StatusCode, "authenticated": false})
}

func main() {
	if e := probe(); e != nil {
		_, _ = io.WriteString(os.Stderr, e.Error()+"\n")
		os.Exit(1)
	}
}
