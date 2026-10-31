package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"workgraph.local/platform/internal/access"
	app "workgraph.local/platform/internal/capture/application"
	capture "workgraph.local/platform/internal/capture/domain"
	"workgraph.local/platform/internal/platform/postgres"
	retrievalapp "workgraph.local/platform/internal/retrieval/application"
	retrieval "workgraph.local/platform/internal/retrieval/domain"
)

func TestCaptureImportOriginIsImmutableWithoutChangingOrdinaryCommitContext(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	service := app.NewService(postgres.NewCaptureStore(pool))
	input := snapshot()
	target := strings.Repeat("a", 40)
	input.Agent.Name = "git-ai-import"
	input.Messages = []capture.Message{{Role: "system", Kind: "provenance_report", Text: "Source report; unknown lines remain unknown."}}
	input.Commits = []string{target}
	input.Origin = &capture.ImportOrigin{System: "git-ai", Format: "authorship/3.0.0", SourceID: target, ObjectID: strings.Repeat("b", 40), Ref: "refs/notes/ai", RefObject: strings.Repeat("c", 40), SourceDigest: strings.Repeat("d", 64), TargetCommit: target, BaseCommit: strings.Repeat("f", 40), Part: 1, Parts: 1, TimeKind: "commit_recorded"}
	result, err := service.Ingest(ctx, subject, input)
	if err != nil || !result.Created {
		t.Fatal(err)
	}
	input.Revision = 2
	original := *input.Origin
	changed := original
	changed.ObjectID = strings.Repeat("e", 40)
	input.Origin = &changed
	if _, err := service.Ingest(ctx, subject, input); !errors.Is(err, app.ErrConflict) {
		t.Fatal("immutable origin retargeted", err)
	}
	input.Origin = &original
	input.Commits = append(input.Commits, strings.Repeat("e", 40))
	if _, err := service.Ingest(ctx, subject, input); err != nil {
		t.Fatal("explicit context cannot extend", err)
	}
	stored, err := service.Get(ctx, reader, input.RepositoryID, input.SessionID, 0)
	if err != nil || stored.Session.Snapshot().Origin.ObjectID != original.ObjectID || len(stored.Session.Snapshot().Commits) != 2 {
		t.Fatal("origin bytes/context changed", err)
	}
}

func TestImportedOriginIsIndexedQuotedAndRevokedUnderCaptureAuthority(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	input := snapshot()
	target := strings.Repeat("a", 40)
	input.Agent.Name = "git-ai-import"
	input.Messages = []capture.Message{{Role: "system", Kind: "provenance_report", Text: "Source-reported provenance"}}
	input.Commits = []string{target}
	input.Origin = &capture.ImportOrigin{System: "git-ai", Format: "authorship/3.0.0", SourceID: target, ObjectID: strings.Repeat("b", 40), Ref: "refs/notes/ai", RefObject: strings.Repeat("c", 40), SourceDigest: strings.Repeat("d", 64), TargetCommit: target, BaseCommit: strings.Repeat("f", 40), Part: 1, Parts: 1, TimeKind: "commit_recorded"}
	if _, err := app.NewService(postgres.NewCaptureStore(pool)).Ingest(ctx, subject, input); err != nil {
		t.Fatal(err)
	}
	store := postgres.NewRetrievalStore(pool)
	if worked, err := retrievalapp.NewIndexer(store).ReconcileOnce(ctx); err != nil || !worked {
		t.Fatal("origin index not accepted", err)
	}
	principal := access.Principal{UserID: reader, Scopes: []access.Permission{access.ReadCapture}}
	service := retrievalapp.NewService(store)
	result, err := service.Search(ctx, principal, retrieval.Query{ProjectID: project, Text: "authorship"})
	if err != nil || len(result.Items) != 1 {
		t.Fatal("origin not searchable", result, err)
	}
	hit := result.Items[0]
	if hit.Part != "import_origin" || !strings.Contains(hit.Title, "not verified authorship") {
		t.Fatal("source claim presented without provenance", hit)
	}
	expanded, err := service.Expand(ctx, principal, retrieval.Expansion{EvidenceID: hit.ID})
	if err != nil {
		t.Fatal(err)
	}
	var origin capture.ImportOrigin
	if err := json.Unmarshal([]byte(expanded.Content), &origin); err != nil || origin.ObjectID != input.Origin.ObjectID || origin.TargetCommit != target {
		t.Fatal("quoted origin identity lost", err)
	}
	if _, err := pool.Exec(ctx, "DELETE FROM memberships WHERE user_id=$1", reader); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Expand(ctx, principal, retrieval.Expansion{EvidenceID: hit.ID}); !errors.Is(err, retrieval.ErrNotFound) && !errors.Is(err, access.ErrDenied) {
		t.Fatal("origin escaped revoked membership", err)
	}
}
