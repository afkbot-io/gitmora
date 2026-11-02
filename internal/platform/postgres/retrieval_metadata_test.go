package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"workgraph.local/platform/internal/access"
	captureapp "workgraph.local/platform/internal/capture/application"
	capture "workgraph.local/platform/internal/capture/domain"
	"workgraph.local/platform/internal/platform/postgres"
	"workgraph.local/platform/internal/retrieval/application"
	"workgraph.local/platform/internal/retrieval/domain"
)

func TestSearchMetadataPagesExactCommitRangesAndFileEvidence(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	a := "a123456" + strings.Repeat("0", 33)
	b := "b123456" + strings.Repeat("0", 33)
	snap := snapshot()
	snap.Agent = capture.Agent{Name: "opencode", Model: "last-session-configuration"}
	snap.Commits = []string{a, b}
	snap.Messages = nil
	snap.Files = []string{"src/auth.go"}
	for i := 0; i < 60; i++ {
		model := "alpha"
		if i >= 30 {
			model = "beta"
		}
		snap.Messages = append(snap.Messages, capture.Message{Role: "tool", ToolName: "edit", Text: fmt.Sprintf("PaginationMarker step %d", i), Model: model, Provider: "local", NativeID: fmt.Sprintf("part-%d", i), Changes: []capture.FileChange{{Path: "src/auth.go"}}})
	}
	snap.Messages[0].Text = "FarAlpha " + strings.Repeat("PaginationMarker prefix ", 300) + " DeepNeedleTail FarOmega"
	snap.CommitRanges = []capture.CommitRange{{CommitSHA: a, Start: 0, End: 30, Basis: "observed_checkpoint"}, {CommitSHA: b, Start: 30, End: 60, Basis: "observed_checkpoint"}}
	if _, err := captureapp.NewService(postgres.NewCaptureStore(pool)).Ingest(ctx, subject, snap); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE commits SET confirmed_at=now()`); err != nil {
		t.Fatal(err)
	}
	store := postgres.NewRetrievalStore(pool)
	service := application.NewService(store)
	if worked, err := application.NewIndexer(store).ReconcileOnce(ctx); err != nil || !worked {
		t.Fatal(worked, err)
	}
	p := access.Principal{UserID: reader, Scopes: []access.Permission{access.ReadCapture}}
	query := domain.Query{ProjectID: project, RepositoryID: repository, Text: "PaginationMarker", CommitSHA: a, Model: "ALPHA", Role: "tool", Limit: 7}
	seen := map[string]bool{}
	parts := map[string]bool{}
	pages := 0
	for {
		result, err := service.Search(ctx, p, query)
		if err != nil {
			t.Fatal(err)
		}
		pages++
		for _, hit := range result.Items {
			if seen[hit.ID] || parts[hit.Part] || hit.Metadata.Model != "alpha" || hit.Metadata.ModelBasis != "message_metadata" || hit.Metadata.Relation != "tool_reported" {
				t.Fatal("duplicate or wrong model", hit.Part, hit.Metadata)
			}
			seen[hit.ID] = true
			parts[hit.Part] = true
		}
		if result.Next == nil {
			if result.HasMore {
				t.Fatal("missing cursor")
			}
			break
		}
		changed := query
		changed.Model = "beta"
		changed.After = result.Next
		if _, err := service.Search(ctx, p, changed); !errors.Is(err, domain.ErrInvalid) {
			t.Fatal("cursor reused for another model", err)
		}
		query.After = result.Next
	}
	if len(parts) != 30 || pages != 5 {
		t.Fatal("missing/duplicated page records", len(parts), pages)
	}
	query = domain.Query{ProjectID: project, RepositoryID: repository, CommitSHA: a, FilePath: "src/auth.go", Text: "DeepNeedleTail"}
	result, err := service.Search(ctx, p, query)
	if err != nil || len(result.Items) != 1 || result.Items[0].Start == 0 || len(result.Items[0].FilePaths) != 1 || result.Items[0].FilePaths[0] != "src/auth.go" {
		t.Fatal("later chunk lost path association", result, err)
	}
	far, farErr := service.Search(ctx, p, domain.Query{ProjectID: project, Text: "FarAlpha FarOmega"})
	if farErr != nil || len(far.Items) != 1 || far.Items[0].Part != "messages/0" {
		t.Fatal("words separated by a chunk boundary disappeared", far, farErr)
	}
	hit := result.Items[0]
	expanded, err := service.Expand(ctx, p, domain.Expansion{EvidenceID: hit.ID})
	if err != nil || expanded.Metadata != hit.Metadata {
		t.Fatal("expansion dropped attribution", err)
	}
	// A confirmed but unrelated commit is outside this token's available source set.
	unrelated := "a123456" + strings.Repeat("1", 33)
	if _, err = pool.Exec(ctx, `INSERT INTO commits(repository_id,sha,confirmed_at) VALUES($1,$2,now())`, repository, unrelated); err != nil {
		t.Fatal(err)
	}
	prefix, err := service.Search(ctx, p, domain.Query{ProjectID: project, Text: "A123456"})
	if err != nil || prefix.ResolvedCommitSHA != a {
		t.Fatal("prefix leaked an unavailable source or failed resolution", prefix.ResolvedCommitSHA, err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO capture_revision_commits(repository_id,session_id,revision,sha) VALUES($1,$2,1,$3)`, repository, snap.SessionID, unrelated); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Search(ctx, p, domain.Query{ProjectID: project, Text: "a123456"}); !errors.Is(err, domain.ErrAmbiguous) {
		t.Fatal("ambiguous SHA silently chosen", err)
	}
	if _, err = pool.Exec(ctx, `DELETE FROM memberships WHERE user_id=$1`, reader); err != nil {
		t.Fatal(err)
	}
	if _, err = service.Search(ctx, p, query); !errors.Is(err, access.ErrDenied) {
		t.Fatal("revoked path search allowed", err)
	}
}

func TestSearchProjectionUpgradeKeepsOldEvidenceReadable(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	snap := snapshot()
	snap.Messages[0].Text = "ProjectionUpgradeMarker"
	if _, err := captureapp.NewService(postgres.NewCaptureStore(pool)).Ingest(ctx, subject, snap); err != nil {
		t.Fatal(err)
	}
	store := postgres.NewRetrievalStore(pool)
	indexer := application.NewIndexer(store)
	service := application.NewService(store)
	if _, err := indexer.ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	p := access.Principal{UserID: reader, Scopes: []access.Permission{access.ReadCapture}}
	query := domain.Query{ProjectID: project, Text: "ProjectionUpgradeMarker"}
	before, err := service.Search(ctx, p, query)
	if err != nil || len(before.Items) != 1 {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE retrieval_jobs SET projection_version=1`); err != nil {
		t.Fatal(err)
	}
	pending, err := service.Search(ctx, p, query)
	if err != nil || len(pending.Items) != 1 || pending.PendingSources != 1 {
		t.Fatal("upgrade hid acknowledged source", pending, err)
	}
	// Fresh captures must not wait behind a potentially large metadata backfill.
	if _, err = pool.Exec(ctx, `UPDATE retrieval_jobs SET available_at=now()-interval '1 day'`); err != nil {
		t.Fatal(err)
	}
	fresh := snap
	fresh.SessionID = "33333333-3333-4333-8333-333333333333"
	fresh.Messages = []capture.Message{{Role: "assistant", Text: "FreshBeforeBackfillMarker"}}
	if _, err = captureapp.NewService(postgres.NewCaptureStore(pool)).Ingest(ctx, subject, fresh); err != nil {
		t.Fatal(err)
	}
	if worked, err := indexer.ReconcileOnce(ctx); err != nil || !worked {
		t.Fatal(worked, err)
	}
	discovered, err := service.Search(ctx, p, domain.Query{ProjectID: project, Text: "FreshBeforeBackfillMarker"})
	if err != nil || len(discovered.Items) != 1 || discovered.PendingSources != 1 {
		t.Fatal("new capture waited behind metadata backfill", discovered, err)
	}
	if worked, err := indexer.ReconcileOnce(ctx); err != nil || !worked {
		t.Fatal(worked, err)
	}
	after, err := service.Search(ctx, p, query)
	if err != nil || len(after.Items) != 1 || after.Items[0].ID != before.Items[0].ID || after.Items[0].Source.Digest != before.Items[0].Source.Digest || after.PendingSources != 0 {
		t.Fatal("immutable evidence changed during reindex", after, err)
	}
}

func TestGitAILineReportsStayReportedAndPinnedToTheirTarget(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	snap := snapshot()
	a := strings.Repeat("a", 40)
	b := strings.Repeat("b", 40)
	snap.Agent.Name = "git-ai-import"
	snap.Files = []string{"src/auth.go"}
	snap.Commits = []string{a, b}
	snap.Messages = []capture.Message{{Role: "system", Kind: "provenance_report", Text: "Imported line report"}}
	snap.Origin = &capture.ImportOrigin{System: "git-ai", Format: "authorship/3.0.0", SourceID: a, ObjectID: strings.Repeat("c", 40), Ref: "refs/notes/ai", RefObject: strings.Repeat("d", 40), SourceDigest: strings.Repeat("e", 64), TargetCommit: a, BaseCommit: strings.Repeat("f", 40), Part: 1, Parts: 1, TimeKind: "commit_recorded", Claims: []capture.LineClaim{{Path: "src/auth.go", Start: 2, End: 5, Key: strings.Repeat("0", 16), Kind: "ai", Agent: &capture.SourceAgent{Tool: "codex", SessionID: "native", Model: "reported-model"}}}}
	if _, err := captureapp.NewService(postgres.NewCaptureStore(pool)).Ingest(ctx, subject, snap); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE commits SET confirmed_at=now()`); err != nil {
		t.Fatal(err)
	}
	store := postgres.NewRetrievalStore(pool)
	if _, err := application.NewIndexer(store).ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	service := application.NewService(store)
	p := access.Principal{UserID: reader, Scopes: []access.Permission{access.ReadCapture}}
	query := domain.Query{ProjectID: project, RepositoryID: repository, CommitSHA: a, FilePath: "src/auth.go"}
	result, err := service.Search(ctx, p, query)
	if err != nil || len(result.Items) != 1 {
		t.Fatal(result, err)
	}
	m := result.Items[0].Metadata
	if m.Relation != "git_ai_reported" || m.ModelBasis != "import_report" || m.Model != "reported-model" || m.ReportedCommit != a || m.ReportedLineStart != 2 || m.ReportedLineEnd != 5 {
		t.Fatal("line claim upgraded or misrepresented", m)
	}
	query.CommitSHA = b
	if result, err = service.Search(ctx, p, query); err != nil || len(result.Items) != 0 {
		t.Fatal("claim escaped its target commit", result, err)
	}
}

func TestUnresolvedShortHexSearchFallsBackToTextAndPinsThatMode(t *testing.T) {
	pool := database(t)
	seed(t, pool)
	ctx := context.Background()
	snap := snapshot()
	snap.Messages = []capture.Message{{Role: "assistant", Text: "Literal abc12de first"}, {Role: "assistant", Text: "Literal abc12de second"}, {Role: "assistant", Text: "Literal abc12de third"}}
	if _, err := captureapp.NewService(postgres.NewCaptureStore(pool)).Ingest(ctx, subject, snap); err != nil {
		t.Fatal(err)
	}
	store := postgres.NewRetrievalStore(pool)
	if _, err := application.NewIndexer(store).ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	service := application.NewService(store)
	p := access.Principal{UserID: reader, Scopes: []access.Permission{access.ReadCapture}}
	query := domain.Query{ProjectID: project, Text: "ABC12DE", Limit: 1}
	first, err := service.Search(ctx, p, query)
	if err != nil || len(first.Items) != 1 || first.Next == nil || !first.Next.Literal {
		t.Fatal("hex text incorrectly required a commit", first, err)
	}
	sha := "abc12de" + strings.Repeat("0", 33)
	if _, err := pool.Exec(ctx, `WITH added AS (INSERT INTO commits(repository_id,sha,confirmed_at) VALUES($1,$2,now()) RETURNING repository_id,sha) INSERT INTO capture_revision_commits(repository_id,session_id,revision,sha) SELECT repository_id,$3,1,sha FROM added`, repository, sha, snap.SessionID); err != nil {
		t.Fatal(err)
	}
	query.After = first.Next
	next, err := service.Search(ctx, p, query)
	if err != nil || len(next.Items) != 1 || next.Items[0].ID == first.Items[0].ID || next.Next == nil || !next.Next.Literal {
		t.Fatal("page silently changed from text into commit search", next, err)
	}
	if _, err = service.Search(ctx, p, domain.Query{ProjectID: project, CommitPrefix: "def1234"}); !errors.Is(err, domain.ErrNotFound) {
		t.Fatal("explicit unknown commit prefix silently changed meaning", err)
	}
}
