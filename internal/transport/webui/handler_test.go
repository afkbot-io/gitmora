package webui_test

import (
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"workgraph.local/platform/internal/transport/webui"
)

func TestLocalAssetsAndStrictMissingPaths(t *testing.T) {
	h := webui.New(fstest.MapFS{"index.html": {Data: []byte("<html>local app</html>")}, "assets/app-12345678.js": {Data: []byte("export {}")}, "install.sh": {Data: []byte("#!/bin/sh\nexit 0\n")}, "ai.md": {Data: []byte("# Gitmora\ncontext.search\n")}})
	for _, tc := range []struct {
		path        string
		status      int
		cache, kind string
	}{
		{"/", 200, "no-store", "text/html"},
		{"/install.sh", 200, "no-store", "text/plain"},
		{"/ai.md", 200, "no-store", "text/markdown; charset=utf-8"},
		{"/other.md", 404, "no-store", ""},
		{"/assets/app-12345678.js", 200, "public, max-age=31536000, immutable", "javascript"},
		{"/v1/missing", 404, "no-store", ""},
		{"/assets/missing.js", 404, "no-store", ""},
		{"/index.html", 404, "no-store", ""},
		{"/assets/../index.html", 404, "no-store", ""},
		{"/assets/../ai.md", 404, "no-store", ""},
	} {
		t.Run(tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
			if w.Code != tc.status || w.Header().Get("Cache-Control") != tc.cache || !strings.Contains(w.Header().Get("Content-Type"), tc.kind) {
				t.Fatalf("unexpected response: %d %v", w.Code, w.Header())
			}
			if tc.status == 200 && (!strings.Contains(w.Header().Get("Content-Security-Policy"), "default-src 'none'") || strings.Contains(w.Header().Get("Content-Security-Policy"), "unsafe-inline")) {
				t.Fatal("unsafe application CSP")
			}
		})
	}
}

func TestUnbuiltApplicationIsExplicitlyUnavailable(t *testing.T) {
	w := httptest.NewRecorder()
	webui.New(fstest.MapFS{}).ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != 503 || !strings.Contains(w.Body.String(), "Build") {
		t.Fatal("missing build silently served an application")
	}
}

func TestStaticTransportNeverAcceptsMutation(t *testing.T) {
	w := httptest.NewRecorder()
	webui.New(fstest.MapFS{"index.html": {Data: []byte("app")}}).ServeHTTP(w, httptest.NewRequest("POST", "/", nil))
	if w.Code != 405 {
		t.Fatal("static transport accepted mutation")
	}
}

func TestAssetFormatsDoNotDependOnTheHostMIMEDatabase(t *testing.T) {
	for _, tc := range []struct {
		name, kind string
		status     int
	}{
		{"font-12345678.woff2", "font/woff2", 200},
		{"font-12345678.woff", "font/woff", 200},
		{"workgraph.svg", "image/svg+xml", 200},
		{"third-party-licenses.txt", "text/plain; charset=utf-8", 200},
		{"unexpected.bin", "", 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := "assets/" + tc.name
			w := httptest.NewRecorder()
			webui.New(fstest.MapFS{file: {Data: []byte("synthetic asset")}}).ServeHTTP(w, httptest.NewRequest("GET", "/"+file, nil))
			if w.Code != tc.status || tc.status == 200 && w.Header().Get("Content-Type") != tc.kind {
				t.Fatalf("non-portable asset response: %d %s", w.Code, w.Header().Get("Content-Type"))
			}
		})
	}
}
