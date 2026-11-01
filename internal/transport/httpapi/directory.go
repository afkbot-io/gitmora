package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/directory/domain"
	forgeapp "workgraph.local/platform/internal/forge/application"
	"workgraph.local/platform/internal/jsonwire"
)

// DirectoryService exposes catalog/account metadata; content operations retain their own authorities.
type DirectoryService interface {
	LookupRepository(context.Context, access.Principal, string, string) (domain.ResolvedRepository, error)
	LookupRepositoryWithToken(context.Context, access.Principal, string, string, string, string) (domain.ResolvedRepository, error)
	Apply(context.Context, access.Principal, domain.Command) (domain.Receipt, bool, error)
	Me(context.Context, access.Principal) (domain.Me, error)
	Projects(context.Context, access.Principal, domain.Cursor) (domain.Page[domain.Project], error)
	Project(context.Context, access.Principal, string) (domain.Project, error)
	Repositories(context.Context, access.Principal, string, domain.Cursor) (domain.Page[domain.Repository], error)
	Repository(context.Context, access.Principal, string) (domain.Repository, error)
	Members(context.Context, access.Principal, string, domain.Cursor) (domain.Page[domain.Member], error)
	ResolveMember(context.Context, access.Principal, string, string) (domain.MemberCandidate, error)
	Users(context.Context, access.Principal, domain.Cursor) (domain.Page[domain.User], error)
	User(context.Context, access.Principal, string) (domain.User, error)
	Events(context.Context, access.Principal, domain.EventQuery) (domain.EventPage, error)
	Tokens(context.Context, access.Principal, domain.Cursor) (domain.Page[domain.Token], error)
	IssueToken(context.Context, access.Principal, domain.TokenInput) (domain.IssuedToken, error)
}

func directoryAuthorized(c Config, next func(http.ResponseWriter, *http.Request, access.Principal)) http.HandlerFunc {
	return authenticated(c.Authenticator, func(w http.ResponseWriter, r *http.Request, p access.Principal) {
		if c.Directory == nil {
			writeError(w, 503, "directory_unavailable", "Directory is not configured")
			return
		}
		next(w, r, p)
	})
}

type directoryCreate struct {
	RequestID string  `json:"request_id"`
	ID        string  `json:"id"`
	Name      *string `json:"name"`
}
type directoryVersioned struct {
	RequestID       string `json:"request_id"`
	ExpectedVersion int64  `json:"expected_version"`
}
type directoryRename struct {
	directoryVersioned
	Name *string `json:"name"`
}
type directoryRepository struct {
	directoryCreate
	GitLabURL            string `json:"gitlab_url"`
	GitLabProjectID      int64  `json:"gitlab_project_id"`
	Provider             string `json:"provider"`
	ProviderURL          string `json:"provider_url"`
	ProviderRepositoryID int64  `json:"provider_repository_id"`
}
type directoryUserCreate struct {
	RequestID   string  `json:"request_id"`
	ID          string  `json:"id"`
	DisplayName *string `json:"display_name"`
}

type directoryUserUpdate struct {
	directoryVersioned
	DisplayName *string `json:"display_name,omitempty"`
	Enabled     *bool   `json:"enabled,omitempty"`
}
type directoryRole struct {
	directoryVersioned
	Role string `json:"role"`
}
type directoryOperator struct {
	directoryVersioned
	Operator *bool `json:"operator"`
}

func directoryMutation[T any](c Config, build func(*http.Request, T) domain.Command) http.HandlerFunc {
	return directoryAuthorized(c, func(w http.ResponseWriter, r *http.Request, p access.Principal) {
		if r.URL.RawQuery != "" {
			directoryError(w, domain.ErrInvalid)
			return
		}
		raw, ok := forgeBody(w, r, 8192)
		if !ok {
			return
		}
		var input T
		if jsonwire.DecodeObject(raw, &input) != nil {
			writeError(w, 400, "invalid_request", "Invalid directory command JSON")
			return
		}
		command := build(r, input)
		receipt, created, e := c.Directory.Apply(r.Context(), p, command)
		if e != nil {
			directoryError(w, e)
			return
		}
		status := 200
		if created && (command.Action == domain.CreateProject || command.Action == domain.CreateRepository || command.Action == domain.CreateUser) {
			status = 201
		}
		writeJSON(w, status, receipt)
	})
}
func directoryView[T any](c Config, query bool, read func(context.Context, access.Principal, *http.Request) (T, error)) http.HandlerFunc {
	return directoryAuthorized(c, func(w http.ResponseWriter, r *http.Request, p access.Principal) {
		if !query && r.URL.RawQuery != "" {
			directoryError(w, domain.ErrInvalid)
			return
		}
		result, e := read(r.Context(), p, r)
		if e != nil {
			directoryError(w, e)
			return
		}
		writeJSON(w, 200, result)
	})
}

func directoryCursor(r *http.Request) (domain.Cursor, error) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return domain.Cursor{}, domain.ErrInvalid
	}
	for key, values := range q {
		if (key != "after_id" && key != "limit") || len(values) != 1 {
			return domain.Cursor{}, domain.ErrInvalid
		}
	}
	c := domain.Cursor{AfterID: q.Get("after_id")}
	if q.Has("limit") {
		n, e := strconv.Atoi(q.Get("limit"))
		if e != nil {
			return c, domain.ErrInvalid
		}
		c.Limit = n
	}
	return c.Normalize()
}

func registerDirectory(mux *http.ServeMux, c Config) {
	mux.HandleFunc("PUT /v1/me/profile", directoryAuthorized(c, func(w http.ResponseWriter, r *http.Request, p access.Principal) {
		if r.URL.RawQuery != "" {
			directoryError(w, domain.ErrInvalid)
			return
		}
		raw, ok := forgeBody(w, r, 2048)
		if !ok {
			return
		}
		var in struct {
			directoryVersioned
			Username string `json:"username"`
		}
		if jsonwire.DecodeObject(raw, &in) != nil {
			writeError(w, 400, "invalid_request", "Invalid profile")
			return
		}
		receipt, _, e := c.Directory.Apply(r.Context(), p, domain.Command{RequestID: in.RequestID, Action: domain.SetUsername, EntityID: p.UserID, ExpectedVersion: in.ExpectedVersion, Username: in.Username})
		if e != nil {
			directoryError(w, e)
			return
		}
		writeJSON(w, 200, receipt)
	}))

	mux.HandleFunc("POST /v1/projects/{project}/members/resolve", directoryAuthorized(c, func(w http.ResponseWriter, r *http.Request, p access.Principal) {
		var in struct {
			Identifier string `json:"identifier"`
		}
		if r.URL.RawQuery != "" {
			directoryError(w, domain.ErrInvalid)
			return
		}
		raw, ok := forgeBody(w, r, 1024)
		if !ok {
			return
		}
		if jsonwire.DecodeObject(raw, &in) != nil {
			writeError(w, 400, "invalid_request", "Invalid member lookup")
			return
		}
		value, e := c.Directory.ResolveMember(r.Context(), p, r.PathValue("project"), in.Identifier)
		if e != nil {
			directoryError(w, e)
			return
		}
		writeJSON(w, 200, value)
	}))

	mux.HandleFunc("GET /v1/me", directoryView(c, false, func(ctx context.Context, p access.Principal, _ *http.Request) (domain.Me, error) {
		return c.Directory.Me(ctx, p)
	}))
	mux.HandleFunc("GET /v1/projects", directoryView(c, true, func(ctx context.Context, p access.Principal, r *http.Request) (domain.Page[domain.Project], error) {
		q, e := directoryCursor(r)
		if e != nil {
			return domain.Page[domain.Project]{}, e
		}
		return c.Directory.Projects(ctx, p, q)
	}))
	mux.HandleFunc("GET /v1/projects/{project}", directoryView(c, false, func(ctx context.Context, p access.Principal, r *http.Request) (domain.Project, error) {
		return c.Directory.Project(ctx, p, r.PathValue("project"))
	}))
	mux.HandleFunc("GET /v1/projects/{project}/repositories", directoryView(c, true, func(ctx context.Context, p access.Principal, r *http.Request) (domain.Page[domain.Repository], error) {
		q, e := directoryCursor(r)
		if e != nil {
			return domain.Page[domain.Repository]{}, e
		}
		return c.Directory.Repositories(ctx, p, r.PathValue("project"), q)
	}))
	mux.HandleFunc("GET /v1/repositories/{repository}", directoryView(c, false, func(ctx context.Context, p access.Principal, r *http.Request) (domain.Repository, error) {
		return c.Directory.Repository(ctx, p, r.PathValue("repository"))
	}))
	mux.HandleFunc("GET /v1/projects/{project}/members", directoryView(c, true, func(ctx context.Context, p access.Principal, r *http.Request) (domain.Page[domain.Member], error) {
		q, e := directoryCursor(r)
		if e != nil {
			return domain.Page[domain.Member]{}, e
		}
		return c.Directory.Members(ctx, p, r.PathValue("project"), q)
	}))
	mux.HandleFunc("GET /v1/admin/users", directoryView(c, true, func(ctx context.Context, p access.Principal, r *http.Request) (domain.Page[domain.User], error) {
		q, e := directoryCursor(r)
		if e != nil {
			return domain.Page[domain.User]{}, e
		}
		return c.Directory.Users(ctx, p, q)
	}))
	mux.HandleFunc("GET /v1/admin/users/{user}", directoryView(c, false, func(ctx context.Context, p access.Principal, r *http.Request) (domain.User, error) {
		return c.Directory.User(ctx, p, r.PathValue("user"))
	}))
	mux.HandleFunc("GET /v1/me/tokens", directoryView(c, true, func(ctx context.Context, p access.Principal, r *http.Request) (domain.Page[domain.Token], error) {
		q, e := directoryCursor(r)
		if e != nil {
			return domain.Page[domain.Token]{}, e
		}
		return c.Directory.Tokens(ctx, p, q)
	}))
	mux.HandleFunc("GET /v1/directory/events", directoryView(c, true, func(ctx context.Context, p access.Principal, r *http.Request) (domain.EventPage, error) {
		values, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			return domain.EventPage{}, domain.ErrInvalid
		}
		for key, v := range values {
			if (key != "project_id" && key != "after_sequence" && key != "limit") || len(v) != 1 {
				return domain.EventPage{}, domain.ErrInvalid
			}
		}
		q := domain.EventQuery{ProjectID: values.Get("project_id")}
		var e error
		if values.Has("after_sequence") {
			q.AfterSequence, e = strconv.ParseInt(values.Get("after_sequence"), 10, 64)
			if e != nil {
				return domain.EventPage{}, domain.ErrInvalid
			}
		}
		if values.Has("limit") {
			q.Limit, e = strconv.Atoi(values.Get("limit"))
			if e != nil {
				return domain.EventPage{}, domain.ErrInvalid
			}
		}
		return c.Directory.Events(ctx, p, q)
	}))
	mux.HandleFunc("POST /v1/projects", directoryMutation(c, func(_ *http.Request, i directoryCreate) domain.Command {
		return domain.Command{RequestID: i.RequestID, Action: domain.CreateProject, EntityID: i.ID, Name: i.Name}
	}))
	mux.HandleFunc("PATCH /v1/projects/{project}", directoryMutation(c, func(r *http.Request, i directoryRename) domain.Command {
		return domain.Command{RequestID: i.RequestID, Action: domain.RenameProject, EntityID: r.PathValue("project"), ExpectedVersion: i.ExpectedVersion, Name: i.Name}
	}))
	mux.HandleFunc("DELETE /v1/projects/{project}", directoryMutation(c, func(r *http.Request, i directoryVersioned) domain.Command {
		return domain.Command{RequestID: i.RequestID, Action: domain.DeleteProject, EntityID: r.PathValue("project"), ExpectedVersion: i.ExpectedVersion}
	}))
	mux.HandleFunc("POST /v1/projects/{project}/repositories", directoryMutation(c, func(r *http.Request, i directoryRepository) domain.Command {
		return domain.Command{RequestID: i.RequestID, Action: domain.CreateRepository, ProjectID: r.PathValue("project"), EntityID: i.ID, Name: i.Name, GitLabURL: i.GitLabURL, GitLabProjectID: i.GitLabProjectID, Provider: i.Provider, ProviderURL: i.ProviderURL, ProviderRepositoryID: i.ProviderRepositoryID}
	}))
	mux.HandleFunc("POST /v1/projects/{project}/repositories/lookup", directoryAuthorized(c, func(w http.ResponseWriter, r *http.Request, p access.Principal) {
		if r.URL.RawQuery != "" {
			directoryError(w, domain.ErrInvalid)
			return
		}
		raw, ok := forgeBody(w, r, 8192)
		if !ok {
			return
		}
		var input struct {
			URL      string `json:"url"`
			Provider string `json:"provider"`
			Token    string `json:"api_token"`
		}
		if jsonwire.DecodeObject(raw, &input) != nil {
			directoryError(w, domain.ErrInvalid)
			return
		}
		var result domain.ResolvedRepository
		var err error
		if input.Provider != "" {
			result, err = c.Directory.LookupRepositoryWithToken(r.Context(), p, r.PathValue("project"), input.URL, input.Provider, input.Token)
		} else {
			result, err = c.Directory.LookupRepository(r.Context(), p, r.PathValue("project"), input.URL)
		}
		if err != nil {
			var provider *forgeapp.ProviderError
			if errors.As(err, &provider) {
				forgeError(w, err)
			} else {
				directoryError(w, err)
			}
			return
		}
		writeJSON(w, 200, result)
	}))
	mux.HandleFunc("PATCH /v1/projects/{project}/repositories/{repository}", directoryMutation(c, func(r *http.Request, i directoryRename) domain.Command {
		return domain.Command{RequestID: i.RequestID, Action: domain.RenameRepository, ProjectID: r.PathValue("project"), EntityID: r.PathValue("repository"), ExpectedVersion: i.ExpectedVersion, Name: i.Name}
	}))
	mux.HandleFunc("DELETE /v1/projects/{project}/repositories/{repository}", directoryMutation(c, func(r *http.Request, i directoryVersioned) domain.Command {
		return domain.Command{RequestID: i.RequestID, Action: domain.DeleteRepository, ProjectID: r.PathValue("project"), EntityID: r.PathValue("repository"), ExpectedVersion: i.ExpectedVersion}
	}))
	mux.HandleFunc("PUT /v1/projects/{project}/members/{user}", directoryMutation(c, func(r *http.Request, i directoryRole) domain.Command {
		return domain.Command{RequestID: i.RequestID, Action: domain.SetMember, ProjectID: r.PathValue("project"), EntityID: r.PathValue("user"), ExpectedVersion: i.ExpectedVersion, Role: i.Role}
	}))
	mux.HandleFunc("DELETE /v1/projects/{project}/members/{user}", directoryMutation(c, func(r *http.Request, i directoryVersioned) domain.Command {
		return domain.Command{RequestID: i.RequestID, Action: domain.RemoveMember, ProjectID: r.PathValue("project"), EntityID: r.PathValue("user"), ExpectedVersion: i.ExpectedVersion}
	}))
	mux.HandleFunc("POST /v1/admin/users", directoryMutation(c, func(_ *http.Request, i directoryUserCreate) domain.Command {
		return domain.Command{RequestID: i.RequestID, Action: domain.CreateUser, EntityID: i.ID, Name: i.DisplayName}
	}))
	mux.HandleFunc("PATCH /v1/admin/users/{user}", directoryMutation(c, func(r *http.Request, i directoryUserUpdate) domain.Command {
		return domain.Command{RequestID: i.RequestID, Action: domain.UpdateUser, EntityID: r.PathValue("user"), ExpectedVersion: i.ExpectedVersion, Name: i.DisplayName, Enabled: i.Enabled}
	}))
	mux.HandleFunc("DELETE /v1/admin/users/{user}", directoryMutation(c, func(r *http.Request, i directoryVersioned) domain.Command {
		return domain.Command{RequestID: i.RequestID, Action: domain.DeleteUser, EntityID: r.PathValue("user"), ExpectedVersion: i.ExpectedVersion}
	}))
	mux.HandleFunc("PUT /v1/admin/users/{user}/operator", directoryMutation(c, func(r *http.Request, i directoryOperator) domain.Command {
		return domain.Command{RequestID: i.RequestID, Action: domain.SetOperator, EntityID: r.PathValue("user"), ExpectedVersion: i.ExpectedVersion, Operator: i.Operator}
	}))
	mux.HandleFunc("POST /v1/me/tokens/{token}/revoke", directoryMutation(c, func(r *http.Request, i struct {
		RequestID string `json:"request_id"`
	}) domain.Command {
		return domain.Command{RequestID: i.RequestID, Action: domain.RevokeToken, EntityID: r.PathValue("token")}
	}))
	mux.HandleFunc("POST /v1/me/tokens", directoryAuthorized(c, func(w http.ResponseWriter, r *http.Request, p access.Principal) {
		if r.URL.RawQuery != "" {
			directoryError(w, domain.ErrInvalid)
			return
		}
		raw, ok := forgeBody(w, r, 8192)
		if !ok {
			return
		}
		var input domain.TokenInput
		if jsonwire.DecodeObject(raw, &input) != nil {
			writeError(w, 400, "invalid_request", "Invalid token request JSON")
			return
		}
		result, e := c.Directory.IssueToken(r.Context(), p, input)
		if e != nil {
			directoryError(w, e)
			return
		}
		status := 200
		if result.Created {
			status = 201
		}
		writeJSON(w, status, result)
	}))
}

func directoryError(w http.ResponseWriter, e error) {
	switch {
	case errors.Is(e, domain.ErrProviderUnavailable):
		writeError(w, 503, "repository_lookup_unavailable", "Repository lookup is unavailable")
	case errors.Is(e, domain.ErrInvalid):
		writeError(w, 422, "invalid_directory_request", "Invalid directory input or pagination")
	case errors.Is(e, domain.ErrNotFound):
		writeError(w, 404, "directory_entry_unavailable", "Directory entry unavailable")
	case errors.Is(e, domain.ErrRetired):
		writeError(w, 410, "directory_identity_retired", "Deleted identifiers cannot be reused")
	case errors.Is(e, domain.ErrUsernameTaken):
		writeError(w, 409, "username_taken", "Username already taken")
	case errors.Is(e, domain.ErrConflict):
		writeError(w, 409, "directory_conflict", "Version, request identity or catalog identity conflicts")
	case errors.Is(e, domain.ErrLastAdmin):
		writeError(w, 409, "last_project_administrator", "Assign another enabled project administrator first")
	case errors.Is(e, domain.ErrLastOperator):
		writeError(w, 409, "last_installation_operator", "Assign another enabled installation operator first")
	case errors.Is(e, domain.ErrDelegation):
		writeError(w, 403, "credential_delegation_denied", "Requested credential exceeds parent grant, lifetime or delegation depth")
	default:
		retrievalError(w, e)
	}
}
