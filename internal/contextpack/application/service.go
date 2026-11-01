// Package application composes permission-scoped source projections and bounded exact excerpts for agents and humans.
package application

import (
	"context"
	"time"
	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/contextpack/domain"
	"workgraph.local/platform/internal/identifier"
	rd "workgraph.local/platform/internal/retrieval/domain"
)

// Reader owns a bounded cross-source read projection; it never changes workflow or performs provider I/O.
type Reader interface {
	Read(context.Context, access.Principal, domain.Query) (domain.Pack, error)
}

// Sources is the existing permission-aware compact lexical retrieval operation.
type Sources interface {
	Search(context.Context, access.Principal, rd.Query) (rd.Result, error)
}

// Service keeps source authority separate from the reason an excerpt was selected into a pack.
type Service struct {
	reader  Reader
	sources Sources
}

// NewService injects authorized source projection and compact evidence operations.
func NewService(reader Reader, sources Sources) *Service { return &Service{reader, sources} }

// Pack returns cached observations plus bounded citations, with no model or provider dependency.
func (s *Service) Pack(parent context.Context, p access.Principal, q domain.Query) (domain.Pack, error) {
	if !identifier.Valid(p.UserID) || !p.Has(access.ReadCapture) && !p.Has(access.ReadIntegration) {
		return domain.Pack{}, access.ErrDenied
	}
	q, err := domain.Normalize(q)
	if err != nil {
		return domain.Pack{}, err
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	result, err := s.reader.Read(ctx, p, q)
	if err != nil {
		return domain.Pack{}, err
	}
	result.Query = q
	result.Evidence = []domain.Evidence{}
	seen := map[string]bool{}
	add := func(h rd.Hit, reason string) {
		if seen[h.ID] {
			return
		}
		seen[h.ID] = true
		if len(result.Evidence) >= q.Limit {
			result.EvidenceMore = true
			return
		}
		result.Evidence = append(result.Evidence, domain.Evidence{Hit: h, Selection: reason})
	}
	search := func(query rd.Query) ([]rd.Hit, error) {
		found, err := s.sources.Search(ctx, p, query)
		if err != nil {
			return nil, err
		}
		result.EvidenceMore = result.EvidenceMore || found.HasMore
		return found.Items, nil
	}
	base := rd.Query{ProjectID: q.ProjectID, RepositoryID: q.RepositoryID, Limit: q.Limit}
	var remaining []rd.Hit
	if q.CommitSHA != "" {
		base.CommitSHA = q.CommitSHA
		found, err := search(base)
		if err != nil {
			return domain.Pack{}, err
		}
		remaining = found
	}
	if p.Has(access.ReadCapture) {
		for _, capture := range result.Captures.Items {
			found, err := search(rd.Query{ProjectID: q.ProjectID, RepositoryID: capture.Source.RepositoryID, SessionID: capture.Source.SessionID, Limit: 1})
			if err != nil {
				return domain.Pack{}, err
			}
			for _, hit := range found {
				reason := "capture_stream"
				add(hit, reason)
			}
		}
	}
	for _, hit := range remaining {
		add(hit, "direct_filter")
	}
	for _, mr := range result.MergeRequests.Items {
		if mr.SourceRepositoryID == "" || mr.Head == "" {
			continue
		}
		found, err := search(rd.Query{ProjectID: q.ProjectID, RepositoryID: mr.SourceRepositoryID, CommitSHA: mr.Head, Limit: 1})
		if err != nil {
			return domain.Pack{}, err
		}
		for _, hit := range found {
			add(hit, "observed_mr_head")
		}
	}
	if err := bound(&result); err != nil {
		return domain.Pack{}, domain.ErrInvalid
	}
	return result, nil
}
