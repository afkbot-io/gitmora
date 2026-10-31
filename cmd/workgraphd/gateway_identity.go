package main

import (
	"errors"
	"net/http"
	"path"
	"strings"
)

func applicationCookie(name string) bool {
	return strings.HasPrefix(name, "__Host-workgraph_") || strings.HasPrefix(name, "workgraph_")
}

// filterIdentityRequest keeps RP browser authority out of the provider; OIDC Basic/JWT authentication remains valid.
func filterIdentityRequest(request *http.Request) {
	cookies := request.Cookies()
	request.Header.Del("Cookie")
	for _, cookie := range cookies {
		if !applicationCookie(cookie.Name) {
			request.AddCookie(cookie)
		}
	}
	authorization := strings.Fields(request.Header.Get("Authorization"))
	if len(authorization) == 2 && strings.EqualFold(authorization[0], "Bearer") && strings.HasPrefix(authorization[1], "wg_") {
		request.Header.Del("Authorization")
	}
}

// scopeIdentityCookies prevents namespace collisions and confines provider cookies to its host-only path.
// This separates cookie delivery, not the browser origin's trust: both applications are trusted on this origin.
func scopeIdentityCookies(response *http.Response, prefix string) error {
	values := response.Header.Values("Set-Cookie")
	scoped := make([]string, 0, len(values))
	for _, value := range values {
		cookie, err := http.ParseSetCookie(value)
		if err != nil || applicationCookie(cookie.Name) || strings.HasPrefix(cookie.Name, "__Host-") {
			return errors.New("invalid identity cookie namespace")
		}
		cookie.Domain = ""
		if cookie.Path != prefix && !strings.HasPrefix(cookie.Path, prefix+"/") {
			cookie.Path = prefix
		}
		scoped = append(scoped, cookie.String())
	}
	response.Header.Del("Set-Cookie")
	for _, value := range scoped {
		response.Header.Add("Set-Cookie", value)
	}
	return nil
}

func identityPathHandler(next http.Handler, prefix string) http.Handler {
	if prefix == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, prefix+"/") || path.Clean(r.URL.Path) != r.URL.Path && path.Clean(r.URL.Path)+"/" != r.URL.Path || strings.ContainsAny(r.URL.Path, "%\\") {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}
