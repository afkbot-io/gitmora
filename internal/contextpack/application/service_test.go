package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/contextpack/application"
	"workgraph.local/platform/internal/contextpack/domain"
	forge "workgraph.local/platform/internal/forge/domain"
	rd "workgraph.local/platform/internal/retrieval/domain"
)

type reader struct {
	calls int
	pack  domain.Pack
}

func TestLargeValidProviderDetailIsReducedWithExplicitGaps(t *testing.T) {
	r := &reader{pack: domain.Pack{MergeRequests: domain.MRPage{Items: []domain.MR{}}}}
	for range 4 {
		m := domain.MR{Observation: strings.Repeat("a", 64), Head: strings.Repeat("b", 40), Pipelines: []domain.Pipeline{}}
		for range 8 {
			m.Pipelines = append(m.Pipelines, domain.Pipeline{Pipeline: forge.Pipeline{Ref: strings.Repeat("r", 1024), URL: "https://gitlab.internal/" + strings.Repeat("p", 1900)}})
		}
		r.pack.MergeRequests.Items = append(r.pack.MergeRequests.Items, m)
	}
	svc := application.NewService(r, &sources{})
	p := access.Principal{UserID: "33333333-3333-4333-8333-333333333333", Scopes: []access.Permission{access.ReadCapture}}
	got, err := svc.Pack(context.Background(), p, domain.Query{ProjectID: "55555555-5555-4555-8555-555555555555", RepositoryID: "11111111-1111-4111-8111-111111111111", MR: 7})
	raw, _ := json.Marshal(got)
	if err != nil || !got.BudgetLimited || len(raw) > 64*1024 || !got.MergeRequests.Items[0].PipelinesMore || got.MergeRequests.Items[0].Head != strings.Repeat("b", 40) {
		t.Fatal("budget lost source identity/gap", len(raw), err)
	}
}

func (r *reader) Read(context.Context, access.Principal, domain.Query) (domain.Pack, error) {
	r.calls++
	return r.pack, nil
}

type sources struct{ calls int }

func (s *sources) Search(_ context.Context, p access.Principal, q rd.Query) (rd.Result, error) {
	s.calls++
	if !p.Has(access.ReadCapture) {
		return rd.Result{}, access.ErrDenied
	}
	if q.SessionID == "" {
		return rd.Result{Items: []rd.Hit{}}, nil
	}
	return rd.Result{Items: []rd.Hit{{Chunk: rd.Chunk{ID: strings.Repeat("a", 64), Source: rd.Source{Kind: "capture", SessionID: q.SessionID}, Content: "Late window"}}}}, nil
}
func TestStreamExcerptsKeepTheirOwnCommitProvenance(t *testing.T) {
	r := &reader{pack: domain.Pack{Captures: domain.CapturePage{Items: []domain.Capture{{Source: rd.Source{Kind: "capture", RepositoryID: "11111111-1111-4111-8111-111111111111", SessionID: "22222222-2222-4222-8222-222222222222"}, ChainVerified: true}}}}}
	s := &sources{}
	svc := application.NewService(r, s)
	p := access.Principal{UserID: "33333333-3333-4333-8333-333333333333", Scopes: []access.Permission{access.ReadCapture}}
	q := domain.Query{ProjectID: "55555555-5555-4555-8555-555555555555", RepositoryID: "11111111-1111-4111-8111-111111111111", CommitSHA: strings.Repeat("b", 40)}
	got, err := svc.Pack(context.Background(), p, q)
	if err != nil || len(got.Evidence) != 0 {
		t.Fatal(got, err)
	}
	p.Scopes = []access.Permission{access.WriteCapture}
	if _, err := svc.Pack(context.Background(), p, q); !errors.Is(err, access.ErrDenied) || r.calls != 1 {
		t.Fatal("write-only credential invoked reader", err)
	}
}
