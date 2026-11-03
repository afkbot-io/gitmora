package application

import (
	"context"
	"time"

	"workgraph.local/platform/internal/retrieval/domain"
)

// Embeddings separates local model inference from authorized source storage.
// Profile pins model, dimensions and input formatting; changing it rebuilds only derived vectors.
type Embeddings interface {
	Profile() string
	Embed(context.Context, []string, bool) ([][]float32, error)
}

func (s *Service) prepareSemantic(ctx context.Context, q domain.Query) (domain.Query, bool, error) {
	q.Engine = "lexical"
	// Exact selectors and metadata-only requests never invoke a model.
	if q.Terms == "" || q.CommitPrefix != "" || q.Mode == "lexical" || q.After != nil && (q.After.SearchMode == "" || q.After.SearchMode == "lexical") {
		return q, false, nil
	}
	if s.embeddings == nil {
		if q.Mode == "semantic" || q.After != nil {
			return q, false, domain.ErrUnavailable
		}
		return q, q.Mode == "hybrid", nil
	}
	if q.After != nil && q.After.SemanticProfile != s.embeddings.Profile() {
		return q, false, domain.ErrInvalid
	}
	if q.After != nil {
		q.Engine = q.After.SearchMode
		q.SemanticProfile = q.After.SemanticProfile
		q.VectorDigest = q.After.VectorDigest
		return q, false, nil
	}
	bounded, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	vectors, err := s.embeddings.Embed(bounded, []string{q.Text}, true)
	if err != nil || len(vectors) != 1 {
		if q.Mode == "semantic" || q.After != nil {
			return q, false, domain.ErrUnavailable
		}
		return q, true, nil
	}
	vector, err := domain.NormalizeVector(vectors[0], len(vectors[0]))
	if err != nil {
		return q, false, domain.ErrUnavailable
	}
	q.Vector = vector
	q.VectorDigest = domain.VectorHash(q.Vector)
	q.SemanticProfile = s.embeddings.Profile()
	q.Engine = "hybrid"
	if q.Mode == "semantic" {
		q.Engine = "semantic"
	}
	if q.After != nil && (q.After.VectorDigest != q.VectorDigest || q.After.SearchMode != q.Engine) {
		return q, false, domain.ErrInvalid
	}
	return q, false, nil
}

// EmbeddingLease fences a small batch; no database transaction spans model inference.
type EmbeddingLease struct{ ID, Owner, Content string }
type EmbeddingStore interface {
	ClaimEmbeddings(context.Context, string) ([]EmbeddingLease, error)
	FinishEmbeddings(context.Context, string, []EmbeddingLease, [][]float32, string) error
}

// EmbeddingIndexer adds retryable, optional vectors after ordinary lexical indexing.
type EmbeddingIndexer struct {
	store EmbeddingStore
	model Embeddings
}

func NewEmbeddingIndexer(s EmbeddingStore, e Embeddings) *EmbeddingIndexer {
	return &EmbeddingIndexer{s, e}
}

// ReconcileOnce leaves lexical projections intact when inference fails or its lease expires.
func (w *EmbeddingIndexer) ReconcileOnce(ctx context.Context) (bool, error) {
	batch, err := w.store.ClaimEmbeddings(ctx, w.model.Profile())
	if err != nil || len(batch) == 0 {
		return false, err
	}
	texts := make([]string, len(batch))
	for i, b := range batch {
		texts[i] = b.Content
	}
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	vectors, err := w.model.Embed(bounded, texts, false)
	cancel()
	code := ""
	if err != nil || len(vectors) != len(batch) {
		err = domain.ErrUnavailable
		code = "embedding_unavailable"
		vectors = nil
	}
	if code == "" {
		for i := range vectors {
			vectors[i], err = domain.NormalizeVector(vectors[i], len(vectors[i]))
			if err != nil {
				err = domain.ErrUnavailable
				code = "embedding_unavailable"
				vectors = nil
				break
			}
		}
	}
	finish, done := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer done()
	if err := w.store.FinishEmbeddings(finish, w.model.Profile(), batch, vectors, code); err != nil {
		return true, err
	}
	return true, err
}
