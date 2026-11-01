package domain

import (
	"strconv"
	"time"

	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/identifier"
)

// BrowseCursor is a stable identity boundary, never an authorization grant. IID is decimal text to avoid JSON rounding.
type BrowseCursor struct {
	ObservedAt   *time.Time `json:"observed_at,omitempty"`
	RepositoryID string     `json:"repository_id,omitempty"`
	ID           string     `json:"id,omitempty"`
	IID          string     `json:"iid,omitempty"`
}

// BrowseQuery discovers accepted observations within a project and optional repository.
type BrowseQuery struct {
	Order        string        `json:"order,omitempty"`
	ProjectID    string        `json:"project_id"`
	RepositoryID string        `json:"repository_id,omitempty"`
	Kind         string        `json:"kind"`
	Limit        int           `json:"limit,omitempty"`
	After        *BrowseCursor `json:"after,omitempty"`
}

// NormalizeBrowse rejects ambiguous cursor shapes and takes ownership before use by an adapter.
func NormalizeBrowse(q BrowseQuery) (BrowseQuery, error) {
	if q.Order != "" && q.Order != "recent" || !identifier.Valid(q.ProjectID) || q.RepositoryID != "" && !identifier.Valid(q.RepositoryID) || q.Limit < 0 || q.Limit > 50 {
		return BrowseQuery{}, ErrInvalid
	}
	if q.Kind != "session" && q.Kind != "merge_request" {
		return BrowseQuery{}, ErrInvalid
	}
	if q.After != nil {
		a := *q.After
		if q.Order == "recent" {
			if a.ObservedAt == nil || a.ObservedAt.Year() < 1900 || a.ObservedAt.Year() > 9999 {
				return BrowseQuery{}, ErrInvalid
			}
			owned := *a.ObservedAt
			a.ObservedAt = &owned
		} else if a.ObservedAt != nil {
			return BrowseQuery{}, ErrInvalid
		}
		valid := false
		switch q.Kind {
		case "session":
			valid = identifier.Valid(a.ID) && identifier.Valid(a.RepositoryID) && a.IID == ""
		case "merge_request":
			n, err := strconv.ParseInt(a.IID, 10, 64)
			valid = err == nil && n > 0 && strconv.FormatInt(n, 10) == a.IID && identifier.Valid(a.RepositoryID) && a.ID == ""
		}
		if !valid || q.RepositoryID != "" && a.RepositoryID != q.RepositoryID {
			return BrowseQuery{}, ErrInvalid
		}
		q.After = &a
	}
	if q.Limit == 0 {
		q.Limit = 20
	}
	return q, nil
}

// BrowseAllowed preserves independent capture/integration capabilities even when the user has a project role.
func BrowseAllowed(p access.Principal, kind string) bool {
	return identifier.Valid(p.UserID) && (kind == "session" && p.Has(access.ReadCapture) || kind == "merge_request" && p.Has(access.ReadIntegration))
}

// BrowseEntry contains a current source header and its context selector, without conversation/document bodies.
type BrowseEntry struct {
	Cursor     BrowseCursor `json:"cursor"`
	Query      Query        `json:"query"`
	Title      string       `json:"title"`
	State      string       `json:"state,omitempty"`
	Number     string       `json:"number,omitempty"`
	Agent      string       `json:"agent,omitempty"`
	Segment    int64        `json:"segment,omitempty"`
	Revision   int64        `json:"revision,omitempty"`
	Digest     string       `json:"digest,omitempty"`
	ObservedAt time.Time    `json:"observed_at"`
}

// BrowsePage is a bounded cache inventory, not a complete provider activity feed or coherent remote snapshot.
type BrowsePage struct {
	SchemaVersion int           `json:"schema_version"`
	Query         BrowseQuery   `json:"query"`
	ReadAt        time.Time     `json:"read_at"`
	Items         []BrowseEntry `json:"items"`
	Next          *BrowseCursor `json:"next,omitempty"`
}
