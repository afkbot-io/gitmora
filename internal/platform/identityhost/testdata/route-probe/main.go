// Verification-only Linux process: exercise the actual OIDC adapter and ordinary OS certificate roots.
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"workgraph.local/platform/internal/identity/domain"
	"workgraph.local/platform/internal/identity/oidc"
)

func verify() error {
	if len(os.Args) != 3 || (os.Args[2] != "accepted" && os.Args[2] != "rejected") {
		return errors.New("select an explicit fixture issuer and expected result")
	}
	routes, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return errors.New("private Linux routes unavailable")
	}
	for _, line := range strings.Split(string(routes), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 7 && fields[1] == "00000000" && fields[7] == "00000000" {
			return errors.New("private RP has a default IPv4 route")
		}
	}
	config := domain.Config{Issuer: os.Args[1], ClientID: "gitmora", PublicURL: "https://gitmora.internal", AuthStyle: "basic", SessionSeconds: 28800, Policy: domain.Policy}
	if _, err := os.ReadFile(os.Getenv("SSL_CERT_FILE")); err != nil {
		return errors.New("fixture OS trust file unreadable")
	}
	if os.Args[2] == "accepted" {
		issuer, err := url.Parse(config.Issuer)
		if err != nil {
			return errors.New("fixture issuer invalid")
		}
		port := issuer.Port()
		if port == "" {
			port = "443"
		}
		connection, err := tls.DialWithDialer(&net.Dialer{Timeout: 3 * time.Second}, "tcp", net.JoinHostPort(issuer.Hostname(), port), &tls.Config{ServerName: issuer.Hostname(), MinVersion: tls.VersionTLS12})
		if err != nil {
			return fmt.Errorf("fixture TLS diagnosis: %w", err)
		}
		connection.Close()
	}
	client, err := oidc.New(config, "synthetic-unused-discovery-secret")
	if err != nil {
		return errors.New("fixture OIDC configuration invalid")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	address, err := client.AuthorizationURL(ctx, "fixture-state", strings.Repeat("v", 43), "fixture-nonce")
	if os.Args[2] == "rejected" {
		if err == nil {
			return errors.New("forbidden private TLS route accepted")
		}
	} else {
		if err != nil {
			return errors.New("expected private TLS OIDC discovery unavailable")
		}
		parsed, err := url.Parse(address)
		if err != nil || parsed.Scheme+"://"+parsed.Host+parsed.Path != config.Issuer+"/protocol/openid-connect/auth" || parsed.Query().Get("code_challenge_method") != "S256" || parsed.Query().Get("client_id") != config.ClientID {
			return errors.New("discovered authorization authority or PKCE changed")
		}
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]string{"status": os.Args[2], "transport": "actual private OIDC adapter with OS TLS trust and no default IPv4 route"})
}

func main() {
	if err := verify(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
