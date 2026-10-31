package application_test

import (
	"context"
	"errors"
	"testing"

	"workgraph.local/platform/internal/work/application"
	"workgraph.local/platform/internal/work/domain"
)

type store struct {
	application.Store
	lease application.Lease
	out   application.Outcome
}

func (s *store) Claim(context.Context) (application.Lease, bool, error) { return s.lease, true, nil }
func (s *store) Finish(_ context.Context, _ application.Lease, out application.Outcome) error {
	s.out = out
	return nil
}

type vault struct{}

func (vault) Open(string, []byte) ([]byte, error) { return []byte("synthetic-token"), nil }
func (vault) Seal(string, []byte) ([]byte, error) { return nil, nil }

type provider struct {
	application.Provider
	task     domain.Task
	err      error
	writes   int
	links    int
	failLink bool
}

func (p *provider) Task(context.Context, domain.Config, string) (domain.Task, error) {
	return p.task, p.err
}
func (p *provider) Publish(context.Context, domain.Config, string, string, domain.EvidenceDocument) error {
	p.writes++
	return nil
}
func (p *provider) AddLink(_ context.Context, _ domain.Config, _ string, _ string, link domain.MRLink) error {
	p.links++
	p.task.Links = append(p.task.Links, domain.Link{URL: link.URL, Title: link.Title})
	if p.failLink {
		p.failLink = false
		return &application.ProviderError{Code: "provider_unavailable", Retryable: true}
	}
	return nil
}

func TestMRLinkLostAcknowledgementReconcilesOnNextAttempt(t *testing.T) {
	link, err := domain.NewMRLink("11111111-1111-4111-8111-111111111111", "https://gitlab.internal", 42, 7, "https://gitlab.internal/p/-/merge_requests/7")
	if err != nil {
		t.Fatal(err)
	}
	storage := &store{lease: application.Lease{Kind: "link", Link: link}}
	remote := &provider{failLink: true}
	service := application.NewService(storage, vault{}, remote)
	if _, err := service.ReconcileOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if storage.out.Code != "provider_unavailable" || storage.out.Blocked || remote.links != 1 {
		t.Fatal(storage.out, remote.links)
	}
	if _, err := service.ReconcileOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if storage.out.Code != "" || storage.out.Task == nil || remote.links != 1 {
		t.Fatal("uncertain ACK repeated link_add", storage.out, remote.links)
	}
}

func TestMRLinkReadbackAvoidsDuplicateWritesAndRetainsUpdatedContext(t *testing.T) {
	link, err := domain.NewMRLink("11111111-1111-4111-8111-111111111111", "https://gitlab.internal", 42, 7, "https://gitlab.internal/team/project/-/merge_requests/7")
	if err != nil {
		t.Fatal(err)
	}
	for _, present := range []bool{false, true} {
		storage := &store{lease: application.Lease{Kind: "link", Link: link}}
		remote := &provider{}
		if present {
			remote.task.Links = []domain.Link{{URL: link.URL, Title: "Human label"}}
		}
		if _, err := application.NewService(storage, vault{}, remote).ReconcileOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		if remote.links != map[bool]int{false: 1, true: 0}[present] || storage.out.Code != "" || storage.out.Task == nil || len(storage.out.Task.Links) != 1 {
			t.Fatal("link retry duplicated a write or lost fresh context", storage.out, remote.links)
		}
	}
}

func TestReadbackReconcilesLostAcknowledgementWithoutOverwriting(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "same", true: "changed"}[changed], func(t *testing.T) {
			doc := domain.EvidenceDocument{FileName: "workgraph-evidence-owned.md", Content: "immutable evidence"}
			current := doc.Content
			if changed {
				current = "manually edited"
			}
			store := &store{lease: application.Lease{Kind: "publication", Document: doc}}
			provider := &provider{task: domain.Task{Documents: []domain.Document{{FileName: doc.FileName, Content: current}}}}
			service := application.NewService(store, vault{}, provider)
			if _, err := service.ReconcileOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			if provider.writes != 0 {
				t.Fatal("readback caused duplicate or destructive write")
			}
			if changed && (store.out.Code != "publication_conflict" || !store.out.Blocked) {
				t.Fatal(store.out)
			}
			if !changed && (store.out.Code != "" || store.out.Task == nil) {
				t.Fatal(store.out)
			}
		})
	}
}
func TestProviderFailuresNeverPersistRawDiagnosticAndInvalidateDeniedSources(t *testing.T) {
	for _, err := range []error{errors.New("secret remote error body"), &application.ProviderError{Code: "country_access_denied"}} {
		store := &store{}
		provider := &provider{err: err}
		service := application.NewService(store, vault{}, provider)
		if _, e := service.ReconcileOnce(context.Background()); e != nil {
			t.Fatal(e)
		}
		if store.out.Code == err.Error() {
			t.Fatal("raw error stored")
		}
		if _, ok := err.(*application.ProviderError); ok && (!store.out.Blocked || !store.out.Unavailable) {
			t.Fatal("denied source not invalidated")
		}
	}
}
