package domain

import (
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"workgraph.local/platform/internal/identifier"
)

var requestPath = regexp.MustCompile(`/(?:pull|-/merge_requests)/[1-9][0-9]*$`)

// ResolveQuery selects an observed MR/PR by its human URL, without fetching the supplied URL.
type ResolveQuery struct {
	ProjectID string `json:"project_id"`
	URL       string `json:"url"`
}

type Resolved struct {
	Query Query  `json:"query"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

func NormalizeResolve(q ResolveQuery) (ResolveQuery, error) {
	q.URL = strings.TrimSpace(q.URL)
	u, err := url.Parse(q.URL)
	if !identifier.Valid(q.ProjectID) || err != nil || len(q.URL) > 2000 || !utf8.ValidString(q.URL) || strings.ContainsAny(q.URL, "\x00\r\n\\") || u.User != nil || u.Opaque != "" || u.Host == "" || u.RawPath != "" || (u.Scheme != "https" && u.Scheme != "http") {
		return ResolveQuery{}, ErrInvalid
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	if !requestPath.MatchString(u.Path) {
		return ResolveQuery{}, ErrInvalid
	}
	u.RawQuery, u.Fragment, u.RawFragment, u.ForceQuery = "", "", "", false
	u.Host = strings.ToLower(u.Host)
	q.URL = u.String()
	return q, nil
}
