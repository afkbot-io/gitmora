package nativeapi_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"workgraph.local/platform/internal/transport/nativeapi"
)

func TestAPIUsesOnlyExactServerAndNeverFollowsCredentialRedirects(t *testing.T) {
	followed := false
	key := "wg_" + strings.Repeat("A", 43)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { followed = true }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+key || r.Header.Get("Cookie") != "" {
			t.Error("wrong authority transport")
		}
		if r.URL.Path == "/v1/redirect" {
			w.Header().Set("Location", target.URL)
			w.WriteHeader(307)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(201)
		w.Write([]byte(`{"id":"created"}`))
	}))
	defer server.Close()
	client, e := nativeapi.New(server.URL)
	if e != nil {
		t.Fatal(e)
	}
	result, e := client.Do(context.Background(), "POST", "/v1/projects", []byte(`{"name":"Team"}`), key)
	if e != nil || result.Status != 201 || !strings.Contains(string(result.Body), "created") {
		t.Fatal(result, e)
	}
	if _, e = client.Do(context.Background(), "GET", "/v1/redirect", nil, key); e == nil || followed {
		t.Fatal("credential followed redirect", e)
	}
	for _, path := range []string{target.URL + "/v1/projects", "//evil.invalid/v1/projects", "/v1/../secrets", "/v1/projects#private"} {
		if _, e = client.Do(context.Background(), "GET", path, nil, key); !errors.Is(e, nativeapi.ErrInvalid) {
			t.Fatal("unsafe destination", path, e)
		}
	}
}
func TestAPIRejectsPrivateDiagnosticsAndMalformedResponse(t *testing.T) {
	for _, tc := range []struct {
		status  int
		content string
	}{{403, `{"error":"private-provider-secret"}`}, {200, `{"id":1,"id":2}`}, {200, `<html>private-provider-secret</html>`}} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(tc.status)
			w.Write([]byte(tc.content))
		}))
		c, e := nativeapi.New(server.URL)
		if e != nil {
			t.Fatal(e)
		}
		_, e = c.Do(context.Background(), "GET", "/v1/projects", nil, "wg_"+strings.Repeat("A", 43))
		server.Close()
		if e == nil || strings.Contains(e.Error(), "private-provider-secret") {
			t.Fatal("invalid response accepted or diagnostic leaked", e)
		}
	}
}
