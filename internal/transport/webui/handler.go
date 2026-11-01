// Package webui serves only the locally built application and its immutable assets.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"regexp"
	"strings"
)

//go:embed all:assets
var bundled embed.FS

var hashedAsset = regexp.MustCompile(`-[A-Za-z0-9_-]{8,}\.(js|css|woff2|woff)$`)

// Keep delivery independent of an optional /etc/mime.types, including scratch images.
var contentTypes = map[string]string{
	".js": "text/javascript; charset=utf-8", ".css": "text/css; charset=utf-8",
	".woff": "font/woff", ".woff2": "font/woff2", ".svg": "image/svg+xml",
	".txt": "text/plain; charset=utf-8",
	".sh": "text/plain; charset=utf-8",
}

// Embedded returns application bytes built before the Go binary is compiled.
func Embedded() http.Handler {
	assets, err := fs.Sub(bundled, "assets")
	if err != nil {
		panic("webui: embedded assets unavailable")
	}
	return New(assets)
}

// New intentionally has no SPA fallback: unknown API/asset paths never receive HTML.
func New(assets fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self' 'report-sample'; font-src 'self'; img-src 'self'; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'; object-src 'none'")
		if r.Method != "GET" && r.Method != "HEAD" {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Method not allowed", 405)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/")
		if r.URL.Path == "/" {
			name = "index.html"
		} else if (r.URL.Path != "/install.sh" && !strings.HasPrefix(r.URL.Path, "/assets/")) || !fs.ValidPath(name) || strings.ContainsAny(name, "\\\x00") {
			http.NotFound(w, r)
			return
		}
		data, err := fs.ReadFile(assets, name)
		if err != nil {
			if name == "index.html" {
				http.Error(w, "Build the local web application before compiling this server.", 503)
			} else {
				http.NotFound(w, r)
			}
			return
		}
		kind := contentTypes[path.Ext(name)]
		if name == "index.html" {
			kind = "text/html; charset=utf-8"
		}
		if kind == "" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", kind)
		if name != "index.html" && hashedAsset.MatchString(name) {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		if r.Method == "GET" {
			_, _ = w.Write(data)
		}
	})
}
