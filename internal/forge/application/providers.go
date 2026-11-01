package application

import (
	"context"
	"workgraph.local/platform/internal/forge/domain"
)

// Adapter supplies read-only metadata and immutable code from one configured provider.
type Adapter interface {
	Provider
	CodeProvider
}

// Providers routes on immutable catalog identity, never caller paths or webhook payloads.
type Providers struct{ GitLab, GitHub Adapter }

func (p Providers) selectProvider(repo domain.Repository) (Adapter, error) {
	var adapter Adapter
	switch repo.Provider {
	case "":
		adapter = p.GitLab
	case "github":
		adapter = p.GitHub
	default:
		return nil, domain.ErrInvalid
	}
	if adapter == nil {
		return nil, &ProviderError{Code: "provider_endpoint_not_allowed"}
	}
	return adapter, nil
}
func (p Providers) VerifyProject(ctx context.Context, r domain.Repository, t string) error {
	a, e := p.selectProvider(r)
	if e != nil {
		return e
	}
	return a.VerifyProject(ctx, r, t)
}
func (p Providers) ConfirmCommit(ctx context.Context, r domain.Repository, t, s string) (bool, error) {
	a, e := p.selectProvider(r)
	if e != nil {
		return false, e
	}
	return a.ConfirmCommit(ctx, r, t, s)
}
func (p Providers) Changes(ctx context.Context, r domain.Repository, t string, c domain.Cursor) (domain.ChangePage, error) {
	a, e := p.selectProvider(r)
	if e != nil {
		return domain.ChangePage{}, e
	}
	return a.Changes(ctx, r, t, c)
}
func (p Providers) MR(ctx context.Context, r domain.Repository, t string, i int64) (domain.MergeRequest, bool, error) {
	a, e := p.selectProvider(r)
	if e != nil {
		return domain.MergeRequest{}, false, e
	}
	return a.MR(ctx, r, t, i)
}
func (p Providers) MRCommits(ctx context.Context, r domain.Repository, t string, m domain.MergeRequest, n int) (domain.MRCommitPage, error) {
	a, e := p.selectProvider(r)
	if e != nil {
		return domain.MRCommitPage{}, e
	}
	return a.MRCommits(ctx, r, t, m, n)
}
func (p Providers) CodeDiff(ctx context.Context, r domain.Repository, t, s string, n int) (domain.DiffPage, error) {
	a, e := p.selectProvider(r)
	if e != nil {
		return domain.DiffPage{}, e
	}
	return a.CodeDiff(ctx, r, t, s, n)
}
func (p Providers) CodeFile(ctx context.Context, r domain.Repository, t, s, f string) (domain.FileObservation, error) {
	a, e := p.selectProvider(r)
	if e != nil {
		return domain.FileObservation{}, e
	}
	return a.CodeFile(ctx, r, t, s, f)
}
