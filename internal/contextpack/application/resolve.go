package application

import (
	"context"
	"time"

	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/contextpack/domain"
	"workgraph.local/platform/internal/identifier"
)

type ResolutionReader interface {
	Resolve(context.Context, access.Principal, domain.ResolveQuery) (domain.Resolved, error)
}

type Resolver struct{ reader ResolutionReader }

func NewResolver(reader ResolutionReader) *Resolver { return &Resolver{reader: reader} }

func (s *Resolver) Resolve(parent context.Context, p access.Principal, q domain.ResolveQuery) (domain.Resolved, error) {
	if !identifier.Valid(p.UserID) || !p.Has(access.ReadIntegration) {
		return domain.Resolved{}, access.ErrDenied
	}
	q, err := domain.NormalizeResolve(q)
	if err != nil {
		return domain.Resolved{}, err
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	return s.reader.Resolve(ctx, p, q)
}
