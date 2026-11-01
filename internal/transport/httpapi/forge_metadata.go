package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/forge/application"
)

func metadataQuery(r *http.Request, mr bool) (application.MetadataQuery, error) {
	query := application.MetadataQuery{Limit: 50}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return query, err
	}
	for key, items := range values {
		if len(items) != 1 || items[0] == "" {
			return query, errors.New("invalid metadata query")
		}
		switch key {
		case "limit":
			query.Limit, err = strconv.Atoi(items[0])
			if err != nil || query.Limit < 1 || query.Limit > 100 {
				return query, errors.New("invalid limit")
			}
		case "after_id":
			if mr {
				return query, errors.New("invalid query")
			}
			query.AfterID, err = strconv.ParseInt(items[0], 10, 64)
			if err != nil || query.AfterID < 0 {
				return query, errors.New("invalid cursor")
			}
		case "after_sha":
			if !mr {
				return query, errors.New("invalid query")
			}
			query.AfterSHA = items[0]
		case "observation":
			if !mr || len(items[0]) != 64 || strings.Trim(items[0], "0123456789abcdef") != "" {
				return query, errors.New("invalid observation")
			}
			query.Observation = items[0]
		default:
			return query, errors.New("unknown metadata query")
		}
	}
	return query, nil
}
func registerForgeMetadata(mux *http.ServeMux, config Config) {
	read := func(next func(http.ResponseWriter, *http.Request, access.Principal)) http.HandlerFunc {
		return authorize(config.Authenticator, access.ReadIntegration, func(w http.ResponseWriter, r *http.Request, p access.Principal) {
			if config.Forge == nil {
				writeError(w, 503, "integration_unconfigured", "Provider integration is not configured")
				return
			}
			next(w, r, p)
		})
	}
	for _, root := range []string{"/v1/repositories/{repository}/gitlab", "/v1/repositories/{repository}/forge"} {
		mux.HandleFunc("GET "+root+"/merge-requests/{iid}", read(func(w http.ResponseWriter, r *http.Request, p access.Principal) {
			query, err := metadataQuery(r, true)
			if err != nil {
				writeError(w, 400, "invalid_request", "Invalid metadata query")
				return
			}
			query.IID, err = strconv.ParseInt(r.PathValue("iid"), 10, 64)
			if err != nil || query.IID < 1 {
				writeError(w, 400, "invalid_request", "Invalid merge request IID")
				return
			}
			result, err := config.Forge.MergeRequest(r.Context(), p.UserID, r.PathValue("repository"), query)
			if err != nil {
				forgeError(w, err)
				return
			}
			writeJSON(w, 200, result)
		}))
		mux.HandleFunc("GET "+root+"/pipelines", read(func(w http.ResponseWriter, r *http.Request, p access.Principal) {
			query, err := metadataQuery(r, false)
			if err != nil {
				writeError(w, 400, "invalid_request", "Invalid metadata query")
				return
			}
			result, err := config.Forge.Pipelines(r.Context(), p.UserID, r.PathValue("repository"), query)
			if err != nil {
				forgeError(w, err)
				return
			}
			writeJSON(w, 200, result)
		}))
		mux.HandleFunc("GET "+root+"/deployments", read(func(w http.ResponseWriter, r *http.Request, p access.Principal) {
			query, err := metadataQuery(r, false)
			if err != nil {
				writeError(w, 400, "invalid_request", "Invalid metadata query")
				return
			}
			result, err := config.Forge.Deployments(r.Context(), p.UserID, r.PathValue("repository"), query)
			if err != nil {
				forgeError(w, err)
				return
			}
			writeJSON(w, 200, result)
		}))
	}
}
