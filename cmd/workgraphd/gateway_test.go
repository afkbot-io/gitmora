package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestGatewayAlwaysUsesFixedBackendAndDoesNotNeedDatabaseSecrets(t *testing.T) {
	t.Setenv("WORKGRAPH_DATABASE_URL", "")
	var out bytes.Buffer
	if e := run(context.Background(), []string{"gateway", "--help"}, &out); e != nil {
		t.Fatal("gateway help required database", e)
	}
	seen := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen++
		if r.URL.Path != "/v1/captures" || r.Header.Get("Authorization") != "Bearer synthetic" || r.Header.Get("Cookie") != "session=synthetic" {
			t.Error("gateway changed request contract")
		}
		if strings.Contains(r.Header.Get("X-Forwarded-For"), "forged") {
			t.Error("gateway retained untrusted forwarding authority")
		}
		w.Header().Set("Content-Security-Policy", "default-src 'self'")
		w.WriteHeader(202)
		_, _ = io.WriteString(w, "ack")
	}))
	defer backend.Close()
	target, _ := url.Parse(backend.URL)
	proxy := httptest.NewServer(gatewayHandler(target))
	defer proxy.Close()
	r, _ := http.NewRequest("POST", proxy.URL+"/v1/captures", strings.NewReader("payload"))
	r.Host = "external.invalid"
	r.Header.Set("Authorization", "Bearer synthetic")
	r.Header.Set("Cookie", "session=synthetic")
	r.Header.Set("X-Forwarded-For", "forged")
	response, e := http.DefaultClient.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	if response.StatusCode != 202 || response.Header.Get("Content-Security-Policy") == "" || seen != 1 {
		t.Fatal("fixed backend response not preserved")
	}
}

func TestGatewayExplicitPublicOriginOverwritesUntrustedProxyAuthority(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "identity.internal:8443" || r.Header.Get("X-Forwarded-Host") != "identity.internal:8443" || r.Header.Get("X-Forwarded-Proto") != "https" || r.Header.Get("X-Forwarded-Port") != "8443" {
			t.Errorf("public authority not fixed: %s %+v", r.Host, r.Header)
		}
		if strings.Contains(r.Header.Get("X-Forwarded-For"), "forged") {
			t.Error("forged client forwarding retained")
		}
		w.WriteHeader(204)
	}))
	defer backend.Close()
	target, _ := url.Parse(backend.URL)
	origin, _ := url.Parse("https://identity.internal:8443")
	proxy := httptest.NewServer(gatewayHandlerWithOrigin(target, origin))
	defer proxy.Close()
	req, _ := http.NewRequest("GET", proxy.URL+"/realms/gitmora", nil)
	req.Host = "forged.invalid"
	req.Header.Set("X-Forwarded-Proto", "http")
	req.Header.Set("X-Forwarded-Host", "forged.invalid")
	req.Header.Set("X-Forwarded-Port", "80")
	req.Header.Set("X-Forwarded-For", "forged")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 204 {
		t.Fatal("proxy response failed")
	}
	for _, value := range []string{"http://identity.internal", "https://identity.internal/path", "https://user:pass@identity.internal", "https://identity.internal?x=y"} {
		if err := run(context.Background(), []string{"gateway", "--public-origin", value}, io.Discard); err == nil {
			t.Fatalf("invalid origin accepted: %s", value)
		}
	}
}
