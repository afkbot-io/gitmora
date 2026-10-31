package httpapi

import (
	"context"
	"errors"
	"net/http"
	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/contextpack/domain"
	"workgraph.local/platform/internal/jsonwire"
)

// ContextPackService exposes cached source context without any workflow mutation or model call.
type ContextPackService interface {
	Pack(context.Context, access.Principal, domain.Query) (domain.Pack, error)
}

// ContextBrowseService discovers current authorized source headers independently of provider transports.
type ContextBrowseService interface {
	Browse(context.Context, access.Principal, domain.BrowseQuery) (domain.BrowsePage, error)
}

func registerContextPacks(mux *http.ServeMux, c Config) {
	mux.HandleFunc("POST /v1/context/browse", contextRead(c, func(w http.ResponseWriter, r *http.Request, p access.Principal) {
		if c.ContextBrowser == nil {
			writeError(w, 503, "context_unavailable", "Context browsing is not configured")
			return
		}
		raw, ok := forgeBody(w, r, 8192)
		if !ok {
			return
		}
		var q domain.BrowseQuery
		if jsonwire.DecodeObject(raw, &q) != nil {
			writeError(w, 400, "invalid_request", "Invalid browse query JSON")
			return
		}
		result, err := c.ContextBrowser.Browse(r.Context(), p, q)
		if errors.Is(err, domain.ErrInvalid) {
			writeError(w, 422, "invalid_context_query", "Invalid source browse scope or cursor")
			return
		}
		if err != nil {
			retrievalError(w, err)
			return
		}
		writeJSON(w, 200, result)
	}))
	mux.HandleFunc("POST /v1/context/pack", contextRead(c, func(w http.ResponseWriter, r *http.Request, p access.Principal) {
		if c.ContextPacks == nil {
			writeError(w, 503, "context_unavailable", "Context packs are not configured")
			return
		}
		raw, ok := forgeBody(w, r, 8192)
		if !ok {
			return
		}
		var q domain.Query
		if jsonwire.DecodeObject(raw, &q) != nil {
			writeError(w, 400, "invalid_request", "Invalid context pack query JSON")
			return
		}
		result, err := c.ContextPacks.Pack(r.Context(), p, q)
		if errors.Is(err, domain.ErrInvalid) {
			writeError(w, 422, "invalid_context_query", "Invalid or excessive context pack query")
			return
		}
		if err != nil {
			retrievalError(w, err)
			return
		}
		writeJSON(w, 200, result)
	}))
}
