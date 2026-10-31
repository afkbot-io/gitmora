package application_test

import (
	"context"
	"errors"
	"testing"

	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/contextpack/application"
	"workgraph.local/platform/internal/contextpack/domain"
)

type browser struct {
	calls int
	query domain.BrowseQuery
}

func (r *browser) Browse(_ context.Context, _ access.Principal, q domain.BrowseQuery) (domain.BrowsePage, error) {
	r.calls++
	r.query = q
	return domain.BrowsePage{}, nil
}
func TestBrowseDoesNotBroadenIndependentSourceCapabilities(t *testing.T) {
	r := &browser{}
	s := application.NewBrowser(r)
	p := access.Principal{UserID: "33333333-3333-4333-8333-333333333333", Scopes: []access.Permission{access.ReadIntegration}}
	q := domain.BrowseQuery{ProjectID: "11111111-1111-4111-8111-111111111111", Kind: "session"}
	if _, err := s.Browse(context.Background(), p, q); !errors.Is(err, access.ErrDenied) || r.calls != 0 {
		t.Fatal("integration capability exposed sessions", err)
	}
	q.Kind = "task"
	if _, err := s.Browse(context.Background(), p, q); err != nil || r.calls != 1 || r.query.Limit != 20 {
		t.Fatal(r, err)
	}
	q.Kind = "unknown"
	if _, err := s.Browse(context.Background(), p, q); !errors.Is(err, domain.ErrInvalid) || r.calls != 1 {
		t.Fatal("invalid query reached reader", err)
	}
}
