package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"workgraph.local/platform/internal/access"
	captureapp "workgraph.local/platform/internal/capture/application"
	directoryapp "workgraph.local/platform/internal/directory/application"
	"workgraph.local/platform/internal/directory/domain"
	"workgraph.local/platform/internal/identifier"
	"workgraph.local/platform/internal/platform/postgres"
	retrieval "workgraph.local/platform/internal/retrieval/domain"
)

func directoryPrincipal(t *testing.T, pool *pgxpool.Pool, user string, caps ...access.Permission) access.Principal {
	t.Helper()
	issued, e := access.IssueCredential(user, "Directory", caps, time.Now(), time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	store := postgres.NewCredentialStore(pool)
	if e = store.Save(context.Background(), issued.Record); e != nil {
		t.Fatal(e)
	}
	p, e := store.Authenticate(context.Background(), issued.Secret)
	if e != nil {
		t.Fatal(e)
	}
	return p
}

func TestDirectoryDeletionCascadesLocalEvidenceAndFencesOldWork(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	s := postgres.NewDirectoryStore(pool)
	if e := s.GrantOperatorOffline(ctx, subject); e != nil {
		t.Fatal(e)
	}
	p := directoryPrincipal(t, pool, subject, access.ReadDirectory, access.ManagePlatform)
	if _, e := captureapp.NewService(postgres.NewCaptureStore(pool)).Ingest(ctx, subject, snapshot()); e != nil {
		t.Fatal(e)
	}
	rs := postgres.NewRetrievalStore(pool)
	lease, ok, e := rs.Claim(ctx)
	if e != nil || !ok {
		t.Fatal(lease, ok, e)
	}
	view, e := s.Repository(ctx, p, repository)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.Apply(ctx, p, domain.Command{RequestID: requestID(t), Action: domain.DeleteRepository, ProjectID: project, EntityID: repository, ExpectedVersion: view.Version}); e != nil {
		t.Fatal(e)
	}
	if count(t, pool, "capture_revisions") != 0 || count(t, pool, "retrieval_jobs") != 0 || count(t, pool, "outbox_events") != 0 {
		t.Fatal("local evidence/jobs survived deletion")
	}
	if e = rs.Finish(ctx, lease, nil, "index_source_unavailable"); !errors.Is(e, retrieval.ErrLease) {
		t.Fatal("deleted source worker published a result", e)
	}
	var retired bool
	if e = pool.QueryRow(ctx, `SELECT retired_at IS NOT NULL FROM directory_identifiers WHERE kind='repository' AND id=$1`, repository).Scan(&retired); e != nil || !retired {
		t.Fatal(retired, e)
	}
}

func TestDirectoryMemberVersionsPreventABAAndFailedAuditRollsBack(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	s := postgres.NewDirectoryStore(pool)
	if e := s.GrantOperatorOffline(ctx, subject); e != nil {
		t.Fatal(e)
	}
	p := directoryPrincipal(t, pool, subject, access.ReadDirectory, access.ManagePlatform)
	var previous int64
	if e := pool.QueryRow(ctx, `SELECT version FROM memberships WHERE project_id=$1 AND user_id=$2`, project, reader).Scan(&previous); e != nil {
		t.Fatal(e)
	}
	if _, _, e := s.Apply(ctx, p, domain.Command{RequestID: requestID(t), Action: domain.RemoveMember, ProjectID: project, EntityID: reader, ExpectedVersion: previous}); e != nil {
		t.Fatal(e)
	}
	readded, _, e := s.Apply(ctx, p, domain.Command{RequestID: requestID(t), Action: domain.SetMember, ProjectID: project, EntityID: reader, Role: "writer"})
	if e != nil || readded.Version <= previous {
		t.Fatal(readded, e)
	}
	if _, _, e = s.Apply(ctx, p, domain.Command{RequestID: requestID(t), Action: domain.SetMember, ProjectID: project, EntityID: reader, ExpectedVersion: previous, Role: "admin"}); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("old membership version retargeted a recreated grant", e)
	}
	cmd := domain.Command{RequestID: requestID(t), Action: domain.CreateProject, EntityID: "77777777-7777-4777-8777-777777777777", Name: display("Atomic")}
	if _, e = pool.Exec(ctx, `ALTER TABLE directory_events ADD CONSTRAINT simulated_audit_failure CHECK(false)NOT VALID`); e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.Apply(ctx, p, cmd); e == nil {
		t.Fatal("mutation committed without audit")
	}
	var exists bool
	if e = pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM projects WHERE id=$1)`, cmd.EntityID).Scan(&exists); e != nil || exists {
		t.Fatal("partial directory mutation", exists, e)
	}
	if _, e = pool.Exec(ctx, `ALTER TABLE directory_events DROP CONSTRAINT simulated_audit_failure`); e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.Apply(ctx, p, cmd); e != nil {
		t.Fatal("rolled-back request could not be retried", e)
	}
}

func TestDirectoryConcurrentDelegationCannotEscapeRevocation(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	store := postgres.NewDirectoryStore(pool)
	service := directoryapp.NewService(store, time.Now)
	cs := postgres.NewCredentialStore(pool)
	for range 5 {
		p := directoryPrincipal(t, pool, subject, access.ManageCredentials, access.ReadCapture)
		input := domain.TokenInput{RequestID: requestID(t), Label: "Concurrent", Scopes: []access.Permission{access.ReadCapture}, TTLSeconds: 300}
		var wg sync.WaitGroup
		var issued domain.IssuedToken
		var issueErr, revokeErr error
		wg.Go(func() { issued, issueErr = service.IssueToken(ctx, p, input) })
		wg.Go(func() { revokeErr = cs.Revoke(ctx, p.UserID, p.TokenID) })
		wg.Wait()
		if revokeErr != nil {
			t.Fatal(revokeErr)
		}
		if issueErr != nil && !errors.Is(issueErr, access.ErrDenied) {
			t.Fatal(issueErr)
		}
		if issued.Secret != "" {
			if _, e := cs.Authenticate(ctx, issued.Secret); !errors.Is(e, access.ErrUnauthenticated) {
				t.Fatal("racing child remained usable", e)
			}
		}
	}
}

func TestDirectoryCredentialDelegationRevocationAndNoSecretReplay(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	store := postgres.NewDirectoryStore(pool)
	service := directoryapp.NewService(store, time.Now)
	p := directoryPrincipal(t, pool, subject, access.ManageCredentials, access.ReadCapture)
	input := domain.TokenInput{RequestID: requestID(t), Label: "Collector", Scopes: []access.Permission{access.ReadCapture, access.ManageCredentials}, TTLSeconds: 1200}
	first, e := service.IssueToken(ctx, p, input)
	if e != nil || !first.Created || first.Secret == "" {
		t.Fatal(first.Created, e)
	}
	replay, e := service.IssueToken(ctx, p, input)
	if e != nil || replay.Secret != "" || replay.Created || replay.Token.ID != first.Token.ID {
		t.Fatal(replay, e)
	}
	changed := input
	changed.Label = "Changed"
	if _, e = service.IssueToken(ctx, p, changed); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("issuance request changed identity", e)
	}
	escalated := input
	escalated.RequestID = requestID(t)
	escalated.Scopes = append(escalated.Scopes, access.ManagePlatform)
	if _, e = service.IssueToken(ctx, p, escalated); !errors.Is(e, domain.ErrDelegation) {
		t.Fatal("delegated a missing scope", e)
	}
	extended := input
	extended.RequestID = requestID(t)
	extended.TTLSeconds = 7200
	if _, e = service.IssueToken(ctx, p, extended); !errors.Is(e, domain.ErrDelegation) {
		t.Fatal("delegation outlived parent", e)
	}
	cs := postgres.NewCredentialStore(pool)
	child, e := cs.Authenticate(ctx, first.Secret)
	if e != nil {
		t.Fatal(e)
	}
	descInput := input
	descInput.RequestID = requestID(t)
	descInput.TTLSeconds = 300
	grandchild, e := service.IssueToken(ctx, child, descInput)
	if e != nil {
		t.Fatal(e)
	}
	if e = cs.Revoke(ctx, p.UserID, p.TokenID); e != nil {
		t.Fatal(e)
	}
	for _, secret := range []string{first.Secret, grandchild.Secret} {
		if _, e = cs.Authenticate(ctx, secret); !errors.Is(e, access.ErrUnauthenticated) {
			t.Fatal("descendant escaped parent revocation", e)
		}
	}
	readerP := directoryPrincipal(t, pool, reader, access.ManageCredentials)
	if _, _, e = store.Apply(ctx, readerP, domain.Command{RequestID: requestID(t), Action: domain.RevokeToken, EntityID: first.Token.ID}); !errors.Is(e, access.ErrDenied) {
		t.Fatal("another user's token revoked", e)
	}
	raw, e := json.Marshal(first.Token)
	if e != nil || strings.Contains(string(raw), first.Secret) || strings.Contains(string(raw), "secret_hash") {
		t.Fatal("token metadata leaked secret", e)
	}
}
func requestID(t *testing.T) string {
	t.Helper()
	id, e := identifier.New()
	if e != nil {
		t.Fatal(e)
	}
	return id
}
func display(s string) *string { return &s }

func TestDirectoryVersionedCatalogReplayAndRetiredIdentity(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	s := postgres.NewDirectoryStore(pool)
	if e := s.GrantOperatorOffline(ctx, subject); e != nil {
		t.Fatal(e)
	}
	p := directoryPrincipal(t, pool, subject, access.ReadDirectory, access.ManageDirectory, access.ManagePlatform)
	create := domain.Command{RequestID: requestID(t), Action: domain.CreateProject, EntityID: "77777777-7777-4777-8777-777777777777", Name: display("New project")}
	ids := make(chan domain.Receipt, 5)
	errs := make(chan error, 5)
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() { r, _, e := s.Apply(ctx, p, create); ids <- r; errs <- e })
	}
	wg.Wait()
	close(ids)
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	var first domain.Receipt
	for r := range ids {
		if first.Sequence != 0 && first != r {
			t.Fatal("replay changed receipt", first, r)
		}
		first = r
	}
	projectView, e := s.Project(ctx, p, create.EntityID)
	if e != nil || projectView.Role != "admin" || projectView.Version != first.Version {
		t.Fatal(projectView, e)
	}
	altered := create
	altered.Name = display("Different")
	if _, _, e = s.Apply(ctx, p, altered); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("request ID changed input", e)
	}
	repo := domain.Command{RequestID: requestID(t), Action: domain.CreateRepository, ProjectID: create.EntityID, EntityID: "88888888-8888-4888-8888-888888888888", Name: display("App"), GitLabURL: "https://gitlab.internal/", GitLabProjectID: 99}
	added, _, e := s.Apply(ctx, p, repo)
	if e != nil {
		t.Fatal(e)
	}
	view, e := s.Repository(ctx, p, repo.EntityID)
	if e != nil || view.GitLabURL != "https://gitlab.internal" || view.GitLabProjectID != 99 {
		t.Fatal(view, e)
	}
	rename := domain.Command{RequestID: requestID(t), Action: domain.RenameRepository, ProjectID: create.EntityID, EntityID: repo.EntityID, ExpectedVersion: view.Version, Name: display("Renamed")}
	renamed, _, e := s.Apply(ctx, p, rename)
	if e != nil {
		t.Fatal(e)
	}
	rename.RequestID = requestID(t)
	if _, _, e = s.Apply(ctx, p, rename); !errors.Is(e, domain.ErrConflict) {
		t.Fatal("stale update accepted", e)
	}
	deletion := domain.Command{RequestID: requestID(t), Action: domain.DeleteRepository, ProjectID: create.EntityID, EntityID: repo.EntityID, ExpectedVersion: renamed.Version}
	deleted, _, e := s.Apply(ctx, p, deletion)
	if e != nil || !deleted.Deleted {
		t.Fatal(deleted, e)
	}
	if _, e = s.Repository(ctx, p, repo.EntityID); !errors.Is(e, domain.ErrNotFound) {
		t.Fatal(e)
	}
	repo.RequestID = requestID(t)
	if _, _, e = s.Apply(ctx, p, repo); !errors.Is(e, domain.ErrRetired) {
		t.Fatal("retired repository UUID reused", e)
	}
	current, e := s.Project(ctx, p, create.EntityID)
	if e != nil || current.Version <= added.ProjectVersion {
		t.Fatal(current, e)
	}
	remove := domain.Command{RequestID: requestID(t), Action: domain.DeleteProject, EntityID: create.EntityID, ExpectedVersion: current.Version}
	receipt, _, e := s.Apply(ctx, p, remove)
	if e != nil {
		t.Fatal(e)
	}
	journal, e := s.Events(ctx, p, domain.EventQuery{ProjectID: create.EntityID})
	if e != nil || len(journal.Items) < 3 {
		t.Fatal("deleted-project journal unavailable", journal, e)
	}
	repeat, created, e := s.Apply(ctx, p, remove)
	if e != nil || created || repeat != receipt {
		t.Fatal("deleted project receipt did not replay", repeat, created, e)
	}
	create.RequestID = requestID(t)
	if _, _, e = s.Apply(ctx, p, create); !errors.Is(e, domain.ErrRetired) {
		t.Fatal("retired project UUID reused", e)
	}
}

func TestDirectoryProjectAuthorityDoesNotBecomeContentOrPlatformAuthority(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	s := postgres.NewDirectoryStore(pool)
	if e := s.GrantOperatorOffline(ctx, subject); e != nil {
		t.Fatal(e)
	}
	if _, e := pool.Exec(ctx, `UPDATE memberships SET role='admin' WHERE user_id=$1`, reader); e != nil {
		t.Fatal(e)
	}
	scoped := directoryPrincipal(t, pool, reader, access.ReadDirectory, access.ManageDirectory, access.ManagePlatform)
	user := domain.Command{RequestID: requestID(t), Action: domain.CreateUser, EntityID: "77777777-7777-4777-8777-777777777777", Name: display("User")}
	if _, _, e := s.Apply(ctx, scoped, user); !errors.Is(e, access.ErrDenied) {
		t.Fatal("project admin became installation operator", e)
	}
	foreign := "99999999-9999-4999-8999-999999999999"
	if _, e := pool.Exec(ctx, `INSERT INTO projects(id,name)VALUES($1,'Foreign')`, foreign); e != nil {
		t.Fatal(e)
	}
	if _, e := pool.Exec(ctx, `INSERT INTO repositories(id,project_id,gitlab_url,gitlab_project_id,name)VALUES($1,$2,'https://gitlab.internal',99,'Foreign')`, otherRepository, foreign); e != nil {
		t.Fatal(e)
	}
	root := directoryPrincipal(t, pool, subject, access.ReadDirectory, access.ManagePlatform, access.ReadCapture)
	all, e := s.Projects(ctx, root, domain.Cursor{})
	if e != nil || len(all.Items) != 2 {
		t.Fatal(all, e)
	}
	narrow := directoryPrincipal(t, pool, subject, access.ReadDirectory)
	own, e := s.Projects(ctx, narrow, domain.Cursor{Limit: 1})
	if e != nil || len(own.Items) != 1 || own.Items[0].ID != project || own.NextID != "" {
		t.Fatal("permission applied after LIMIT", own, e)
	}
	if _, e = s.Project(ctx, scoped, foreign); !errors.Is(e, access.ErrDenied) {
		t.Fatal("foreign project readable", e)
	}
	if e := postgres.NewCaptureStore(pool).WithinAuthorized(ctx, root.UserID, otherRepository, access.ReadCapture, func(_ captureapp.Transaction) error { return nil }); !errors.Is(e, access.ErrDenied) {
		t.Fatal("operator metadata privilege granted content access", e)
	}
}

func TestDirectoryConcurrentLastAdminAndAccountDisable(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	s := postgres.NewDirectoryStore(pool)
	if e := s.GrantOperatorOffline(ctx, subject); e != nil {
		t.Fatal(e)
	}
	p := directoryPrincipal(t, pool, subject, access.ReadDirectory, access.ManageDirectory, access.ManagePlatform)
	me, e := s.Me(ctx, p)
	if e != nil {
		t.Fatal(e)
	}
	off := false
	if _, _, e = s.Apply(ctx, p, domain.Command{RequestID: requestID(t), Action: domain.UpdateUser, EntityID: subject, ExpectedVersion: me.User.Version, Enabled: &off}); !errors.Is(e, domain.ErrLastOperator) {
		t.Fatal("last operator disabled", e)
	}
	newUser := "77777777-7777-4777-8777-777777777777"
	if _, _, e = s.Apply(ctx, p, domain.Command{RequestID: requestID(t), Action: domain.CreateUser, EntityID: newUser, Name: display("Second admin")}); e != nil {
		t.Fatal(e)
	}
	if _, e = pool.Exec(ctx, `UPDATE memberships SET role='admin' WHERE user_id=$1`, reader); e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.Apply(ctx, p, domain.Command{RequestID: requestID(t), Action: domain.SetMember, ProjectID: project, EntityID: newUser, Role: "admin"}); e != nil {
		t.Fatal(e)
	}
	members, e := s.Members(ctx, p, project, domain.Cursor{})
	if e != nil {
		t.Fatal(e)
	}
	commands := []domain.Command{}
	for _, m := range members.Items {
		if m.Role == "admin" {
			commands = append(commands, domain.Command{RequestID: requestID(t), Action: domain.RemoveMember, ProjectID: project, EntityID: m.UserID, ExpectedVersion: m.Version})
		}
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, c := range commands {
		wg.Go(func() { _, _, e := s.Apply(ctx, p, c); errs <- e })
	}
	wg.Wait()
	close(errs)
	success, last := 0, 0
	for e := range errs {
		if e == nil {
			success++
		} else if errors.Is(e, domain.ErrLastAdmin) {
			last++
		} else {
			t.Fatal(e)
		}
	}
	if success != 1 || last != 1 {
		t.Fatal(success, last)
	}
	view, e := s.User(ctx, p, newUser)
	if e != nil {
		t.Fatal(e)
	}
	// If the other administrator survived, disable/delete the user; otherwise first replace the sole admin.
	remaining, e := s.Members(ctx, p, project, domain.Cursor{})
	if e != nil {
		t.Fatal(e)
	}
	for _, m := range remaining.Items {
		if m.UserID == newUser && m.Role == "admin" {
			var version int64
			if e = pool.QueryRow(ctx, `SELECT version FROM memberships WHERE project_id=$1 AND user_id=$2`, project, subject).Scan(&version); e != nil {
				t.Fatal(e)
			}
			if _, _, e = s.Apply(ctx, p, domain.Command{RequestID: requestID(t), Action: domain.SetMember, ProjectID: project, EntityID: subject, ExpectedVersion: version, Role: "admin"}); e != nil {
				t.Fatal(e)
			}
		}
	}
	credential := directoryPrincipal(t, pool, newUser, access.ReadCapture)
	deleted, _, e := s.Apply(ctx, p, domain.Command{RequestID: requestID(t), Action: domain.DeleteUser, EntityID: newUser, ExpectedVersion: view.Version})
	if e != nil || !deleted.Deleted {
		t.Fatal(deleted, e)
	}
	current, e := s.User(ctx, p, newUser)
	if e != nil || current.Enabled || current.DeletedAt == nil || current.DisplayName != "Deleted user" {
		t.Fatal(current, e)
	}
	if _, e = s.Me(ctx, credential); !errors.Is(e, access.ErrDenied) {
		t.Fatal("deleted account remained authenticated", e)
	}
	events, e := s.Events(ctx, p, domain.EventQuery{Limit: 100})
	if e != nil || len(events.Items) < 3 {
		t.Fatal(events, e)
	}
	raw, _ := json.Marshal(events)
	if strings.Contains(string(raw), "Second admin") {
		t.Fatal("audit stored display names")
	}
}
