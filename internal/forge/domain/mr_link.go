package domain

import (
	"fmt"
	"net"
	"net/url"
	"path"
	"strings"
	"unicode/utf8"
	"workgraph.local/platform/internal/endpoint"
	"workgraph.local/platform/internal/identifier"
	"workgraph.local/platform/internal/redact"
)

// MRLink is an immutable outbound operation derived from a scoped provider observation.
type MRLink struct {
	Provider     string `json:"provider,omitempty"`
	RepositoryID string `json:"repository_id"`
	BaseURL      string `json:"provider_base_url"`
	ProjectID    int64  `json:"provider_project_id"`
	IID          int64  `json:"iid"`
	URL          string `json:"url"`
	Title        string `json:"title"`
}

// NewProviderMRLink validates an explicitly selected provider and its exact human MR/PR URL.
func NewProviderMRLink(provider, repo, base string, project, iid int64, value string) (MRLink, error) {
	l := MRLink{Provider: provider, RepositoryID: repo, BaseURL: base, ProjectID: project, IID: iid, URL: value, Title: ProviderMRLinkTitle(provider, iid)}
	if l.Validate() != nil {
		return MRLink{}, ErrInvalid
	}
	return l, nil
}

func ProviderMRLinkTitle(provider string, iid int64) string {
	if provider == "github" {
		return fmt.Sprintf("GitHub PR #%d", iid)
	}
	return fmt.Sprintf("GitLab MR !%d", iid)
}

// Validate binds a bounded credential-free link to its provider origin, base path and exact MR IID.
func (l MRLink) Validate() error {
	if l.Provider != "" && l.Provider != "github" {
		return ErrInvalid
	}
	if l.Provider == "github" || strings.HasPrefix(l.BaseURL, "https://github.com/") {
		canonical, ok := endpoint.GitHubRepository(l.BaseURL)
		if !ok || canonical != l.BaseURL {
			return ErrInvalid
		}
		if !identifier.Valid(l.RepositoryID) || l.ProjectID < 1 || l.IID < 1 || l.Title != fmt.Sprintf("GitHub PR #%d", l.IID) || l.URL != canonical+fmt.Sprintf("/pull/%d", l.IID) || redact.Text(l.URL) != l.URL {
			return ErrInvalid
		}
		return nil
	}
	base, e := url.Parse(l.BaseURL)
	target, err := url.Parse(l.URL)
	root, _ := url.Parse(canonicalLinkURL(l.BaseURL))
	canonical, _ := url.Parse(canonicalLinkURL(l.URL))
	if e != nil || err != nil || !identifier.Valid(l.RepositoryID) || l.ProjectID < 1 || l.IID < 1 || !safeMRURL(l.BaseURL) || !safeMRURL(l.URL) || l.Title != fmt.Sprintf("GitLab MR !%d", l.IID) || base.RawQuery != "" || base.Fragment != "" || target.RawQuery != "" || target.Fragment != "" || canonical.Scheme != root.Scheme || canonical.Host != root.Host || strings.Contains(l.URL, "\\") || path.Clean(target.Path) != target.Path || !strings.HasPrefix(target.Path, strings.TrimSuffix(base.Path, "/")+"/") || !strings.HasSuffix(target.Path, fmt.Sprintf("/-/merge_requests/%d", l.IID)) || redact.Text(l.URL) != l.URL {
		return ErrInvalid
	}
	return nil
}

func canonicalLinkURL(value string) string {
	if !safeMRURL(value) {
		return ""
	}
	u, _ := url.Parse(value)
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if u.Scheme == "https" && port == "443" || u.Scheme == "http" && port == "80" {
		port = ""
	}
	if port != "" {
		u.Host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		u.Host = "[" + host + "]"
	} else {
		u.Host = host
	}
	return u.String()
}

func safeMRURL(s string) bool {
	u, e := url.Parse(s)
	return e == nil && len(s) <= 2000 && u.Host != "" && u.User == nil && u.Opaque == "" && (u.Scheme == "https" || u.Scheme == "http") && utf8.ValidString(s) && !strings.ContainsAny(s, "\x00\r\n")
}
