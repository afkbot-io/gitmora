package postgres_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	captureapp "workgraph.local/platform/internal/capture/application"
	capture "workgraph.local/platform/internal/capture/domain"
	"workgraph.local/platform/internal/platform/postgres"
)

func TestCaptureContinuationVerifiesExactParentAfterOutOfOrderDelivery(t *testing.T) {
	for _, mode := range []string{"matching", "wrong_digest", "foreign_author", "wrong_task"} {
		t.Run(mode, func(t *testing.T) {
			pool := database(t)
			seed(t, pool)
			ctx := context.Background()
			service := captureapp.NewService(postgres.NewCaptureStore(pool))
			parent := snapshot()
			parent.Task = &capture.TaskRef{CountryID: taskCountry, TaskID: taskUUID}
			canonical, err := capture.Sanitize(parent)
			if err != nil {
				t.Fatal(err)
			}
			child := parent
			child.SessionID = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"
			child.Messages = []capture.Message{{Role: "assistant", Kind: "agent_summary", Text: "Agent summary"}}
			child.Continuation = &capture.Continuation{RootSessionID: parent.SessionID, PreviousSessionID: parent.SessionID, PreviousRevision: 1, PreviousDigest: canonical.Digest(), Segment: 2, Reason: "compaction"}
			if mode == "wrong_digest" {
				child.Continuation.PreviousDigest = strings.Repeat("f", 64)
			}
			if mode == "wrong_task" {
				child.Task = &capture.TaskRef{CountryID: taskCountry, TaskID: parent.SessionID}
			}
			if _, err := service.Ingest(ctx, subject, child); err != nil {
				t.Fatal(err)
			}
			view, err := service.Get(ctx, reader, repository, child.SessionID, 0)
			if err != nil || view.ParentReferenceVerified {
				t.Fatal("missing parent verified", view, err)
			}
			author := subject
			if mode == "foreign_author" {
				if _, err := pool.Exec(ctx, `UPDATE memberships SET role='writer' WHERE user_id=$1`, reader); err != nil {
					t.Fatal(err)
				}
				author = reader
			}
			if _, err := service.Ingest(ctx, author, parent); err != nil {
				t.Fatal(err)
			}
			view, err = service.Get(ctx, reader, repository, child.SessionID, 0)
			if err != nil || view.ParentReferenceVerified != (mode == "matching") {
				t.Fatal("incorrect parent trust", view.ParentReferenceVerified, err)
			}
		})
	}
}

func TestLegacyCaptureIdentityAndCanonicalBytesRemainCompatible(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	input := snapshot()
	input.Commits = nil
	session, err := capture.Sanitize(input)
	if err != nil {
		t.Fatal(err)
	}
	input = session.Snapshot()
	oldIdentity := struct {
		StartedAt time.Time        `json:"started_at"`
		Agent     string           `json:"agent"`
		Task      *capture.TaskRef `json:"task"`
	}{input.StartedAt, input.Agent.Name, input.Task}
	raw, err := json.Marshal(oldIdentity)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	if _, err := pool.Exec(ctx, `INSERT INTO capture_sessions(repository_id,session_id,originator_id,identity_digest,latest_revision) VALUES($1,$2,$3,$4,1)`, repository, input.SessionID, subject, fmt.Sprintf("%x", digest)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO capture_revisions(repository_id,session_id,revision,digest,canonical) VALUES($1,$2,1,$3,$4)`, repository, input.SessionID, session.Digest(), session.CanonicalJSON()); err != nil {
		t.Fatal(err)
	}
	input.Revision = 2
	input.Messages = append(input.Messages, capture.Message{Role: "assistant", Text: "Later legacy-compatible observation"})
	service := captureapp.NewService(postgres.NewCaptureStore(pool))
	if _, err := service.Ingest(ctx, subject, input); err != nil {
		t.Fatal("optional continuation changed legacy identity", err)
	}
	old, err := service.Get(ctx, reader, repository, input.SessionID, 1)
	if err != nil || old.Session.Digest() != session.Digest() {
		t.Fatal("legacy canonical evidence changed", err)
	}
}
