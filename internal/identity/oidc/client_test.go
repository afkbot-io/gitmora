package oidc_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"workgraph.local/platform/internal/identity/domain"
	adapter "workgraph.local/platform/internal/identity/oidc"
)

func TestRealSignedOIDCCodeExchangeRejectsInvalidClaims(t *testing.T) {
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	var base, mode, nonce, expectedChallenge string
	encode := func(v any) string { raw, _ := json.Marshal(v); return base64.RawURLEncoding.EncodeToString(raw) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			json.NewEncoder(w).Encode(map[string]any{"issuer": base, "authorization_endpoint": base + "/authorize", "token_endpoint": base + "/token", "jwks_uri": base + "/keys", "id_token_signing_alg_values_supported": []string{"RS256"}})
		case "/keys":
			json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{"kty": "RSA", "use": "sig", "alg": "RS256", "kid": "fixture", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
		case "/token":
			client, secret, ok := r.BasicAuth()
			r.ParseForm()
			v := r.Form.Get("code_verifier")
			h := sha256.Sum256([]byte(v))
			if !ok || client != "workgraph" || secret != "synthetic-client-secret" || base64.RawURLEncoding.EncodeToString(h[:]) != expectedChallenge {
				w.WriteHeader(400)
				w.Write([]byte(`{"error":"invalid_grant"}`))
				return
			}
			claims := map[string]any{"iss": base, "sub": "subject-1", "aud": "workgraph", "iat": time.Now().Unix(), "exp": time.Now().Add(time.Minute).Unix(), "nonce": nonce}
			switch mode {
			case "nonce":
				claims["nonce"] = "wrong"
			case "issuer":
				claims["iss"] = "https://another.internal"
			case "audience":
				claims["aud"] = "other"
			case "expired":
				claims["exp"] = time.Now().Add(-time.Minute).Unix()
			case "future":
				claims["iat"] = time.Now().Add(time.Hour).Unix()
			case "nbf":
				claims["nbf"] = time.Now().Add(time.Hour).Unix()
			case "azp":
				claims["aud"] = []string{"workgraph", "other"}
				claims["azp"] = "other"
			}
			header := map[string]any{"alg": "RS256", "kid": "fixture", "typ": "JWT"}
			if mode == "oversized_jwk" {
				// A genuinely signed token must not make the JWK parser accept an unbounded RSA modulus.
				n := new(big.Int).Lsh(big.NewInt(1), 4096)
				header["jwk"] = map[string]string{"kty": "RSA", "n": base64.RawURLEncoding.EncodeToString(n.Bytes()), "e": "AQAB"}
			}
			body := encode(header) + "." + encode(claims)
			digest := sha256.Sum256([]byte(body))
			signature, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
			if mode == "signature" {
				signature[0] ^= 1
			}
			jwt := body + "." + base64.RawURLEncoding.EncodeToString(signature)
			json.NewEncoder(w).Encode(map[string]any{"access_token": "synthetic-access-token", "token_type": "Bearer", "expires_in": 60, "id_token": jwt})
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	base = server.URL
	config := domain.Config{Issuer: base, ClientID: "workgraph", PublicURL: "http://127.0.0.1:8080", AuthStyle: "basic", SessionSeconds: 28800, Policy: domain.Policy}
	client, e := adapter.New(config, "synthetic-client-secret")
	if e != nil {
		t.Fatal(e)
	}
	state, _ := domain.NewSecret(domain.StatePrefix)
	browser, _ := domain.NewSecret(domain.FlowPrefix)
	verifier, n, e := domain.Proof(state, browser)
	if e != nil {
		t.Fatal(e)
	}
	nonce = n
	auth, e := client.AuthorizationURL(context.Background(), state, verifier, nonce)
	if e != nil {
		t.Fatal(e)
	}
	u, _ := url.Parse(auth)
	expectedChallenge = u.Query().Get("code_challenge")
	if expectedChallenge == "" || u.Query().Get("code_challenge_method") != "S256" || u.Query().Get("nonce") != nonce {
		t.Fatal("PKCE/nonce absent")
	}
	identity, e := client.Exchange(context.Background(), "fixture-code", verifier, nonce)
	if e != nil || identity.Subject != "subject-1" || identity.Issuer != base {
		t.Fatal(identity, e)
	}
	for _, bad := range []string{"nonce", "issuer", "audience", "expired", "future", "azp", "nbf", "signature", "oversized_jwk"} {
		t.Run(bad, func(t *testing.T) {
			mode = bad
			if _, e := client.Exchange(context.Background(), "fixture-code", verifier, nonce); e == nil || strings.Contains(e.Error(), "synthetic") {
				t.Fatal("invalid proof accepted or secret leaked", e)
			}
		})
	}
}

func TestDiscoveryCannotRedirectOrSelectAnotherOrigin(t *testing.T) {
	var externalCalls atomic.Int64
	external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		externalCalls.Add(1)
		w.Write([]byte(`{"error":"must not be called"}`))
	}))
	defer external.Close()
	for _, mode := range []string{"endpoint", "redirect", "duplicate", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			var base string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch mode {
				case "redirect":
					http.Redirect(w, r, external.URL, 302)
				case "duplicate":
					w.Write([]byte(`{"issuer":"` + base + `","issuer":"` + base + `"}`))
				case "oversized":
					w.Write([]byte(`{"value":"` + strings.Repeat("x", 129*1024) + `"}`))
				default:
					json.NewEncoder(w).Encode(map[string]any{"issuer": base, "authorization_endpoint": base + "/auth", "token_endpoint": external.URL + "/token", "jwks_uri": base + "/keys"})
				}
			}))
			defer server.Close()
			base = server.URL
			cfg := domain.Config{Issuer: base, ClientID: "fixture", PublicURL: "http://127.0.0.1:8080", AuthStyle: "basic", SessionSeconds: 28800, Policy: domain.Policy}
			client, e := adapter.New(cfg, "synthetic-secret")
			if e != nil {
				t.Fatal(e)
			}
			if _, e = client.AuthorizationURL(context.Background(), "state", "verifier", "nonce"); e == nil {
				t.Fatal("invalid provider metadata accepted")
			}
		})
	}
	if externalCalls.Load() != 0 {
		t.Fatal("followed an unconfigured authority")
	}
}

func TestWaitingForDiscoveryHonorsRequestCancellation(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release; w.Write([]byte(`{}`)) }))
	defer server.Close()
	cfg := domain.Config{Issuer: server.URL, ClientID: "fixture", PublicURL: "http://127.0.0.1:8080", AuthStyle: "basic", SessionSeconds: 28800, Policy: domain.Policy}
	client, e := adapter.New(cfg, "synthetic-secret")
	if e != nil {
		t.Fatal(e)
	}
	first, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, e := client.AuthorizationURL(first, "state", "verifier", "nonce"); done <- e }()
	<-entered
	waiting, stop := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer stop()
	start := time.Now()
	_, e = client.AuthorizationURL(waiting, "other-state", "verifier", "nonce")
	if e == nil || time.Since(start) > time.Second {
		close(release)
		t.Fatal("discovery wait ignored cancellation", e)
	}
	cancel()
	close(release)
	if e = <-done; e == nil {
		t.Fatal("cancelled discovery succeeded")
	}
}
