package httpapi_test

import (
	"context"
	"net/http"
	"testing"

	"workgraph.local/platform/internal/transport/httpapi"
)

func TestApplicationCannotCatchUnknownAPIPaths(t *testing.T) {
	calls := 0
	ui := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls++; _, _ = w.Write([]byte("local app")) })
	h := httpapi.NewHandler(httpapi.Config{Captures: &capture{}, Authenticator: auth{}, Ready: func(context.Context) error { return nil }, WebUI: ui})
	for _, path := range []string{"/v1/missing", "/v1", "/mcp-missing", "/health/missing", "/unknown"} {
		if response := request(h, "GET", path, "", ""); response.Code != 404 || calls != 0 {
			t.Fatalf("UI caught unknown path %s", path)
		}
	}
	for _, path := range []string{"/", "/assets/app.js"} {
		if response := request(h, "GET", path, "", ""); response.Code != 200 {
			t.Fatal("application unavailable")
		}
	}
	if calls != 2 {
		t.Fatal("wrong application routes")
	}
	if response := request(h, "GET", "/v1/me", "", ""); response.Code != 401 {
		t.Fatal("application bypassed API authentication")
	}
}
