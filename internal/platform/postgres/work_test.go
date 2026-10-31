package postgres_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"workgraph.local/platform/internal/access"
	captureapp "workgraph.local/platform/internal/capture/application"
	capture "workgraph.local/platform/internal/capture/domain"
	"workgraph.local/platform/internal/platform/postgres"
	"workgraph.local/platform/internal/platform/secretbox"
	"workgraph.local/platform/internal/work/application"
	"workgraph.local/platform/internal/work/domain"
)

const taskCountry = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
const taskUUID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"

type taskProvider struct {
	task      domain.Task
	failWrite bool
	writes    map[string]bool
	calls     int
}

func (p *taskProvider) Verify(context.Context, domain.Config) error { return nil }
func (p *taskProvider) AddLink(_ context.Context, _ domain.Config, _ string, _ string, link domain.MRLink) error {
	p.task.Links = append(p.task.Links, domain.Link{URL: link.URL, Title: link.Title})
	return nil
}
func (p *taskProvider) Task(context.Context, domain.Config, string) (domain.Task, error) {
	return p.task, nil
}
func (p *taskProvider) Publish(_ context.Context, _ domain.Config, _ string, id string, d domain.EvidenceDocument) error {
	p.calls++
	p.writes[id] = true
	if p.failWrite {
		p.failWrite = false
		return &application.ProviderError{Code: "provider_unavailable", Retryable: true}
	}
	return nil
}

func TestTasktopiaDurabilityScopeAndAmbiguousWriteRetry(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	_, _ = pool.Exec(ctx, "UPDATE memberships SET role='admin' WHERE user_id=$1", subject)
	store := postgres.NewWorkStore(pool)
	vault, _ := secretbox.New("test", map[string][]byte{"test": make([]byte, 32)})
	provider := &taskProvider{task: domain.Task{CountryID: taskCountry, ID: taskUUID, Number: 4, Title: "Scoped task", Status: "IN_PROGRESS", Progress: 10, UpdatedAt: time.Now(), URL: "https://tasks.internal/task/4?countryId=" + taskCountry + "&taskId=" + taskUUID}, writes: map[string]bool{}}
	service := application.NewService(store, vault, provider)
	config := domain.Config{Endpoint: "https://tasks.internal/mcp", CountryID: taskCountry, Token: "synthetic-private-service-token"}
	if _, err := service.Connect(ctx, reader, project, config); !errors.Is(err, access.ErrDenied) {
		t.Fatal("reader configured", err)
	}
	if _, err := service.Connect(ctx, subject, project, config); err != nil {
		t.Fatal(err)
	}
	var envelope []byte
	if err := pool.QueryRow(ctx, "SELECT secret_envelope FROM work_connections WHERE project_id=$1", project).Scan(&envelope); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(envelope), config.Token) {
		t.Fatal("plaintext token stored")
	}
	if err := service.Refresh(ctx, subject, project, taskUUID); err != nil {
		t.Fatal(err)
	}
	if worked, err := service.ReconcileOnce(ctx); err != nil || !worked {
		t.Fatal(worked, err)
	}
	if view, err := service.Task(ctx, reader, project, taskUUID); err != nil || view.Task.ID != taskUUID || !view.Available {
		t.Fatal(view, err)
	}
	if _, err := service.Task(ctx, otherRepository, project, taskUUID); !errors.Is(err, access.ErrDenied) {
		t.Fatal("cross-project read", err)
	}
	snap := snapshot()
	snap.Task = &capture.TaskRef{CountryID: taskCountry, TaskID: taskUUID}
	if _, err := captureapp.NewService(postgres.NewCaptureStore(pool)).Ingest(ctx, subject, snap); err != nil {
		t.Fatal(err)
	}
	input := application.PublishInput{RepositoryID: repository, SessionID: snap.SessionID, Revision: 1}
	if _, err := service.QueuePublication(ctx, subject, project, input); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("unconfirmed evidence published", err)
	}
	_, _ = pool.Exec(ctx, "UPDATE commits SET confirmed_at=now() WHERE repository_id=$1", repository)
	publication, err := service.QueuePublication(ctx, subject, project, input)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := service.QueuePublication(ctx, subject, project, input)
	if err != nil || duplicate.ID != publication.ID {
		t.Fatal("duplicate journal", err)
	}
	if _, err := service.QueuePublication(ctx, reader, project, input); !errors.Is(err, access.ErrDenied) {
		t.Fatal("reader published", err)
	}
	provider.failWrite = true
	if _, err := service.ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	first, err := service.Publication(ctx, reader, project, publication.ID)
	if err != nil || first.State != "pending" || first.ErrorCode != "provider_unavailable" {
		t.Fatal(first, err)
	}
	// Recreate both service and store, simulating restart after a remote success with a lost local ACK.
	_, _ = pool.Exec(ctx, "UPDATE work_jobs SET available_at=now() WHERE id=$1", publication.ID)
	restarted := application.NewService(postgres.NewWorkStore(pool), vault, provider)
	if _, err := restarted.ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	final, err := restarted.Publication(ctx, reader, project, publication.ID)
	if err != nil || final.State != "complete" || len(provider.writes) != 1 || provider.calls != 2 {
		t.Fatal(final, err, provider.calls)
	}
	if err := restarted.Disconnect(ctx, subject, project); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Task(ctx, reader, project, taskUUID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("disabled source exposed", err)
	}
}

func TestTasktopiaGenerationAndLeaseFences(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	_, _ = pool.Exec(ctx, "UPDATE memberships SET role='admin' WHERE user_id=$1", subject)
	store := postgres.NewWorkStore(pool)
	c := domain.Config{Endpoint: "https://tasks.internal/mcp", CountryID: taskCountry, Token: "test-token"}
	if _, err := store.Connect(ctx, subject, project, c, make([]byte, 64)); err != nil {
		t.Fatal(err)
	}
	if err := store.Refresh(ctx, subject, project, taskUUID); err != nil {
		t.Fatal(err)
	}
	lease, found, err := store.Claim(ctx)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	if _, err := store.Connect(ctx, subject, project, c, make([]byte, 64)); err != nil {
		t.Fatal(err)
	}
	if err := store.Finish(ctx, lease, application.Outcome{}); !errors.Is(err, domain.ErrLease) {
		t.Fatal("rotated worker committed", err)
	}
	c.CountryID = otherRepository
	if _, err := store.Connect(ctx, subject, project, c, make([]byte, 64)); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("changed external identity", err)
	}
	next, found, err := store.Claim(ctx)
	if err != nil || !found {
		t.Fatal(found, err)
	}
	_, _ = pool.Exec(ctx, "UPDATE work_jobs SET lease_until=now()-interval '1 second' WHERE id=$1", next.ID)
	if err := store.Finish(ctx, next, application.Outcome{}); !errors.Is(err, domain.ErrLease) {
		t.Fatal("expired lease committed", err)
	}
}

func TestTasktopiaLateReadPreservesNewerProjectionAndBothObservations(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "older-content", true: "older-missing"}[missing], func(t *testing.T) {
			pool := database(t)
			seed(t, pool)
			ctx := context.Background()
			_, _ = pool.Exec(ctx, "UPDATE memberships SET role='admin' WHERE user_id=$1", subject)
			store := postgres.NewWorkStore(pool)
			config := domain.Config{Endpoint: "https://tasks.internal/mcp", CountryID: taskCountry, Token: "test-token"}
			if _, err := store.Connect(ctx, subject, project, config, make([]byte, 64)); err != nil {
				t.Fatal(err)
			}
			if err := store.Refresh(ctx, subject, project, taskUUID); err != nil {
				t.Fatal(err)
			}
			older, found, err := store.Claim(ctx)
			if err != nil || !found {
				t.Fatal(found, err)
			}
			_, err = pool.Exec(ctx, `INSERT INTO work_jobs(id,project_id,country_id,task_id,kind,request_key,document) VALUES($1,$2,$3,$4,'publication','synthetic-overlap','{}')`, otherRepository, project, taskCountry, taskUUID)
			if err != nil {
				t.Fatal(err)
			}
			newer, found, err := store.Claim(ctx)
			if err != nil || !found {
				t.Fatal(found, err)
			}
			task := domain.Task{CountryID: taskCountry, ID: taskUUID, Number: 1, Title: "Newer description", Status: "PLANNING", UpdatedAt: time.Now(), URL: "https://tasks.internal/task/1"}
			if err = store.Finish(ctx, newer, application.Outcome{Task: &task}); err != nil {
				t.Fatal(err)
			}
			task.Title = "Older description"
			olderOutcome := application.Outcome{Task: &task}
			if missing {
				olderOutcome = application.Outcome{Code: "task_not_found", Unavailable: true, RetryAfter: time.Minute}
			}
			if err = store.Finish(ctx, older, olderOutcome); err != nil {
				t.Fatal(err)
			}
			view, err := store.Task(ctx, reader, project, taskUUID)
			if err != nil || view.Task.Title != "Newer description" {
				t.Fatal("late read replaced newer source", view, err)
			}
			expected := 2
			if missing {
				expected = 1
			}
			if count(t, pool, "work_task_observations") != expected {
				t.Fatal("source history lost")
			}
		})
	}
}
