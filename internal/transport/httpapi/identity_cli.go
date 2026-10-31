package httpapi

import (
	"context"
	"errors"
	"net/http"

	identityapp "workgraph.local/platform/internal/identity/application"
	"workgraph.local/platform/internal/identity/domain"
	"workgraph.local/platform/internal/jsonwire"
)

// CLIEnrollmentService exposes hash registration/proof-owned polling and explicit browser-only consent.
type CLIEnrollmentService interface {
	Start(context.Context, domain.CLIRequest) (identityapp.CLIStarted, error)
	Inspect(context.Context, domain.Session, string) (domain.CLIView, error)
	Decide(context.Context, domain.Session, string, string, bool) (domain.CLIView, error)
	Poll(context.Context, string, string, string) (domain.CLIView, error)
	Cancel(context.Context, string, string, string) (domain.CLIView, error)
}

func cliContinuationName(c domain.Config) string {
	if c.Secure() {
		return "__Host-workgraph_cli"
	}
	return "workgraph_cli_dev"
}

// cliContinuation consumes only a canonical public human code; no caller selects a callback destination.
func cliContinuation(w http.ResponseWriter, r *http.Request, c Config) string {
	if c.Identity == nil || c.CLIEnrollment == nil {
		return ""
	}
	config := c.Identity.Config()
	cookies := r.CookiesNamed(cliContinuationName(config))
	if len(cookies) == 0 {
		return ""
	}
	identitySetCookie(w, config, cliContinuationName(config), "", true, -1)
	if len(cookies) != 1 {
		return ""
	}
	code, e := domain.NormalizeCLICode(cookies[0].Value)
	if e != nil {
		return ""
	}
	return code
}

func registerCLIEnrollment(mux *http.ServeMux, c Config) {
	available := func(browser bool, next func(http.ResponseWriter, *http.Request, domain.Session)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if c.Identity == nil || c.CLIEnrollment == nil {
				writeError(w, 503, "cli_login_unavailable", "CLI enrollment is not configured")
				return
			}
			if r.URL.RawQuery != "" {
				cliError(w, domain.ErrInvalid)
				return
			}
			var session domain.Session
			if browser {
				var ok bool
				session, ok = r.Context().Value(browserSessionContextKey{}).(domain.Session)
				if !ok {
					unauthenticated(w)
					return
				}
			}
			next(w, r, session)
		}
	}
	mux.HandleFunc("POST /v1/auth/cli/prepare", available(false, func(w http.ResponseWriter, r *http.Request, _ domain.Session) {
		raw, ok := forgeBody(w, r, 8192)
		if !ok {
			return
		}
		var input domain.CLIRequest
		if jsonwire.DecodeObject(raw, &input) != nil {
			writeError(w, 400, "invalid_request", "Invalid CLI enrollment JSON")
			return
		}
		v, e := c.CLIEnrollment.Start(r.Context(), input)
		if e != nil {
			cliError(w, e)
			return
		}
		writeJSON(w, 202, v)
	}))
	for _, action := range []string{"poll", "cancel"} {
		mux.HandleFunc("POST /v1/auth/cli/"+action, available(false, func(w http.ResponseWriter, r *http.Request, _ domain.Session) {
			raw, ok := forgeBody(w, r, 2048)
			if !ok {
				return
			}
			var input struct {
				ID     string `json:"id"`
				Device string `json:"device_secret"`
				Proof  string `json:"proof"`
			}
			if jsonwire.DecodeObject(raw, &input) != nil {
				writeError(w, 400, "invalid_request", "Invalid CLI polling JSON")
				return
			}
			run := c.CLIEnrollment.Poll
			if action == "cancel" {
				run = c.CLIEnrollment.Cancel
			}
			v, e := run(r.Context(), input.ID, input.Device, input.Proof)
			if e != nil {
				cliError(w, e)
				return
			}
			if v.Status == "pending" {
				w.Header().Set("Retry-After", "2")
			}
			writeJSON(w, 200, v)
		}))
	}
	mux.HandleFunc("POST /v1/auth/cli/inspect", available(true, func(w http.ResponseWriter, r *http.Request, session domain.Session) {
		raw, ok := forgeBody(w, r, 2048)
		if !ok {
			return
		}
		var input struct {
			Code string `json:"user_code"`
		}
		if jsonwire.DecodeObject(raw, &input) != nil {
			writeError(w, 400, "invalid_request", "Invalid CLI inspection JSON")
			return
		}
		v, e := c.CLIEnrollment.Inspect(r.Context(), session, input.Code)
		if e != nil {
			cliError(w, e)
			return
		}
		writeJSON(w, 200, v)
	}))
	mux.HandleFunc("POST /v1/auth/cli/decide", available(true, func(w http.ResponseWriter, r *http.Request, session domain.Session) {
		raw, ok := forgeBody(w, r, 2048)
		if !ok {
			return
		}
		var input struct {
			Code        string `json:"user_code"`
			Fingerprint string `json:"expected_fingerprint"`
			Approve     *bool  `json:"approve"`
		}
		if jsonwire.DecodeObject(raw, &input) != nil || input.Approve == nil {
			writeError(w, 400, "invalid_request", "Invalid CLI decision JSON")
			return
		}
		v, e := c.CLIEnrollment.Decide(r.Context(), session, input.Code, input.Fingerprint, *input.Approve)
		if e != nil {
			cliError(w, e)
			return
		}
		writeJSON(w, 200, v)
	}))
	mux.HandleFunc("GET /v1/auth/cli/login", func(w http.ResponseWriter, r *http.Request) {
		if c.Identity == nil || c.CLIEnrollment == nil {
			writeError(w, 503, "cli_login_unavailable", "CLI enrollment is not configured")
			return
		}
		locale, e := browserLoginLocale(r, true)
		q := r.URL.Query()
		if e != nil || len(q["code"]) != 1 {
			cliError(w, domain.ErrInvalid)
			return
		}
		code, e := domain.NormalizeCLICode(q.Get("code"))
		if e != nil {
			cliError(w, e)
			return
		}
		config := c.Identity.Config()
		identitySetCookie(w, config, cliContinuationName(config), code, true, int(domain.CLIFlowTTL.Seconds()))
		target := config.PublicURL + "/v1/auth/login"
		if locale != "" {
			target += "?locale=" + locale
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
	})
}

func cliError(w http.ResponseWriter, e error) {
	switch {
	case errors.Is(e, domain.ErrLogin):
		writeError(w, 404, "cli_enrollment_unavailable", "CLI request or grant is unavailable")
	case errors.Is(e, domain.ErrFreshLogin):
		writeError(w, 403, "cli_fresh_login_required", "Open the CLI verification link for a fresh browser login")
	case errors.Is(e, domain.ErrConflict):
		writeError(w, 409, "cli_enrollment_conflict", "CLI enrollment changed or was already decided")
	default:
		identityError(w, e)
	}
}
