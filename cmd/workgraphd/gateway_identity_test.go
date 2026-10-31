package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestSharedOriginIdentityGatewaySeparatesCookieNamespaces(t *testing.T) {
	seen := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen++
		if strings.Contains(r.Header.Get("Cookie"), "rp-private") || r.Header.Get("Authorization") == "Bearer wg_rp-private" || r.Header.Get("Cookie") != "AUTH_SESSION_ID=provider-private" {
			t.Error("RP browser authority reached identity provider")
		}
		if seen == 2 && r.Header.Get("Authorization") != "Basic fixture-client-secret" {
			t.Error("OIDC client authentication was removed")
		}
		http.SetCookie(w, &http.Cookie{Name: "KEYCLOAK_SESSION", Value: "provider", Path: "/", Domain: "tasktopia.online", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
		http.SetCookie(w, &http.Cookie{Name: "AUTH_SESSION_ID", Value: "provider", Path: "/auth/realms/gitmora/", HttpOnly: true})
	}))
	defer backend.Close()
	target, _ := url.Parse(backend.URL)
	origin, _ := url.Parse("https://gitmora.tasktopia.online")
	handler := gatewayHandlerWithIdentity(target, origin, "/auth")
	for _, authorization := range []string{"Bearer wg_rp-private", "Basic fixture-client-secret"} {
		request := httptest.NewRequest("GET", "https://gitmora.tasktopia.online/auth/realms/gitmora/", nil)
		request.Header.Set("Cookie", "__Host-workgraph_session=rp-private; workgraph_login_dev=rp-private; AUTH_SESSION_ID=provider-private; __Host-workgraph_cli=rp-private")
		request.Header.Set("Authorization", authorization)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != 200 || len(response.Result().Cookies()) != 2 {
			t.Fatal("provider response failed")
		}
		for _, cookie := range response.Result().Cookies() {
			if cookie.Domain != "" || cookie.Path != "/auth" && cookie.Path != "/auth/realms/gitmora/" || !cookie.HttpOnly {
				t.Fatal("provider cookie escaped its namespace")
			}
		}
	}
}

func TestSharedOriginIdentityGatewayCannotWriteRPCookiesOrForwardEscapingPaths(t *testing.T) {
	seen := 0
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		seen++
		http.SetCookie(w, &http.Cookie{Name: "__Host-workgraph_session", Value: "provider-must-not-replace-rp", Path: "/", Secure: true})
	}))
	defer backend.Close()
	target, _ := url.Parse(backend.URL)
	origin, _ := url.Parse("https://gitmora.tasktopia.online")
	handler := gatewayHandlerWithIdentity(target, origin, "/auth")
	for _, path := range []string{"/v1/me", "/auth/../v1/me", "/auth/%252e%252e/admin/", "/auth/%5cadmin/"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", "https://gitmora.tasktopia.online"+path, nil))
		if response.Code != 404 || seen != 0 {
			t.Fatal("identity path escaped into another namespace")
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "https://gitmora.tasktopia.online/auth/realms/gitmora/", nil))
	if response.Code != 503 || len(response.Header().Values("Set-Cookie")) != 0 {
		t.Fatal("provider replaced RP browser authority")
	}
}
