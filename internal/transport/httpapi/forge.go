package httpapi

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"time"

	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/forge/application"
	"workgraph.local/platform/internal/forge/domain"
	"workgraph.local/platform/internal/jsonwire"
)

// ForgeService is the authorized provider administration and authenticated webhook use-case port.
type ForgeService interface {
	Connect(context.Context, string, string, domain.Secrets) (application.Status, error)
	Status(context.Context, string, string) (application.Status, error)
	Disconnect(context.Context, string, string) error
	RequestSync(context.Context, string, string) error
	Receive(context.Context, string, domain.Delivery) (bool, error)
	MergeRequest(context.Context, string, string, application.MetadataQuery) (application.MRView, error)
	Pipelines(context.Context, string, string, application.MetadataQuery) (application.PipelinePage, error)
	Deployments(context.Context, string, string, application.MetadataQuery) (application.DeploymentPage, error)
}

func registerForge(mux *http.ServeMux, config Config) {
	registerForgeMetadata(mux, config)
	admin := func(permission access.Permission, next func(http.ResponseWriter, *http.Request, access.Principal)) http.HandlerFunc {
		return authorize(config.Authenticator, permission, func(w http.ResponseWriter, r *http.Request, principal access.Principal) {
			if config.Forge == nil {
				writeError(w, 503, "integration_unconfigured", "Provider encryption and allowed endpoints must be configured")
				return
			}
			if r.URL.RawQuery != "" {
				writeError(w, 400, "invalid_request", "Unexpected query parameters")
				return
			}
			next(w, r, principal)
		})
	}
	// The catalog selects the provider; /gitlab remains a compatibility alias.
	for _, route := range []string{"/v1/repositories/{repository}/gitlab", "/v1/repositories/{repository}/forge"} {
		mux.HandleFunc("PUT "+route, admin(access.ManageIntegration, func(w http.ResponseWriter, r *http.Request, principal access.Principal) {
			raw, ok := forgeBody(w, r, 16384)
			if !ok {
				return
			}
			var input domain.Secrets
			if jsonwire.DecodeObject(raw, &input) != nil {
				writeError(w, 400, "invalid_request", "Invalid provider configuration JSON")
				return
			}
			result, err := config.Forge.Connect(r.Context(), principal.UserID, r.PathValue("repository"), input)
			if err != nil {
				forgeError(w, err)
				return
			}
			writeJSON(w, 200, result)
		}))
		mux.HandleFunc("GET "+route, admin(access.ReadIntegration, func(w http.ResponseWriter, r *http.Request, principal access.Principal) {
			result, err := config.Forge.Status(r.Context(), principal.UserID, r.PathValue("repository"))
			if err != nil {
				forgeError(w, err)
				return
			}
			writeJSON(w, 200, result)
		}))
		mux.HandleFunc("DELETE "+route, admin(access.ManageIntegration, func(w http.ResponseWriter, r *http.Request, principal access.Principal) {
			if err := config.Forge.Disconnect(r.Context(), principal.UserID, r.PathValue("repository")); err != nil {
				forgeError(w, err)
				return
			}
			w.WriteHeader(204)
		}))
		mux.HandleFunc("POST "+route+"/sync", admin(access.ManageIntegration, func(w http.ResponseWriter, r *http.Request, principal access.Principal) {
			if err := config.Forge.RequestSync(r.Context(), principal.UserID, r.PathValue("repository")); err != nil {
				forgeError(w, err)
				return
			}
			writeJSON(w, 202, map[string]bool{"scheduled": true})
		}))
	}
	mux.HandleFunc("POST /v1/integrations/gitlab/{repository}/webhook", func(w http.ResponseWriter, r *http.Request) {
		if config.Forge == nil {
			writeError(w, 503, "integration_unconfigured", "Provider integration is not configured")
			return
		}
		if r.URL.RawQuery != "" {
			writeError(w, 400, "invalid_request", "Unexpected webhook query parameters")
			return
		}
		for _, key := range []string{"Webhook-Id", "Webhook-Timestamp", "Webhook-Signature", "X-Gitlab-Token", "Idempotency-Key", "X-Gitlab-Event-UUID"} {
			if len(r.Header.Values(key)) > 1 {
				writeError(w, 401, "invalid_webhook", "Invalid webhook authentication")
				return
			}
		}
		if len(r.Header.Values("X-Gitlab-Event")) != 1 {
			writeError(w, 400, "invalid_request", "One webhook event header is required")
			return
		}
		raw, ok := forgeBody(w, r, 2*1024*1024)
		if !ok {
			return
		}
		id := r.Header.Get("Webhook-Id")
		if id == "" {
			id = r.Header.Get("Idempotency-Key")
		}
		if id == "" {
			id = r.Header.Get("X-Gitlab-Event-UUID")
		}
		delivery := domain.Delivery{ID: id, Timestamp: r.Header.Get("Webhook-Timestamp"), Signature: r.Header.Get("Webhook-Signature"), LegacyToken: r.Header.Get("X-Gitlab-Token"), Event: r.Header.Get("X-Gitlab-Event"), Body: raw}
		created, err := config.Forge.Receive(r.Context(), r.PathValue("repository"), delivery)
		if err != nil {
			forgeError(w, err)
			return
		}
		writeJSON(w, 202, map[string]bool{"accepted": true, "created": created})
	})
}
func forgeBody(w http.ResponseWriter, r *http.Request, limit int64) ([]byte, bool) {
	kind, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || kind != "application/json" {
		writeError(w, 415, "unsupported_media_type", "Use application/json")
		return nil, false
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, 413, "request_too_large", "Request exceeds the byte limit")
		} else {
			writeError(w, 400, "invalid_request", "Cannot read request")
		}
		return nil, false
	}
	return raw, true
}
func forgeError(w http.ResponseWriter, err error) {
	var provider *application.ProviderError
	switch {
	case errors.Is(err, domain.ErrUnauthenticated):
		writeError(w, 401, "invalid_webhook", "Invalid webhook authentication")
	case errors.Is(err, domain.ErrInvalid):
		writeError(w, 422, "invalid_integration", "Invalid integration input")
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, 404, "integration_not_found", "Integration not found")
	case errors.Is(err, domain.ErrConflict):
		writeError(w, 409, "integration_conflict", "Integration identity conflicts with stored state")
	case errors.As(err, &provider):
		if provider.RetryAfter > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(int(provider.RetryAfter/time.Second)))
		}
		writeError(w, 502, "provider_unavailable", "Cannot verify the configured GitLab project")
	default:
		mapError(w, err)
	}
}
