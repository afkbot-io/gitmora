package application

import (
	"context"
	"time"

	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/contextpack/domain"
)

// BrowseReader discovers source headers inside current membership/capability and availability predicates.
type BrowseReader interface {
	Browse(context.Context, access.Principal, domain.BrowseQuery) (domain.BrowsePage, error)
}

// Browser exposes read-only source navigation without provider I/O or workflow mutation.
type Browser struct{ reader BrowseReader }

// NewBrowser injects the source projection; no source or provider mutation port is accepted.
func NewBrowser(reader BrowseReader) *Browser { return &Browser{reader: reader} }

// Browse validates a single typed source scope and keeps the initiating principal's capabilities unchanged.
func (s *Browser) Browse(parent context.Context, p access.Principal, q domain.BrowseQuery) (domain.BrowsePage, error) {
	q, err := domain.NormalizeBrowse(q)
	if err != nil {
		return domain.BrowsePage{}, err
	}
	if !domain.BrowseAllowed(p, q.Kind) {
		return domain.BrowsePage{}, access.ErrDenied
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	return s.reader.Browse(ctx, p, q)
}
