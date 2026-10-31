package httpapi

import (
	"context"
	"net/http"

	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/forge/application"
	"workgraph.local/platform/internal/jsonwire"
)

// CodeService exposes current scoped ingestion health and bounded immutable source quotes.
type CodeService interface {
	Status(context.Context, string, string, string) (application.CodeStatus, error)
	Quote(context.Context, string, string, string, string, int, int) (application.CodeQuote, error)
	Retry(context.Context, string, string, string) error
}
type fileQuoteInput struct {
	CommitSHA string `json:"commit_sha"`
	FilePath  string `json:"file_path"`
	Offset    int    `json:"offset,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

func registerCode(mux *http.ServeMux, config Config) {
	wrap := func(permission access.Permission, next func(http.ResponseWriter, *http.Request, access.Principal)) http.HandlerFunc {
		return authorize(config.Authenticator, permission, func(w http.ResponseWriter, r *http.Request, p access.Principal) {
			if config.Code == nil {
				writeError(w, 503, "integration_unconfigured", "GitLab code ingestion is not configured")
				return
			}
			if r.URL.RawQuery != "" {
				writeError(w, 400, "invalid_request", "Unexpected query parameters")
				return
			}
			next(w, r, p)
		})
	}
	route := "/v1/repositories/{repository}/code"
	mux.HandleFunc("GET "+route+"/commits/{sha}", wrap(access.ReadIntegration, func(w http.ResponseWriter, r *http.Request, p access.Principal) {
		result, err := config.Code.Status(r.Context(), p.UserID, r.PathValue("repository"), r.PathValue("sha"))
		if err != nil {
			forgeError(w, err)
			return
		}
		writeJSON(w, 200, result)
	}))
	mux.HandleFunc("POST "+route+"/commits/{sha}/sync", wrap(access.ManageIntegration, func(w http.ResponseWriter, r *http.Request, p access.Principal) {
		if err := config.Code.Retry(r.Context(), p.UserID, r.PathValue("repository"), r.PathValue("sha")); err != nil {
			forgeError(w, err)
			return
		}
		writeJSON(w, 202, map[string]bool{"scheduled": true})
	}))
	mux.HandleFunc("POST "+route+"/file", wrap(access.ReadIntegration, func(w http.ResponseWriter, r *http.Request, p access.Principal) {
		raw, ok := forgeBody(w, r, 8192)
		if !ok {
			return
		}
		var input fileQuoteInput
		if jsonwire.DecodeObject(raw, &input) != nil {
			writeError(w, 400, "invalid_request", "Invalid pinned file quote JSON")
			return
		}
		result, err := config.Code.Quote(r.Context(), p.UserID, r.PathValue("repository"), input.CommitSHA, input.FilePath, input.Offset, input.Limit)
		if err != nil {
			forgeError(w, err)
			return
		}
		writeJSON(w, 200, result)
	}))
}
