package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"time"

	identity "workgraph.local/platform/internal/identity/domain"
)

// gatewayCommand runs the credential-free ingress role before any database/provider configuration is loaded.
func gatewayCommand(ctx context.Context, args []string, output io.Writer) error {
	flags := flag.NewFlagSet("gateway", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() {
		_, _ = fmt.Fprintln(output, "Usage: gitmorad gateway --listen 0.0.0.0:8080 --upstream http://api:8080 [--public-origin https://identity.internal] [--identity-prefix /auth]")
	}
	listen := flags.String("listen", "0.0.0.0:8080", "Ingress listen address")
	upstream := flags.String("upstream", "http://api:8080", "Fixed private API upstream")
	publicURL := flags.String("public-origin", "", "Optional fixed public origin for an identity backend behind TLS termination")
	prefix := flags.String("identity-prefix", "", "Optional /auth namespace with separate provider cookies")
	e := flags.Parse(args)
	if errors.Is(e, flag.ErrHelp) {
		return nil
	}
	if e != nil || flags.NArg() != 0 {
		return errors.New("invalid gateway configuration")
	}
	u, e := url.Parse(*upstream)
	if e != nil || u.Scheme != "http" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery || len(*upstream) > 256 {
		return errors.New("invalid gateway upstream")
	}
	var origin *url.URL
	if *publicURL != "" {
		origin, e = identity.URL(*publicURL)
		if e != nil || origin.Path != "" || identity.Origin(origin) != *publicURL {
			return errors.New("invalid gateway public origin")
		}
	}
	if *prefix != "" && (*prefix != "/auth" || origin == nil) {
		return errors.New("invalid identity gateway prefix")
	}
	return serve(ctx, *listen, gatewayHandlerWithIdentity(u, origin, *prefix))
}

// gatewayHandler forwards to one operator-selected backend, never an absolute URL or CONNECT destination from a caller.
func gatewayHandler(target *url.URL) http.Handler {
	return gatewayHandlerWithOrigin(target, nil)
}

// gatewayHandlerWithOrigin fixes proxy authority from operator configuration, never caller headers.
func gatewayHandlerWithOrigin(target, origin *url.URL) http.Handler {
	return gatewayHandlerWithIdentity(target, origin, "")
}

func gatewayHandlerWithIdentity(target, origin *url.URL, prefix string) http.Handler {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.ResponseHeaderTimeout = 20 * time.Second
	proxy := &httputil.ReverseProxy{Transport: transport, FlushInterval: -1, Rewrite: func(r *httputil.ProxyRequest) {
		r.SetURL(target)
		r.Out.Host = r.In.Host
		r.SetXForwarded()
		if prefix != "" {
			filterIdentityRequest(r.Out)
		}
		if origin != nil {
			r.Out.Host = origin.Host
			r.Out.Header.Set("X-Forwarded-Host", origin.Host)
			r.Out.Header.Set("X-Forwarded-Proto", origin.Scheme)
			port := origin.Port()
			if port == "" {
				if origin.Scheme == "https" {
					port = strconv.Itoa(443)
				} else {
					port = strconv.Itoa(80)
				}
			}
			r.Out.Header.Set("X-Forwarded-Port", port)
		}
	}, ModifyResponse: func(response *http.Response) error {
		if prefix == "" {
			return nil
		}
		return scopeIdentityCookies(response, prefix)
	}, ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(503)
		_, _ = io.WriteString(w, `{"error":{"code":"gateway_backend_unavailable","message":"Service temporarily unavailable"}}`)
	}}
	return identityPathHandler(proxy, prefix)
}
