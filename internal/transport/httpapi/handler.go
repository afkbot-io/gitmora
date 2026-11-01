// Package httpapi translates versioned HTTP contracts into authorized application operations.
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/capture/application"
	"workgraph.local/platform/internal/capture/domain"
	identitydomain "workgraph.local/platform/internal/identity/domain"
	"workgraph.local/platform/internal/jsonwire"
)

// CaptureService is the capture use-case surface consumed by this transport.
type CaptureService interface {
	Ingest(context.Context, string, domain.Snapshot) (application.IngestResult, error)
	Get(context.Context, string, string, string, int64) (application.StoredRevision, error)
}

// Config supplies use cases and a bounded dependency health probe.
type Config struct {
	Captures          CaptureService
	Authenticator     access.Authenticator
	Ready             func(context.Context) error
	Forge             ForgeService
	Code              CodeService
	Retrieval         RetrievalService
	ContextPacks      ContextPackService
	ContextBrowser    ContextBrowseService
	ContextResolver   ContextResolveService
	Directory         DirectoryService
	Identity          IdentityService
	CLIEnrollment     CLIEnrollmentService
	WebUI             http.Handler
	MCPAllowedOrigins []string
	MCPPublicOrigin   string
}

// NewHandler constructs a strict JSON API. Unknown fields, duplicate keys, excessive depth,
// invalid UTF-8, oversized bodies and missing token capabilities fail at the boundary.
func NewHandler(config Config) http.Handler {
	if config.Captures == nil || config.Authenticator == nil || config.Ready == nil {
		panic("httpapi: required dependency missing")
	}
	mux := http.NewServeMux()
	if config.WebUI != nil {
		mux.Handle("GET /{$}", config.WebUI)
		mux.Handle("GET /assets/", config.WebUI)
		mux.Handle("GET /install.sh", config.WebUI)
	}
	registerForge(mux, config)
	registerCode(mux, config)
	registerRetrieval(mux, config)
	registerDirectory(mux, config)
	registerIdentity(mux, config)
	registerCLIEnrollment(mux, config)
	mux.HandleFunc("GET /health/live", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"status": "alive"}) })
	mux.HandleFunc("GET /health/ready", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := config.Ready(ctx); err != nil {
			writeError(w, 503, "not_ready", "Service is not ready")
			return
		}
		writeJSON(w, 200, map[string]string{"status": "ready"})
	})
	mux.HandleFunc("POST /v1/captures", authorize(config.Authenticator, access.WriteCapture, func(w http.ResponseWriter, r *http.Request, principal access.Principal) {
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" {
			writeError(w, 415, "unsupported_media_type", "Use application/json")
			return
		}
		raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 3*1024*1024))
		if err != nil {
			var max *http.MaxBytesError
			if errors.As(err, &max) {
				writeError(w, 413, "request_too_large", "Request exceeds the byte limit")
			} else {
				writeError(w, 400, "invalid_request", "Cannot read request")
			}
			return
		}
		var snapshot domain.Snapshot
		if err := jsonwire.DecodeObject(raw, &snapshot); err != nil {
			writeError(w, 400, "invalid_request", "Invalid capture JSON")
			return
		}
		result, err := config.Captures.Ingest(r.Context(), principal.UserID, snapshot)
		if err != nil {
			mapError(w, err)
			return
		}
		status := http.StatusOK
		if result.Created {
			status = http.StatusCreated
		}
		writeJSON(w, status, result)
	}))
	mux.HandleFunc("GET /v1/repositories/{repository}/sessions/{session}", authorize(config.Authenticator, access.ReadCapture, func(w http.ResponseWriter, r *http.Request, principal access.Principal) {
		revision, err := parseRevision(r)
		if err != nil {
			writeError(w, 400, "invalid_request", "Invalid revision query")
			return
		}
		stored, err := config.Captures.Get(r.Context(), principal.UserID, r.PathValue("repository"), r.PathValue("session"), revision)
		if err != nil {
			mapError(w, err)
			return
		}
		confirmed := stored.ConfirmedCommits
		if confirmed == nil {
			confirmed = []string{}
		}
		writeJSON(w, 200, struct {
			Snapshot                domain.Snapshot `json:"snapshot"`
			Digest                  string          `json:"digest"`
			ConfirmedCommits        []string        `json:"confirmed_commits"`
			ParentReferenceVerified bool            `json:"parent_reference_verified,omitempty"`
		}{stored.Session.Snapshot(), stored.Session.Digest(), confirmed, stored.ParentReferenceVerified})
	}))
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		mux.ServeHTTP(w, r)
	})
	return identityCookies(config, handler)
}

func authorize(auth access.Authenticator, permission access.Permission, next func(http.ResponseWriter, *http.Request, access.Principal)) http.HandlerFunc {
	return authenticated(auth, func(w http.ResponseWriter, r *http.Request, p access.Principal) {
		if !p.Has(permission) {
			writeError(w, 403, "forbidden", "Access denied")
			return
		}
		next(w, r, p)
	})
}

func authenticated(auth access.Authenticator, next func(http.ResponseWriter, *http.Request, access.Principal)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		values := r.Header.Values("Authorization")
		if len(values) == 0 {
			if session, ok := r.Context().Value(browserSessionContextKey{}).(identitydomain.Session); ok {
				next(w, r, session.Principal)
				return
			}
		}
		if len(values) != 1 {
			unauthenticated(w)
			return
		}
		scheme, raw, found := strings.Cut(values[0], " ")
		if !found || !strings.EqualFold(scheme, "Bearer") || raw == "" || strings.ContainsAny(raw, " \t\r\n") {
			unauthenticated(w)
			return
		}
		principal, err := auth.Authenticate(r.Context(), raw)
		if errors.Is(err, access.ErrUnauthenticated) {
			unauthenticated(w)
			return
		}
		if err != nil {
			mapError(w, err)
			return
		}
		next(w, r, principal)
	}
}

func unauthenticated(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="workgraph"`)
	writeError(w, 401, "unauthenticated", "Authentication required")
}

func parseRevision(r *http.Request) (int64, error) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return 0, err
	}
	if len(query) == 0 {
		return 0, nil
	}
	values, ok := query["revision"]
	if !ok || len(query) != 1 || len(values) != 1 || values[0] == "" {
		return 0, errors.New("invalid revision query")
	}
	revision, err := strconv.ParseInt(values[0], 10, 64)
	if err != nil || revision < 0 || revision > 1_000_000_000 {
		return 0, errors.New("invalid revision")
	}
	return revision, nil
}

func mapError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, access.ErrDenied):
		writeError(w, 403, "forbidden", "Access denied")
	case errors.Is(err, application.ErrNotFound):
		writeError(w, 404, "not_found", "Capture not found")
	case errors.Is(err, application.ErrConflict):
		writeError(w, 409, "conflict", "Capture identity conflicts with stored evidence")
	case errors.Is(err, domain.ErrInvalid):
		writeError(w, 422, "invalid_capture", "Capture violates the versioned contract")
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, 503, "temporarily_unavailable", "Operation timed out")
	default:
		writeError(w, 500, "internal_error", "Internal server error")
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
