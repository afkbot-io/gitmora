package application_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/directory/application"
	"workgraph.local/platform/internal/directory/domain"
)

type lookupStore struct {
	application.Store
	allowed bool
}

func (s *lookupStore) AuthorizeRepositoryCreate(context.Context, access.Principal, string) error {
	if !s.allowed {
		return access.ErrDenied
	}
	return nil
}

type lookupResolver struct{ calls int }

func (r *lookupResolver) LookupRepository(context.Context, string) (domain.ResolvedRepository, error) {
	r.calls++
	return domain.ResolvedRepository{Provider: "github", URL: "https://github.com/example/project", ID: 42}, nil
}
func TestRepositoryLookupAuthorizesBeforeProviderIOAndRejectsOtherHosts(t *testing.T) {
	store := &lookupStore{}
	resolver := &lookupResolver{}
	service := application.NewService(store, time.Now).WithRepositoryResolver(resolver)
	_, err := service.LookupRepository(context.Background(), access.Principal{}, "project", "https://github.com/example/project")
	if !errors.Is(err, access.ErrDenied) || resolver.calls != 0 {
		t.Fatal("unauthorized lookup reached provider", err)
	}
	store.allowed = true
	value, err := service.LookupRepository(context.Background(), access.Principal{}, "project", "https://github.com/example/project")
	if err != nil || value.ID != 42 || resolver.calls != 1 {
		t.Fatal(value, err)
	}
	_, err = service.LookupRepository(context.Background(), access.Principal{}, "project", "https://other.invalid/example/project")
	if !errors.Is(err, domain.ErrInvalid) || resolver.calls != 1 {
		t.Fatal("caller controlled lookup destination", err)
	}
}
