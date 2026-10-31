package domain_test

import (
	"errors"
	"testing"

	"workgraph.local/platform/internal/contextpack/domain"
)

func TestBrowseRequiresOneTypedScopeAndOwnsItsCursor(t *testing.T) {
	project := "11111111-1111-4111-8111-111111111111"
	repo := "22222222-2222-4222-8222-222222222222"
	id := "33333333-3333-4333-8333-333333333333"
	q := domain.BrowseQuery{ProjectID: project, Kind: "session", After: &domain.BrowseCursor{RepositoryID: repo, ID: id}}
	got, err := domain.NormalizeBrowse(q)
	if err != nil || got.Limit != 20 {
		t.Fatal(got, err)
	}
	q.After.ID = "changed"
	if got.After.ID != id {
		t.Fatal("cursor aliases caller memory")
	}
	for _, invalid := range []domain.BrowseQuery{
		{ProjectID: project, Kind: "unknown"},
		{ProjectID: project, Kind: "task", RepositoryID: repo},
		{ProjectID: project, Kind: "session", Limit: 51},
		{ProjectID: project, Kind: "session", After: &domain.BrowseCursor{ID: id}},
		{ProjectID: project, Kind: "merge_request", After: &domain.BrowseCursor{RepositoryID: repo, ID: id, IID: "7"}},
		{ProjectID: project, Kind: "task", After: &domain.BrowseCursor{ID: id, IID: "7"}},
		{ProjectID: project, Kind: "merge_request", After: &domain.BrowseCursor{RepositoryID: repo, IID: "9223372036854775808"}},
	} {
		if _, err := domain.NormalizeBrowse(invalid); !errors.Is(err, domain.ErrInvalid) {
			t.Fatal("accepted invalid discovery", invalid, err)
		}
	}
}
