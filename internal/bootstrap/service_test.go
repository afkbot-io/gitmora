package bootstrap_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"workgraph.local/platform/internal/bootstrap"
)

type store struct {
	calls int
	setup bootstrap.Setup
	err   error
}

func (s *store) Initialize(_ context.Context, setup bootstrap.Setup) error {
	s.calls++
	s.setup = setup
	return s.err
}
func valid() bootstrap.Input {
	return bootstrap.Input{DisplayName: "Administrator", ProjectName: "Team", RepositoryName: "Application", GitLabURL: "https://gitlab.internal", GitLabProjectID: 42}
}

func TestInitializeValidatesAndReturnsOnlyCommittedSetup(t *testing.T) {
	now := time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)
	persistence := &store{}
	service := bootstrap.NewService(persistence, func() time.Time { return now })
	result, err := service.Initialize(context.Background(), valid())
	if err != nil {
		t.Fatal(err)
	}
	if persistence.calls != 1 || result.UserID == "" || result.RepositoryID == "" || result.Credential.Secret == "" || persistence.setup.Credential.Hash == "" {
		t.Fatal("incomplete setup")
	}
	persistence.err = bootstrap.ErrAlreadyInitialized
	result, err = service.Initialize(context.Background(), valid())
	if !errors.Is(err, bootstrap.ErrAlreadyInitialized) || result.Credential.Secret != "" {
		t.Fatal("returned an uncommitted credential")
	}
}

func TestInvalidBootstrapNeverWrites(t *testing.T) {
	for _, mutate := range []func(*bootstrap.Input){
		func(i *bootstrap.Input) { i.GitLabURL = "https://user:password@gitlab.internal" },
		func(i *bootstrap.Input) { i.GitLabURL = "https://gitlab.internal?token=secret" },
		func(i *bootstrap.Input) { i.GitLabURL = "http://gitlab.internal" },
		func(i *bootstrap.Input) { i.GitLabProjectID = 0 },
		func(i *bootstrap.Input) { i.ProjectName = "" },
	} {
		persistence := &store{}
		input := valid()
		mutate(&input)
		if _, err := bootstrap.NewService(persistence, time.Now).Initialize(context.Background(), input); !errors.Is(err, bootstrap.ErrInvalid) {
			t.Fatalf("expected invalid input: %v", err)
		}
		if persistence.calls != 0 {
			t.Fatal("invalid setup persisted")
		}
	}
}
