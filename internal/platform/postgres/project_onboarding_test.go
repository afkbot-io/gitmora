package postgres_test

import (
	"context"
	"errors"
	"testing"

	"workgraph.local/platform/internal/access"
	captures "workgraph.local/platform/internal/capture/application"
	pack "workgraph.local/platform/internal/contextpack/domain"
	"workgraph.local/platform/internal/directory/domain"
	"workgraph.local/platform/internal/platform/postgres"
)

func TestSelfServiceProjectOwnsOnlyItsNewProjectAndReplaysAtomically(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	s := postgres.NewDirectoryStore(pool)
	p := directoryPrincipal(t, pool, reader, access.ReadDirectory, access.ManageDirectory, access.ManageCredentials)
	id := "77777777-7777-4777-8777-777777777777"
	command := domain.Command{RequestID: requestID(t), Action: domain.CreateProject, EntityID: id, Name: display("My team")}
	receipt, created, e := s.Apply(ctx, p, command)
	if e != nil || !created {
		t.Fatal(receipt, created, e)
	}
	replay, created, e := s.Apply(ctx, p, command)
	if e != nil || created || replay.Sequence != receipt.Sequence {
		t.Fatal("project replay", replay, e)
	}
	value, e := s.Project(ctx, p, id)
	if e != nil || value.Role != "admin" {
		t.Fatal(value, e)
	}
	old, e := s.Project(ctx, p, project)
	if e != nil || old.Role != "reader" {
		t.Fatal("other membership changed", old, e)
	}
	if _, _, e = s.Apply(ctx, p, domain.Command{RequestID: requestID(t), Action: domain.RenameProject, EntityID: project, ExpectedVersion: old.Version, Name: display("Take over")}); !errors.Is(e, access.ErrDenied) {
		t.Fatal("foreign project modified", e)
	}
	if _, e = s.Users(ctx, p, domain.Cursor{}); !errors.Is(e, access.ErrDenied) {
		t.Fatal("global accounts became visible", e)
	}
	narrow := directoryPrincipal(t, pool, reader, access.ReadDirectory)
	command.EntityID = "88888888-8888-4888-8888-888888888888"
	command.RequestID = requestID(t)
	if _, _, e = s.Apply(ctx, narrow, command); !errors.Is(e, access.ErrDenied) {
		t.Fatal("read-only credential created project", e)
	}
}

func TestUpstreamRepositoryCanBelongToIndependentProjects(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	otherProject := "77777777-7777-4777-8777-777777777777"
	if _, err := pool.Exec(ctx, `INSERT INTO projects(id,name) VALUES($1,'Other company')`, otherProject); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO repositories(id,project_id,gitlab_url,gitlab_project_id,name) VALUES($1,$2,'https://gitlab.internal',42,'Independent copy')`, otherRepository, otherProject); err != nil {
		t.Fatal("upstream identity was globally unique", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO repositories(id,project_id,gitlab_url,gitlab_project_id,name) VALUES('88888888-8888-4888-8888-888888888888',$1,'https://gitlab.internal',42,'Duplicate')`, project); err == nil {
		t.Fatal("duplicate accepted inside project")
	}
	if _, err := captures.NewService(postgres.NewCaptureStore(pool)).Ingest(ctx, subject, snapshot()); err != nil {
		t.Fatal(err)
	}
	store := postgres.NewContextStore(pool)
	if _, err := store.Read(ctx, contextPrincipal(access.ReadCapture), pack.Query{ProjectID: otherProject, RepositoryID: otherRepository, Limit: 6}); !errors.Is(err, access.ErrDenied) {
		t.Fatal("foreign company capture access", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO memberships(project_id,user_id,role) VALUES($1,$2,'reader')`, otherProject, reader); err != nil {
		t.Fatal(err)
	}
	result, err := store.Read(ctx, contextPrincipal(access.ReadCapture), pack.Query{ProjectID: otherProject, RepositoryID: otherRepository, Limit: 6})
	if err != nil || len(result.Captures.Items) != 0 {
		t.Fatal("captures shared by upstream identity", result, err)
	}
}

func TestProjectMemberExactHandleLookupRequiresCurrentAdministration(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	s := postgres.NewDirectoryStore(pool)
	if _, e := pool.Exec(ctx, `UPDATE memberships SET role='admin' WHERE project_id=$1 AND user_id=$2`, project, subject); e != nil {
		t.Fatal(e)
	}
	owner := directoryPrincipal(t, pool, subject, access.ReadDirectory, access.ManageDirectory, access.ManageCredentials)
	guest := directoryPrincipal(t, pool, reader, access.ReadDirectory, access.ManageDirectory, access.ManageCredentials)
	me, e := s.Me(ctx, guest)
	if e != nil {
		t.Fatal(e)
	}
	c := domain.Command{RequestID: requestID(t), Action: domain.SetUsername, EntityID: reader, ExpectedVersion: me.User.Version, Username: "Guest.1"}
	if _, _, e = s.Apply(ctx, guest, c); e != nil {
		t.Fatal(e)
	}
	for _, input := range []string{"@guest.1", reader} {
		candidate, e := s.ResolveMember(ctx, owner, project, input)
		if e != nil || candidate.ID != reader || candidate.Username != "guest.1" {
			t.Fatal(candidate, e)
		}
	}
	if _, e = s.ResolveMember(ctx, guest, project, "guest.1"); !errors.Is(e, access.ErrDenied) {
		t.Fatal("reader looked up global user", e)
	}
	c.EntityID = subject
	c.RequestID = requestID(t)
	if _, _, e = s.Apply(ctx, guest, c); !errors.Is(e, access.ErrDenied) {
		t.Fatal("another account handle changed", e)
	}
	me, e = s.Me(ctx, owner)
	if e != nil {
		t.Fatal(e)
	}
	c.ExpectedVersion = me.User.Version
	c.RequestID = requestID(t)
	if _, _, e = s.Apply(ctx, owner, c); !errors.Is(e, domain.ErrUsernameTaken) {
		t.Fatal("duplicate handle accepted", e)
	}
	if _, e = pool.Exec(ctx, `UPDATE users SET enabled=false WHERE id=$1`, reader); e != nil {
		t.Fatal(e)
	}
	if _, e = s.ResolveMember(ctx, owner, project, "guest.1"); !errors.Is(e, domain.ErrNotFound) {
		t.Fatal("disabled account resolved", e)
	}
}
