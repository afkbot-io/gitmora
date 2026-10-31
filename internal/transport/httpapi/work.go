package httpapi

import (
	"context"
	"errors"
	"net/http"

	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/jsonwire"
	"workgraph.local/platform/internal/work/application"
	"workgraph.local/platform/internal/work/domain"
)

// WorkService exposes country-scoped integration operations without provider transport details.
type WorkService interface {
	Connect(context.Context, string, string, domain.Config) (application.Status, error)
	Status(context.Context, string, string) (application.Status, error)
	Disconnect(context.Context, string, string) error
	Refresh(context.Context, string, string, string) error
	Task(context.Context, string, string, string) (application.TaskView, error)
	QueuePublication(context.Context, string, string, application.PublishInput) (application.Publication, error)
	Publication(context.Context, string, string, string) (application.Publication, error)
	Links(context.Context, string, string, string) (application.LinkPage, error)
}

func registerWork(mux *http.ServeMux, config Config) {
	wrap := func(permission access.Permission, next func(http.ResponseWriter, *http.Request, access.Principal)) http.HandlerFunc {
		return authorize(config.Authenticator, permission, func(w http.ResponseWriter, r *http.Request, p access.Principal) {
			if config.Work == nil {
				writeError(w, 503, "integration_unconfigured", "Tasktopia encryption and allowed endpoints must be configured")
				return
			}
			if r.URL.RawQuery != "" {
				writeError(w, 400, "invalid_request", "Unexpected query parameters")
				return
			}
			next(w, r, p)
		})
	}
	route := "/v1/projects/{project}/tasktopia"
	mux.HandleFunc("GET "+route+"/tasks/{task}/links", wrap(access.ReadIntegration, func(w http.ResponseWriter, r *http.Request, p access.Principal) {
		result, err := config.Work.Links(r.Context(), p.UserID, r.PathValue("project"), r.PathValue("task"))
		if err != nil {
			workError(w, err)
			return
		}
		if !p.Has(access.ReadCapture) {
			for i := range result.Items {
				result.Items[i].Evidence = nil
			}
		}
		writeJSON(w, 200, result)
	}))
	mux.HandleFunc("PUT "+route, wrap(access.ManageIntegration, func(w http.ResponseWriter, r *http.Request, p access.Principal) {
		raw, ok := forgeBody(w, r, 16384)
		if !ok {
			return
		}
		var input domain.Config
		if jsonwire.DecodeObject(raw, &input) != nil {
			writeError(w, 400, "invalid_request", "Invalid Tasktopia configuration JSON")
			return
		}
		result, err := config.Work.Connect(r.Context(), p.UserID, r.PathValue("project"), input)
		if err != nil {
			workError(w, err)
			return
		}
		writeJSON(w, 200, result)
	}))
	mux.HandleFunc("GET "+route, wrap(access.ReadIntegration, func(w http.ResponseWriter, r *http.Request, p access.Principal) {
		result, err := config.Work.Status(r.Context(), p.UserID, r.PathValue("project"))
		if err != nil {
			workError(w, err)
			return
		}
		writeJSON(w, 200, result)
	}))
	mux.HandleFunc("DELETE "+route, wrap(access.ManageIntegration, func(w http.ResponseWriter, r *http.Request, p access.Principal) {
		if err := config.Work.Disconnect(r.Context(), p.UserID, r.PathValue("project")); err != nil {
			workError(w, err)
			return
		}
		w.WriteHeader(204)
	}))
	mux.HandleFunc("POST "+route+"/tasks/{task}/sync", wrap(access.ManageIntegration, func(w http.ResponseWriter, r *http.Request, p access.Principal) {
		if err := config.Work.Refresh(r.Context(), p.UserID, r.PathValue("project"), r.PathValue("task")); err != nil {
			workError(w, err)
			return
		}
		writeJSON(w, 202, map[string]bool{"scheduled": true})
	}))
	mux.HandleFunc("GET "+route+"/tasks/{task}", wrap(access.ReadIntegration, func(w http.ResponseWriter, r *http.Request, p access.Principal) {
		result, err := config.Work.Task(r.Context(), p.UserID, r.PathValue("project"), r.PathValue("task"))
		if err != nil {
			workError(w, err)
			return
		}
		writeJSON(w, 200, result)
	}))
	mux.HandleFunc("POST "+route+"/publications", wrap(access.ManageIntegration, func(w http.ResponseWriter, r *http.Request, p access.Principal) {
		if !p.Has(access.ReadCapture) {
			writeError(w, 403, "forbidden", "Capture read capability is required for evidence publication")
			return
		}
		raw, ok := forgeBody(w, r, 2048)
		if !ok {
			return
		}
		var input application.PublishInput
		if jsonwire.DecodeObject(raw, &input) != nil {
			writeError(w, 400, "invalid_request", "Invalid evidence publication JSON")
			return
		}
		result, err := config.Work.QueuePublication(r.Context(), p.UserID, r.PathValue("project"), input)
		if err != nil {
			workError(w, err)
			return
		}
		writeJSON(w, 202, result)
	}))
	mux.HandleFunc("GET "+route+"/publications/{publication}", wrap(access.ReadIntegration, func(w http.ResponseWriter, r *http.Request, p access.Principal) {
		if !p.Has(access.ReadCapture) {
			writeError(w, 403, "forbidden", "Capture read capability is required for publication evidence")
			return
		}
		result, err := config.Work.Publication(r.Context(), p.UserID, r.PathValue("project"), r.PathValue("publication"))
		if err != nil {
			workError(w, err)
			return
		}
		writeJSON(w, 200, result)
	}))
}
func workError(w http.ResponseWriter, err error) {
	var provider *application.ProviderError
	switch {
	case errors.Is(err, domain.ErrInvalid):
		writeError(w, 422, "invalid_task_integration", "Invalid Tasktopia input")
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, 404, "task_integration_not_found", "Task integration resource not found")
	case errors.Is(err, domain.ErrConflict):
		writeError(w, 409, "task_integration_conflict", "Task scope conflicts or provider-confirmed evidence is unavailable")
	case errors.As(err, &provider):
		writeError(w, 502, "provider_unavailable", "Cannot verify Tasktopia access")
	default:
		mapError(w, err)
	}
}
