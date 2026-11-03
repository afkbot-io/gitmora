package httpapi

import (
	"context"
	"errors"
	"net/http"

	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/jsonwire"
	"workgraph.local/platform/internal/retrieval/domain"
)

// RetrievalService returns compact source evidence under the caller's unchanged capabilities.
type RetrievalService interface {
	Search(context.Context, access.Principal, domain.Query) (domain.Result, error)
	Expand(context.Context, access.Principal, domain.Expansion) (domain.Expanded, error)
}

func contextRead(config Config, next func(http.ResponseWriter, *http.Request, access.Principal)) http.HandlerFunc {
	return authenticated(config.Authenticator, func(w http.ResponseWriter, r *http.Request, p access.Principal) {
		if !p.Has(access.ReadCapture) && !p.Has(access.ReadIntegration) {
			writeError(w, 403, "forbidden", "A source read capability is required")
			return
		}
		if config.Retrieval == nil {
			writeError(w, 503, "retrieval_unavailable", "Retrieval is not configured")
			return
		}
		if r.URL.RawQuery != "" {
			writeError(w, 400, "invalid_request", "Unexpected query parameters")
			return
		}
		next(w, r, p)
	})
}

func registerRetrieval(mux *http.ServeMux, config Config) {
	registerContextPacks(mux, config)
	mux.HandleFunc("POST /v1/context/search", contextRead(config, func(w http.ResponseWriter, r *http.Request, p access.Principal) {
		raw, ok := forgeBody(w, r, 8192)
		if !ok {
			return
		}
		var query domain.Query
		if jsonwire.DecodeObject(raw, &query) != nil {
			writeError(w, 400, "invalid_request", "Invalid context query JSON")
			return
		}
		result, err := config.Retrieval.Search(r.Context(), p, query)
		if err != nil {
			retrievalError(w, err)
			return
		}
		writeJSON(w, 200, result)
	}))
	mux.HandleFunc("POST /v1/context/expand", contextRead(config, func(w http.ResponseWriter, r *http.Request, p access.Principal) {
		raw, ok := forgeBody(w, r, 2048)
		if !ok {
			return
		}
		var query domain.Expansion
		if jsonwire.DecodeObject(raw, &query) != nil {
			writeError(w, 400, "invalid_request", "Invalid evidence expansion JSON")
			return
		}
		result, err := config.Retrieval.Expand(r.Context(), p, query)
		if err != nil {
			retrievalError(w, err)
			return
		}
		writeJSON(w, 200, result)
	}))
	registerContextMCP(mux, config)
}
func retrievalError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrSearchExpired):
		writeError(w, 409, "search_expired", "Search results expired; start a new search")
	case errors.Is(err, domain.ErrInvalid):
		writeError(w, 422, "invalid_context_query", "Invalid or excessive context request")
	case errors.Is(err, domain.ErrUnavailable):
		writeError(w, 503, "semantic_unavailable", "Semantic search is unavailable; retry or use lexical mode")
	case errors.Is(err, domain.ErrAmbiguous):
		writeError(w, 409, "ambiguous_commit", "Provide a longer commit SHA")
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, 404, "evidence_unavailable", "Evidence not found or unavailable")
	default:
		mapError(w, err)
	}
}
