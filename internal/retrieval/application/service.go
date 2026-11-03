// Package application coordinates authorized compact retrieval and durable source indexing.
package application

import (
	"context"
	"errors"
	"time"

	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/identifier"
	"workgraph.local/platform/internal/retrieval/domain"
)

// Reader checks current membership and source availability before ranking or fetching source text.
type Reader interface {
	Authorize(context.Context, access.Principal, domain.Query) error
	Search(context.Context, access.Principal, domain.Query) (domain.Result, error)
	Get(context.Context, access.Principal, string) (domain.Hit, error)
}

// Service exposes source references and excerpts, never an inference or an instruction to execute.
type Service struct {
	reader     Reader
	embeddings Embeddings
}

// NewService injects the permission-aware projection reader.
func NewService(reader Reader) *Service { return &Service{reader: reader} }

// WithEmbeddings enables local semantic retrieval without changing source/citation authority.
func (s *Service) WithEmbeddings(e Embeddings) *Service { s.embeddings = e; return s }
func authorized(p access.Principal) bool {
	return identifier.Valid(p.UserID) && (p.Has(access.ReadCapture) || p.Has(access.ReadIntegration))
}

// Search validates query budgets and returns only excerpts already authorized by the reader.
func (s *Service) Search(ctx context.Context, p access.Principal, q domain.Query) (domain.Result, error) {
	if !authorized(p) {
		return domain.Result{}, access.ErrDenied
	}
	q, err := domain.NormalizeQuery(q)
	if err != nil {
		return domain.Result{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if q.Terms != "" && (s.embeddings != nil || q.Mode == "semantic") && q.Mode != "lexical" {
		if err := s.reader.Authorize(ctx, p, q); err != nil {
			return domain.Result{}, err
		}
	}
	q, degraded, err := s.prepareSemantic(ctx, q)
	if err != nil {
		return domain.Result{}, err
	}
	result, err := s.reader.Search(ctx, p, q)
	if err != nil {
		return domain.Result{}, err
	}
	result.SemanticUnavailable = degraded
	for i := range result.Items {
		hit := &result.Items[i]
		terms := q.Terms
		if terms == "" && q.PrefixText != "" {
			terms = q.PrefixText
		}
		text, offset := domain.Snippet(hit.Content, terms, 500)
		excerpt, e := domain.Expand(hit.Chunk, domain.Expansion{EvidenceID: hit.ID, Offset: offset, Limit: len([]rune(text))})
		if e != nil {
			return domain.Result{}, e
		}
		hit.Chunk = excerpt.Chunk
	}
	return result, nil
}

// Expand reauthorizes each exact citation and reveals only the requested bounded Unicode range.
func (s *Service) Expand(ctx context.Context, p access.Principal, input domain.Expansion) (domain.Expanded, error) {
	if !authorized(p) {
		return domain.Expanded{}, access.ErrDenied
	}
	if input.Validate() != nil {
		return domain.Expanded{}, domain.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	hit, err := s.reader.Get(ctx, p, input.EvidenceID)
	if err != nil {
		return domain.Expanded{}, err
	}
	expanded, err := domain.Expand(hit.Chunk, input)
	if err != nil {
		return domain.Expanded{}, err
	}
	expanded.Current = hit.Current
	expanded.ObservedAt = hit.ObservedAt
	expanded.IndexedAt = hit.IndexedAt
	expanded.ConfirmedCommits = hit.ConfirmedCommits
	expanded.ConfirmedCount = hit.ConfirmedCount
	return expanded, nil
}

// Lease refers to one immutable source job; mutable source projections are checked separately on reads.
type Lease struct {
	ID, Owner  string
	Source     domain.Source
	ObservedAt time.Time
	OutboxID   int64
}

// Part is a sanitized source part prepared by the persistence anti-corruption adapter.
type Part struct {
	Kind, Name, Title, Content string
	Metadata                   domain.Metadata
	FilePaths                  []string
}

// IndexStore atomically claims sources and persists chunks with their durable acknowledgment.
type IndexStore interface {
	Claim(context.Context) (Lease, bool, error)
	Load(context.Context, Lease) ([]Part, error)
	Finish(context.Context, Lease, []domain.Chunk, string) error
}

// Indexer keeps source parsing and deterministic chunking outside database transactions.
type Indexer struct{ store IndexStore }

// NewIndexer injects the durable source/acknowledgment boundary.
func NewIndexer(store IndexStore) *Indexer { return &Indexer{store} }

// ReconcileOnce processes one fenced source; deterministic IDs make uncertain retries harmless.
func (w *Indexer) ReconcileOnce(parent context.Context) (bool, error) {
	lease, found, err := w.store.Claim(parent)
	if err != nil || !found {
		return found, err
	}
	ctx, cancel := context.WithTimeout(parent, 40*time.Second)
	defer cancel()
	parts, err := w.store.Load(ctx, lease)
	chunks := []domain.Chunk{}
	for _, part := range parts {
		if err != nil {
			break
		}
		var group []domain.Chunk
		group, err = domain.Chunks(lease.Source, part.Kind, part.Name, part.Title, part.Content)
		for i := range group {
			group[i].Metadata = part.Metadata
			if i == 0 {
				group[i].FilePaths = part.FilePaths
				group[i].FilePathCount = len(part.FilePaths)
			}
		}
		chunks = append(chunks, group...)
		if len(chunks) > 8192 {
			err = domain.ErrInvalid
			break
		}
	}
	code := ""
	if err != nil {
		code = "index_source_unavailable"
		if errors.Is(err, domain.ErrInvalid) {
			code = "index_source_invalid"
		}
		chunks = nil
	}
	// Bulk text/vector writes need a separate bounded budget on smaller hosts.
	// The source lease covers both the read and this transaction.
	finish, cancelFinish := context.WithTimeout(context.WithoutCancel(parent), 30*time.Second)
	defer cancelFinish()
	if err := w.store.Finish(finish, lease, chunks, code); err != nil {
		// Preserve the old projection and expose retry health without acknowledging
		// an uncertain write. A committed/lost lease rejects this fenced update.
		retry, cancelRetry := context.WithTimeout(context.WithoutCancel(parent), 5*time.Second)
		defer cancelRetry()
		_ = w.store.Finish(retry, lease, nil, "index_write_failed")
		return true, err
	}
	return true, nil
}
