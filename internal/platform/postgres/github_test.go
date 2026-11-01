package postgres_test

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"workgraph.local/platform/internal/access"
	directory "workgraph.local/platform/internal/directory/domain"
	"workgraph.local/platform/internal/identifier"
	"workgraph.local/platform/internal/platform/postgres"
)

func TestGitHubRepositoryCatalogAndWorkerIdentityAreExplicitAndImmutable(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	store := postgres.NewDirectoryStore(pool)
	if err := store.GrantOperatorOffline(ctx, subject); err != nil {
		t.Fatal(err)
	}
	principal := directoryPrincipal(t, pool, subject, access.ReadDirectory, access.ManagePlatform)
	id, err := identifier.New()
	if err != nil {
		t.Fatal(err)
	}
	name := "GitHub project"
	command := directory.Command{RequestID: requestID(t), Action: directory.CreateRepository, ProjectID: project, EntityID: id, Name: &name, Provider: "github", ProviderURL: "https://github.com/example/project", ProviderRepositoryID: 42}
	first, created, err := store.Apply(ctx, principal, command)
	if err != nil || !created {
		t.Fatal(first, created, err)
	}
	again, recreated, err := store.Apply(ctx, principal, command)
	if err != nil || recreated || again.Sequence != first.Sequence {
		t.Fatal("catalog replay changed identity", again, err)
	}
	view, err := store.Repository(ctx, principal, id)
	if err != nil || view.Provider != "github" || view.ProviderURL != command.ProviderURL || view.ProviderRepositoryID != 42 || view.GitLabURL != "" || view.GitLabProjectID != 0 {
		t.Fatal("GitHub masqueraded as GitLab", view, err)
	}
	old, err := store.Repository(ctx, principal, repository)
	if err != nil || old.Provider != "" || old.GitLabURL != "https://gitlab.internal" {
		t.Fatal("legacy GitLab catalog changed", old, err)
	}
	forge := postgres.NewForgeStore(pool)
	if _, err := forge.RepositoryForAdmin(ctx, subject, id); !errors.Is(err, access.ErrDenied) {
		t.Fatal("operator bypassed project-admin membership", err)
	}
	members, err := store.Members(ctx, principal, project, directory.Cursor{})
	if err != nil {
		t.Fatal(err)
	}
	var version int64
	for _, member := range members.Items {
		if member.UserID == subject {
			version = member.Version
		}
	}
	_, _, err = store.Apply(ctx, principal, directory.Command{RequestID: requestID(t), Action: directory.SetMember, ProjectID: project, EntityID: subject, ExpectedVersion: version, Role: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	repo, err := forge.RepositoryForAdmin(ctx, subject, id)
	if err != nil || repo.Provider != "github" || repo.BaseURL != command.ProviderURL || repo.ProjectID != 42 {
		t.Fatal(repo, err)
	}
	status, err := forge.Connect(ctx, subject, repo, bytes.Repeat([]byte{1}, 40), "disabled", true)
	if err != nil || status.Provider != "github" || len(status.Streams) != 2 || !status.Anonymous {
		t.Fatal("unsupported GitLab lanes advertised", status, err)
	}
	lease, found, err := forge.Claim(ctx)
	if err != nil || !found || lease.Repository != repo {
		t.Fatal("worker lost provider fence", lease, err)
	}
}
