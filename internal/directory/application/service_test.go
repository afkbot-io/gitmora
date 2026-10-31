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

type issuanceStore struct {
	application.Store
	accept   bool
	existing string
}

func (s *issuanceStore) IssueToken(_ context.Context, _ access.Principal, _ domain.TokenInput, r access.CredentialRecord) (domain.Token, bool, error) {
	if !s.accept {
		return domain.Token{}, false, access.ErrDenied
	}
	if s.existing != "" {
		return domain.Token{ID: s.existing}, false, nil
	}
	s.existing = r.ID
	return domain.Token{ID: r.ID}, true, nil
}
func TestCredentialSecretRequiresDurableFirstAcceptance(t *testing.T) {
	store := &issuanceStore{}
	service := application.NewService(store, time.Now)
	p := access.Principal{UserID: "11111111-1111-4111-8111-111111111111"}
	input := domain.TokenInput{RequestID: "22222222-2222-4222-8222-222222222222", Label: "Agent", Scopes: []access.Permission{access.ReadCapture}, TTLSeconds: 600}
	denied, e := service.IssueToken(context.Background(), p, input)
	if !errors.Is(e, access.ErrDenied) || denied.Secret != "" {
		t.Fatal("secret escaped failed storage", denied, e)
	}
	store.accept = true
	first, e := service.IssueToken(context.Background(), p, input)
	if e != nil || !first.Created || access.CredentialHash(first.Secret) == "" {
		t.Fatal(first.Created, e)
	}
	repeat, e := service.IssueToken(context.Background(), p, input)
	if e != nil || repeat.Created || repeat.Secret != "" || repeat.Token.ID != first.Token.ID {
		t.Fatal("replay exposed a new secret", repeat.Created, e)
	}
}
