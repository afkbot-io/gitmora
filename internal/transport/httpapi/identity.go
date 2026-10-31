package httpapi

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"workgraph.local/platform/internal/access"
	directory "workgraph.local/platform/internal/directory/domain"
	"workgraph.local/platform/internal/identity/domain"
	"workgraph.local/platform/internal/jsonwire"
)

// IdentityService supplies browser-only sessions; MCP and explicit Authorization headers retain bearer authentication.
type IdentityService interface {
	Config() domain.Config
	Start(context.Context) (string, string, error)
	StartRegistration(context.Context) (string, string, error)
	Finish(context.Context, string, string, string, string) (domain.Login, error)
	FinishCLI(context.Context, string, string, string, string) (domain.Login, error)
	Cancel(context.Context, string, string) error
	Authenticate(context.Context, string) (domain.Session, error)
	Logout(context.Context, string) error
	Bind(context.Context, access.Principal, domain.BindInput) (directory.Receipt, bool, error)
	Bindings(context.Context, access.Principal, directory.Cursor) (directory.Page[domain.Binding], error)
	ChangeBinding(context.Context, access.Principal, string, domain.BindingChange) (directory.Receipt, bool, error)
}
type browserSessionContextKey struct{}

func identityCookie(r *http.Request, name string) (string, error) {
	values := r.CookiesNamed(name)
	if len(values) != 1 {
		return "", domain.ErrLogin
	}
	return values[0].Value, nil
}
func identitySetCookie(w http.ResponseWriter, c domain.Config, name, value string, httpOnly bool, age int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", Secure: c.Secure(), HttpOnly: httpOnly, SameSite: http.SameSiteLaxMode, MaxAge: age, Expires: time.Now().Add(time.Duration(age) * time.Second)})
}
func identityClear(w http.ResponseWriter, c domain.Config) {
	session, csrf, _ := c.CookieNames()
	identitySetCookie(w, c, session, "", true, -1)
	identitySetCookie(w, c, csrf, "", false, -1)
}

// identityCookies recognizes only same-application cookie requests. No provider/bearer secret is injected into headers.
func identityCookies(c Config, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		path := r.URL.Path
		if c.Identity == nil || len(r.Header.Values("Authorization")) > 0 || !strings.HasPrefix(path, "/v1/") || path == "/v1/auth/login" || path == "/v1/auth/register" || path == "/v1/auth/oidc/callback" || path == "/v1/auth/config" || path == "/v1/auth/cli/login" || path == "/v1/auth/cli/prepare" || path == "/v1/auth/cli/poll" || path == "/v1/auth/cli/cancel" || strings.HasSuffix(path, "/webhook") {
			next.ServeHTTP(w, r)
			return
		}
		config := c.Identity.Config()
		sessionName, csrfName, _ := config.CookieNames()
		cookies := r.CookiesNamed(sessionName)
		if len(cookies) == 0 {
			next.ServeHTTP(w, r)
			return
		}
		if len(cookies) != 1 {
			unauthenticated(w)
			return
		}
		session, e := c.Identity.Authenticate(r.Context(), cookies[0].Value)
		if e != nil {
			identityClear(w, config)
			identityError(w, e)
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" {
			origins := r.Header.Values("Origin")
			headers := r.Header.Values("X-Workgraph-CSRF")
			csrf, e := identityCookie(r, csrfName)
			site := r.Header.Values("Sec-Fetch-Site")
			if len(origins) != 1 || origins[0] != config.PublicURL || len(headers) != 1 || e != nil || len(site) > 1 || len(site) == 1 && site[0] != "same-origin" || domain.SecretHash(domain.CSRFPrefix, headers[0]) == "" || subtle.ConstantTimeCompare([]byte(headers[0]), []byte(csrf)) != 1 || subtle.ConstantTimeCompare([]byte(domain.SecretHash(domain.CSRFPrefix, headers[0])), []byte(session.CSRFHash)) != 1 {
				writeError(w, 403, "csrf_rejected", "A same-origin session CSRF proof is required")
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), browserSessionContextKey{}, session)))
	})
}

func registerIdentity(mux *http.ServeMux, c Config) {
	mux.HandleFunc("GET /v1/auth/config", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			identityError(w, domain.ErrInvalid)
			return
		}
		if c.Identity == nil {
			writeJSON(w, 200, map[string]bool{"enabled": false})
			return
		}
		writeJSON(w, 200, map[string]any{"enabled": true, "issuer": c.Identity.Config().Issuer, "login_path": "/v1/auth/login", "registration_enabled": c.Identity.Config().RegistrationEnabled})
	})
	available := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if c.Identity == nil {
				if loginRecovery(c, w, r, domain.ErrProvider) {
					return
				}
				writeError(w, 503, "identity_unavailable", "OIDC is not configured")
				return
			}
			next(w, r)
		}
	}
	mux.HandleFunc("GET /v1/auth/login", available(func(w http.ResponseWriter, r *http.Request) {
		locale, e := browserLoginLocale(r, false)
		if e != nil {
			loginError(c, w, r, e)
			return
		}
		authorization, browser, e := c.Identity.Start(r.Context())
		if e != nil {
			loginError(c, w, r, e)
			return
		}
		authorization, e = localizedAuthorization(authorization, locale)
		if e != nil {
			loginError(c, w, r, e)
			return
		}
		config := c.Identity.Config()
		_, _, flow := config.CookieNames()
		identitySetCookie(w, config, flow, browser, true, 600)
		http.Redirect(w, r, authorization, http.StatusSeeOther)
	}))
	mux.HandleFunc("GET /v1/auth/register", available(func(w http.ResponseWriter, r *http.Request) {
		locale, e := browserLoginLocale(r, false)
		if e != nil {
			loginError(c, w, r, e)
			return
		}
		if !c.Identity.Config().RegistrationEnabled {
			writeError(w, 403, "registration_disabled", "Registration is disabled; contact your Gitmora administrator")
			return
		}
		authorization, browser, e := c.Identity.StartRegistration(r.Context())
		if e != nil {
			loginError(c, w, r, e)
			return
		}
		authorization, e = localizedAuthorization(authorization, locale)
		if e != nil {
			loginError(c, w, r, e)
			return
		}
		config := c.Identity.Config()
		_, _, flow := config.CookieNames()
		identitySetCookie(w, config, flow, browser, true, 600)
		http.Redirect(w, r, authorization, http.StatusSeeOther)
	}))

	mux.HandleFunc("GET /v1/auth/oidc/callback", available(func(w http.ResponseWriter, r *http.Request) {
		config := c.Identity.Config()
		continuation := cliContinuation(w, r, c)
		sessionName, csrfName, flowName := config.CookieNames()
		identitySetCookie(w, config, flowName, "", true, -1)
		if len(r.URL.RawQuery) > 8192 {
			loginError(c, w, r, domain.ErrInvalid)
			return
		}
		q, e := url.ParseQuery(r.URL.RawQuery)
		if e != nil {
			loginError(c, w, r, domain.ErrLogin)
			return
		}
		for key, values := range q {
			if len(values) != 1 {
				loginError(c, w, r, domain.ErrLogin)
				return
			}
			switch key {
			case "state", "code", "error", "error_description", "error_uri", "iss", "session_state":
			default:
				loginError(c, w, r, domain.ErrLogin)
				return
			}
		}
		if q.Has("iss") && q.Get("iss") != config.Issuer || q.Get("state") == "" || q.Get("code") == "" && q.Get("error") == "" || q.Get("code") != "" && q.Get("error") != "" {
			loginError(c, w, r, domain.ErrLogin)
			return
		}
		browser, e := identityCookie(r, flowName)
		if e != nil {
			loginError(c, w, r, domain.ErrLogin)
			return
		}
		if q.Get("error") != "" {
			_ = c.Identity.Cancel(r.Context(), q.Get("state"), browser)
			loginError(c, w, r, domain.ErrLogin)
			return
		}
		old, _ := identityCookie(r, sessionName)
		var login domain.Login
		if continuation != "" {
			login, e = c.Identity.FinishCLI(r.Context(), q.Get("state"), browser, q.Get("code"), old)
		} else {
			login, e = c.Identity.Finish(r.Context(), q.Get("state"), browser, q.Get("code"), old)
		}
		if e != nil {
			loginError(c, w, r, e)
			return
		}
		age := int(time.Until(login.Session.ExpiresAt).Seconds())
		if age < 1 {
			loginError(c, w, r, domain.ErrLogin)
			return
		}
		if !login.ReusedSession {
			identitySetCookie(w, config, sessionName, login.Cookie, true, age)
			identitySetCookie(w, config, csrfName, login.CSRF, false, age)
		}
		target := config.PublicURL + "/"
		if continuation != "" {
			target += "#cli=" + continuation
		}
		http.Redirect(w, r, target, http.StatusSeeOther)
	}))
	mux.HandleFunc("GET /v1/auth/session", available(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			identityError(w, domain.ErrInvalid)
			return
		}
		session, ok := r.Context().Value(browserSessionContextKey{}).(domain.Session)
		if !ok {
			unauthenticated(w)
			return
		}
		writeJSON(w, 200, struct {
			ID        string    `json:"id"`
			UserID    string    `json:"user_id"`
			ExpiresAt time.Time `json:"expires_at"`
		}{session.ID, session.Principal.UserID, session.ExpiresAt})
	}))
	mux.HandleFunc("POST /v1/auth/logout", available(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			identityError(w, domain.ErrInvalid)
			return
		}
		if _, ok := r.Context().Value(browserSessionContextKey{}).(domain.Session); !ok {
			unauthenticated(w)
			return
		}
		config := c.Identity.Config()
		session, _, _ := config.CookieNames()
		cookie, e := identityCookie(r, session)
		if e == nil {
			e = c.Identity.Logout(r.Context(), cookie)
		}
		identityClear(w, config)
		if e != nil {
			identityError(w, e)
			return
		}
		writeJSON(w, 200, map[string]bool{"logged_out": true})
	}))
	mux.HandleFunc("GET /v1/admin/identities", authenticated(c.Authenticator, func(w http.ResponseWriter, r *http.Request, p access.Principal) {
		if c.Identity == nil {
			writeError(w, 503, "identity_unavailable", "OIDC is not configured")
			return
		}
		cursor, e := directoryCursor(r)
		if e != nil {
			identityError(w, e)
			return
		}
		result, e := c.Identity.Bindings(r.Context(), p, cursor)
		if e != nil {
			identityError(w, e)
			return
		}
		writeJSON(w, 200, result)
	}))
	mux.HandleFunc("POST /v1/admin/identities", authenticated(c.Authenticator, func(w http.ResponseWriter, r *http.Request, p access.Principal) {
		if c.Identity == nil {
			writeError(w, 503, "identity_unavailable", "OIDC is not configured")
			return
		}
		if r.URL.RawQuery != "" {
			identityError(w, domain.ErrInvalid)
			return
		}
		raw, ok := forgeBody(w, r, 2048)
		if !ok {
			return
		}
		var input domain.BindInput
		if jsonwire.DecodeObject(raw, &input) != nil {
			writeError(w, 400, "invalid_request", "Invalid identity binding JSON")
			return
		}
		result, created, e := c.Identity.Bind(r.Context(), p, input)
		if e != nil {
			identityError(w, e)
			return
		}
		status := 200
		if created {
			status = 201
		}
		writeJSON(w, status, result)
	}))
	mux.HandleFunc("PUT /v1/admin/identities/{binding}", authenticated(c.Authenticator, func(w http.ResponseWriter, r *http.Request, p access.Principal) {
		if c.Identity == nil {
			writeError(w, 503, "identity_unavailable", "OIDC is not configured")
			return
		}
		if r.URL.RawQuery != "" {
			identityError(w, domain.ErrInvalid)
			return
		}
		raw, ok := forgeBody(w, r, 2048)
		if !ok {
			return
		}
		var input domain.BindingChange
		if jsonwire.DecodeObject(raw, &input) != nil {
			writeError(w, 400, "invalid_request", "Invalid identity decision JSON")
			return
		}
		result, _, e := c.Identity.ChangeBinding(r.Context(), p, r.PathValue("binding"), input)
		if e != nil {
			identityError(w, e)
			return
		}
		writeJSON(w, 200, result)
	}))
}

func identityError(w http.ResponseWriter, e error) {
	switch {
	case errors.Is(e, access.ErrUnauthenticated):
		unauthenticated(w)
	case errors.Is(e, domain.ErrInvalid):
		writeError(w, 422, "invalid_identity_request", "Invalid identity input")
	case errors.Is(e, domain.ErrLogin):
		writeError(w, 400, "login_not_allowed", "Login proof or local identity mapping is invalid or unavailable")
	case errors.Is(e, domain.ErrProvider):
		writeError(w, 503, "identity_provider_unavailable", "Configured identity provider is unavailable")
	case errors.Is(e, domain.ErrConflict):
		writeError(w, 409, "identity_conflict", "Identity mapping or expected edition conflicts")
	case errors.Is(e, domain.ErrCapacity):
		w.Header().Set("Retry-After", "60")
		writeError(w, 429, "identity_capacity", "Too many pending login flows")
	default:
		directoryError(w, e)
	}
}

// Language selects presentation only; callers cannot choose providers or callback targets.
func browserLoginLocale(r *http.Request, cli bool) (string, error) {
	if len(r.URL.RawQuery) > 128 || r.URL.ForceQuery && r.URL.RawQuery == "" {
		return "", domain.ErrInvalid
	}
	q, e := url.ParseQuery(r.URL.RawQuery)
	if e != nil {
		return "", domain.ErrInvalid
	}
	for name, values := range q {
		if len(values) != 1 || name != "locale" && !(cli && name == "code") {
			return "", domain.ErrInvalid
		}
	}
	if values, ok := q["locale"]; ok {
		if values[0] != "ru" && values[0] != "en" {
			return "", domain.ErrInvalid
		}
		return values[0], nil
	}
	return "", nil
}
func localizedAuthorization(authorization, locale string) (string, error) {
	if locale == "" {
		return authorization, nil
	}
	destination, e := url.Parse(authorization)
	if e != nil || destination.Host == "" {
		return "", domain.ErrProvider
	}
	q := destination.Query()
	q.Set("ui_locales", locale)
	destination.RawQuery = q.Encode()
	return destination.String(), nil
}
