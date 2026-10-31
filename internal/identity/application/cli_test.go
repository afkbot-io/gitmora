package application_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/identity/application"
	"workgraph.local/platform/internal/identity/domain"
)

type cliStore struct {
	calls int
	value domain.CLIEnrollment
	err   error
}

func (s *cliStore) CreateCLIEnrollment(_ context.Context, cfg string, r domain.CLIRequest) (domain.CLIEnrollment, error) {
	s.calls++
	s.value.Request = r
	s.value.Configuration = cfg
	return s.value, s.err
}
func (s *cliStore) InspectCLIEnrollment(context.Context, domain.Session, string, string) (domain.CLIEnrollment, error) {
	s.calls++
	return s.value, s.err
}
func (s *cliStore) DecideCLIEnrollment(context.Context, domain.Session, string, string, string, bool) (domain.CLIEnrollment, error) {
	s.calls++
	return s.value, s.err
}
func (s *cliStore) PollCLIEnrollment(context.Context, string, string, string, string) (domain.CLIEnrollment, error) {
	s.calls++
	return s.value, s.err
}
func (s *cliStore) CancelCLIEnrollment(context.Context, string, string, string, string) (domain.CLIEnrollment, error) {
	s.calls++
	return s.value, s.err
}

func TestCLIApplicationRejectsMalformedProofsBeforeStoreAndNeverPublishesHashes(t *testing.T) {
	cfg := domain.Config{Issuer: "https://idp.internal", ClientID: "workgraph", PublicURL: "https://workgraph.internal", AuthStyle: "basic", SessionSeconds: 28800, Policy: domain.Policy}
	s := &cliStore{value: domain.CLIEnrollment{CreatedAt: time.Now(), ExpiresAt: time.Now().Add(10 * time.Minute), Status: "pending"}}
	service := application.NewCLIService(s, cfg)
	i := domain.CLIRequest{ID: "11111111-1111-4111-8111-111111111111", DeviceHash: strings.Repeat("a", 64), ProofHash: strings.Repeat("b", 64), CredentialHash: strings.Repeat("c", 64), CodeHash: strings.Repeat("d", 64), Label: "Laptop", Scopes: []access.Permission{access.ManageCredentials, access.WriteCapture}, TTLSeconds: 86400}
	start, e := service.Start(context.Background(), i)
	if e != nil || start.ID != i.ID || start.VerificationURI != "https://workgraph.internal/v1/auth/cli/login" || start.IntervalSeconds != 2 {
		t.Fatal(start, e)
	}
	i.Scopes = []access.Permission{access.ManagePlatform}
	if _, e := service.Start(context.Background(), i); !errors.Is(e, domain.ErrInvalid) {
		t.Fatal(e)
	}
	for _, run := range []func() error{
		func() error { _, e := service.Poll(context.Background(), i.ID, "raw-credential", "bad"); return e },
		func() error { _, e := service.Cancel(context.Background(), i.ID, "raw-credential", "bad"); return e },
		func() error { _, e := service.Inspect(context.Background(), domain.Session{}, "bad code"); return e },
		func() error {
			_, e := service.Decide(context.Background(), domain.Session{}, "ABCD-EFGH", "bad", true)
			return e
		},
	} {
		if !errors.Is(run(), domain.ErrInvalid) {
			t.Fatal("malformed enrollment reached persistence")
		}
	}
	if s.calls != 1 {
		t.Fatal("early validation dispatched", s.calls)
	}
}
