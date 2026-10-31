package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	identity "workgraph.local/platform/internal/identity/domain"
	"workgraph.local/platform/internal/jsonwire"
)

// healthcheck is deliberately database/configuration independent and available in the shell-free image.
func healthcheck(parent context.Context, args []string, output io.Writer) error {
	failure := errors.New("local health probe failed")
	flags := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() {
		_, _ = fmt.Fprintln(output, "Usage: gitmorad healthcheck --url http://127.0.0.1:8080/health/ready [--oidc-issuer <exact issuer>]")
	}
	endpoint := flags.String("url", "http://127.0.0.1:8080/health/ready", "Local numeric loopback readiness/liveness endpoint")
	issuer := flags.String("oidc-issuer", "", "Verify numeric-loopback discovery availability for this exact issuer")
	e := flags.Parse(args)
	if errors.Is(e, flag.ErrHelp) {
		return nil
	}
	if e != nil || flags.NArg() != 0 {
		return failure
	}
	u, e := url.Parse(*endpoint)
	if e != nil || len(*endpoint) > 256 || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" {
		return failure
	}
	limit := 1024
	if *issuer != "" {
		expected, e := identity.URL(*issuer)
		if e != nil || u.Path != strings.TrimSuffix(expected.Path, "/")+"/.well-known/openid-configuration" {
			return failure
		}
		limit = 128 * 1024
	} else if u.Path != "/health/ready" && u.Path != "/health/live" {
		return failure
	}
	ip := net.ParseIP(u.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return failure
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	defer transport.CloseIdleConnections()
	client := http.Client{Transport: transport, Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r, e := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if e != nil {
		return failure
	}
	response, e := client.Do(r)
	if e != nil {
		return failure
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return failure
	}
	media, _, e := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if e != nil || media != "application/json" {
		return failure
	}
	raw, e := io.ReadAll(io.LimitReader(response.Body, int64(limit+1)))
	if e != nil || len(raw) > limit {
		return failure
	}
	if *issuer != "" {
		var value struct {
			Issuer string `json:"issuer"`
		}
		if jsonwire.DecodeExternalObject(raw, &value) != nil || value.Issuer != *issuer {
			return failure
		}
		return nil
	}
	var result struct {
		Status string `json:"status"`
	}
	wanted := "ready"
	if u.Path == "/health/live" {
		wanted = "alive"
	}
	if jsonwire.DecodeObject(raw, &result) != nil || result.Status != wanted {
		return failure
	}
	return nil
}
