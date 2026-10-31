package nativehttp_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"workgraph.local/platform/internal/access"
	directory "workgraph.local/platform/internal/directory/domain"
	"workgraph.local/platform/internal/identity/domain"
	"workgraph.local/platform/internal/identity/native"
	"workgraph.local/platform/internal/identity/nativehttp"
)

func TestNativeHTTPKeepsDeviceProofsOutOfURLsAndUsesExactAuthenticatedSelfRevocation(t *testing.T) {
	var server string
	key := "wg_" + strings.Repeat("A", 43)
	id := "11111111-1111-4111-8111-111111111111"
	actor := "22222222-2222-4222-8222-222222222222"
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" || r.Header.Get("Cookie") != "" {
			t.Error("secret-bearing URL/cookie")
		}
		if strings.HasPrefix(r.URL.Path, "/v1/auth/cli/") && r.Header.Get("Authorization") != "" {
			t.Error("bearer sent on public enrollment")
		}
		raw, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/auth/cli/prepare":
			if r.Method != "POST" || strings.Contains(string(raw), key) {
				t.Error("candidate uploaded")
			}
			w.WriteHeader(202)
			_ = json.NewEncoder(w).Encode(native.Prepared{ID: id, ExpiresAt: time.Now().Add(domain.CLIFlowTTL), VerificationURI: server + "/v1/auth/cli/login", IntervalSeconds: 2})
		case "/v1/auth/cli/poll", "/v1/auth/cli/cancel":
			if !strings.Contains(string(raw), "device_secret") {
				t.Error("proofs missing")
			}
			_ = json.NewEncoder(w).Encode(domain.CLIView{ID: id, Status: "pending"})
		case "/v1/me":
			if r.Header.Get("Authorization") != "Bearer "+key || r.Method != "GET" {
				t.Error("self credential missing")
			}
			_ = json.NewEncoder(w).Encode(directory.Me{User: directory.User{ID: actor, DisplayName: "Fixture", Enabled: true}, TokenID: id, Scopes: []access.Permission{access.ManageCredentials}, ExpiresAt: time.Now().Add(time.Hour)})
		case "/v1/me/tokens/" + id + "/revoke":
			if r.Header.Get("Authorization") != "Bearer "+key || r.Method != "POST" {
				t.Error("wrong revocation route/authority")
			}
			_ = json.NewEncoder(w).Encode(directory.Receipt{RequestID: id, Action: directory.RevokeToken, Kind: "token", EntityID: id, ActorID: actor, Version: 2, Sequence: 2, At: time.Now(), Deleted: true})
		default:
			t.Error("unexpected route")
			w.WriteHeader(404)
		}
	}))
	defer s.Close()
	server = s.URL
	c, e := nativehttp.New(server)
	if e != nil {
		t.Fatal(e)
	}
	r, _ := native.NewPendingRecord(server, "HTTP", []access.Permission{access.ManageCredentials}, 86400)
	if _, e = c.Prepare(context.Background(), r.Pending.Request); e != nil {
		t.Fatal(e)
	}
	if _, e = c.Poll(context.Background(), id, r.Pending.Device, r.Pending.Proof); e != nil {
		t.Fatal(e)
	}
	if _, e = c.Cancel(context.Background(), id, r.Pending.Device, r.Pending.Proof); e != nil {
		t.Fatal(e)
	}
	if _, e = c.Me(context.Background(), key); e != nil {
		t.Fatal(e)
	}
	if e = c.Revoke(context.Background(), key, id, id); e != nil {
		t.Fatal(e)
	}
}

func TestNativeHTTPRejectsRedirectsMalformedOversizedAndDiagnosticBearingResponses(t *testing.T) {
	for _, mode := range []string{"redirect", "duplicate", "large", "private-error", "html", "wrong-receipt"} {
		t.Run(mode, func(t *testing.T) {
			followed := false
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/moved" {
					followed = true
				}
				w.Header().Set("Content-Type", "application/json")
				switch mode {
				case "redirect":
					w.Header().Set("Location", "/moved")
					w.WriteHeader(307)
				case "duplicate":
					_, _ = io.WriteString(w, `{"token_id":"one","token_id":"two"}`)
				case "large":
					_, _ = io.WriteString(w, strings.Repeat("x", 32769))
				case "private-error":
					w.WriteHeader(503)
					_, _ = io.WriteString(w, "sensitive backend diagnostic")
				case "html":
					w.Header().Set("Content-Type", "text/html")
					_, _ = io.WriteString(w, "{}")
				case "wrong-receipt":
					_, _ = io.WriteString(w, `{"action":"revoke_token","kind":"token","entity_id":"22222222-2222-4222-8222-222222222222"}`)
				}
			}))
			defer s.Close()
			c, e := nativehttp.New(s.URL)
			if e != nil {
				t.Fatal(e)
			}
			key := "wg_" + strings.Repeat("A", 43)
			if mode == "wrong-receipt" {
				e = c.Revoke(context.Background(), key, "11111111-1111-4111-8111-111111111111", "11111111-1111-4111-8111-111111111111")
			} else {
				_, e = c.Me(context.Background(), key)
			}
			if e == nil || followed || strings.Contains(e.Error(), "sensitive") || strings.Contains(e.Error(), key) {
				t.Fatal("unsafe response accepted", e)
			}
			if mode == "private-error" && !errors.Is(e, native.ErrUnavailable) {
				t.Fatal(e)
			}
		})
	}
}

func TestNativeClientCanBeUpdatedBeforeCoreServerMigration(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(directory.Me{User: directory.User{ID: "11111111-1111-4111-8111-111111111111", DisplayName: "Engineer", Enabled: true}, TokenID: "22222222-2222-4222-8222-222222222222", Scopes: []access.Permission{access.ReadCapture, access.ManageCredentials, "knowledge:read", "review:run"}, ExpiresAt: time.Now().Add(time.Hour)})
	}))
	defer s.Close()
	c, e := nativehttp.New(s.URL)
	if e != nil {
		t.Fatal(e)
	}
	a, e := c.Me(context.Background(), "wg_"+strings.Repeat("A", 43))
	if e != nil || len(a.Scopes) != 2 || !slices.Contains(a.Scopes, access.ReadCapture) || !slices.Contains(a.Scopes, access.ManageCredentials) {
		t.Fatal("client-first rollout rejected surviving core credential", a, e)
	}
}
