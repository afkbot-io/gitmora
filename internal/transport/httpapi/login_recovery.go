package httpapi

import (
	"errors"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/identity/domain"
)

// loginHTML requires an explicit HTML preference; wildcard/API clients retain JSON errors.
func loginHTML(c Config, r *http.Request) bool {
	if c.WebUI == nil || r.Method != http.MethodGet || r.URL.Path != "/v1/auth/login" && r.URL.Path != "/v1/auth/oidc/callback" {
		return false
	}
	html, json := 0.0, 0.0
	for _, entry := range strings.Split(strings.Join(r.Header.Values("Accept"), ","), ",") {
		kind, parameters, err := mime.ParseMediaType(strings.TrimSpace(entry))
		if err != nil {
			continue
		}
		q := 1.0
		if value, ok := parameters["q"]; ok {
			q, err = strconv.ParseFloat(value, 64)
			if err != nil || q < 0 || q > 1 {
				continue
			}
		}
		if kind == "text/html" && q > html {
			html = q
		}
		if kind == "application/json" && q > json {
			json = q
		}
	}
	return html > 0 && html > json
}

// loginRecovery redirects only to the configured root and one local, non-sensitive notice.
func loginRecovery(c Config, w http.ResponseWriter, r *http.Request, e error) bool {
	if !loginHTML(c, r) {
		return false
	}
	code := "unavailable"
	if errors.Is(e, domain.ErrLogin) || errors.Is(e, domain.ErrInvalid) || errors.Is(e, access.ErrUnauthenticated) {
		code = "failed"
	} else if errors.Is(e, domain.ErrCapacity) {
		code = "busy"
		w.Header().Set("Retry-After", "60")
	}
	root := "/"
	if c.Identity != nil {
		root = c.Identity.Config().PublicURL + "/"
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, root+"#login="+code, http.StatusSeeOther)
	return true
}

func loginError(c Config, w http.ResponseWriter, r *http.Request, e error) {
	if !loginRecovery(c, w, r, e) {
		identityError(w, e)
	}
}
