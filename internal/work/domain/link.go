package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"path"
	"strings"

	"workgraph.local/platform/internal/identifier"
	"workgraph.local/platform/internal/redact"
)

// LinkEvidence preserves the exact capture revision, confirmed SHA and guarded MR observation used for linking.
type LinkEvidence struct {
	RepositoryID  string `json:"repository_id"`
	SessionID     string `json:"session_id"`
	Revision      int64  `json:"revision"`
	CaptureDigest string `json:"capture_digest"`
	CommitSHA     string `json:"commit_sha"`
	MRObservation string `json:"mr_observation"`
	MRCodeKey     string `json:"mr_code_key"`
}

// Validate checks canonical evidence identifiers; provider authority is checked by the store separately.
func (e LinkEvidence) Validate() error {
	if !identifier.Valid(e.RepositoryID) || !identifier.Valid(e.SessionID) || e.Revision < 1 || e.Revision > 1_000_000_000 || !identifier.ValidCommit(e.CommitSHA) || !digestPattern.MatchString(e.CaptureDigest) || !digestPattern.MatchString(e.MRObservation) || !digestPattern.MatchString(e.MRCodeKey) {
		return ErrInvalid
	}
	return nil
}

// Digest identifies one immutable local association observation, independent of outbound idempotency.
func (e LinkEvidence) Digest() string {
	raw, _ := json.Marshal(e)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// MRLink is an immutable outbound operation derived from a scoped provider observation.
type MRLink struct {
	RepositoryID string `json:"repository_id"`
	BaseURL      string `json:"provider_base_url"`
	ProjectID    int64  `json:"provider_project_id"`
	IID          int64  `json:"iid"`
	URL          string `json:"url"`
	Title        string `json:"title"`
}

// NewMRLink validates the provider's human URL and derives a stable non-sensitive title.
func NewMRLink(repo, base string, project, iid int64, value string) (MRLink, error) {
	l := MRLink{RepositoryID: repo, BaseURL: base, ProjectID: project, IID: iid, URL: value, Title: fmt.Sprintf("GitLab MR !%d", iid)}
	if l.Validate() != nil {
		return MRLink{}, ErrInvalid
	}
	return l, nil
}

// Validate binds a bounded credential-free link to its provider origin, base path and exact MR IID.
func (l MRLink) Validate() error {
	base, e := url.Parse(l.BaseURL)
	target, err := url.Parse(l.URL)
	root, _ := url.Parse(canonicalLinkURL(l.BaseURL))
	canonical, _ := url.Parse(canonicalLinkURL(l.URL))
	if e != nil || err != nil || !identifier.Valid(l.RepositoryID) || l.ProjectID < 1 || l.IID < 1 || !safeURL(l.BaseURL) || !safeURL(l.URL) || l.Title != fmt.Sprintf("GitLab MR !%d", l.IID) || base.RawQuery != "" || base.Fragment != "" || target.RawQuery != "" || target.Fragment != "" || canonical.Scheme != root.Scheme || canonical.Host != root.Host || strings.Contains(l.URL, "\\") || path.Clean(target.Path) != target.Path || !strings.HasPrefix(target.Path, strings.TrimSuffix(base.Path, "/")+"/") || !strings.HasSuffix(target.Path, fmt.Sprintf("/-/merge_requests/%d", l.IID)) || redact.Text(l.URL) != l.URL {
		return ErrInvalid
	}
	return nil
}

// HasLink recognizes an existing remote URL regardless of a human's chosen label.
func (t Task) HasLink(value string) bool {
	canonical := canonicalLinkURL(value)
	if canonical == "" {
		return false
	}
	for _, l := range t.Links {
		if canonicalLinkURL(l.URL) == canonical {
			return true
		}
	}
	return false
}

func canonicalLinkURL(value string) string {
	if !safeURL(value) {
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
