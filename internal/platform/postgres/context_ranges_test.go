package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"workgraph.local/platform/internal/access"
	captureapp "workgraph.local/platform/internal/capture/application"
	capture "workgraph.local/platform/internal/capture/domain"
	pack "workgraph.local/platform/internal/contextpack/domain"
	"workgraph.local/platform/internal/platform/postgres"
)

func TestPRContextReturnsOnlyItsRecordedMessageIntervals(t *testing.T) {
	pool := contextFixture(t)
	ctx := context.Background()
	seedLinkedMR(t, pool, true, 42)
	_, err := pool.Exec(ctx, "UPDATE commits SET confirmed_at=now()")
	if err != nil {
		t.Fatal(err)
	}
	store := postgres.NewContextStore(pool)
	principal := contextPrincipal(access.ReadCapture, access.ReadIntegration)
	q := pack.Query{ProjectID: project, RepositoryID: repository, MR: 7, DialogueID: snapshot().SessionID}
	legacy, err := store.Read(ctx, principal, q)
	if err != nil || !legacy.Scope.MissingCommitRanges || len(legacy.Transcript.Items) != 0 {
		t.Fatal("legacy scope was guessed", legacy, err)
	}
	source := snapshot()
	source.Revision = 2
	source.Commits = []string{strings.Repeat("a", 40), strings.Repeat("b", 40)}
	source.Messages = nil
	for i := 0; i < 60; i++ {
		text := "PR7 target"
		if i >= 30 {
			text = "PR8 foreign"
		}
		source.Messages = append(source.Messages, capture.Message{Role: "user", Text: text})
	}
	source.Messages[0].Text = strings.Repeat("🧪", 10000)
	toolRaw, _ := json.Marshal(map[string]any{"command": "git status --short", "output": strings.Repeat("x", 18000) + "DetailedToolTailMarker", "exit_code": 0})
	source.Messages[5] = capture.Message{Role: "tool", ToolName: "Bash", Text: string(toolRaw)}
	source.CommitRanges = []capture.CommitRange{{CommitSHA: source.Commits[0], Start: 0, End: 30, Basis: "observed_checkpoint"}, {CommitSHA: source.Commits[1], Start: 30, End: 60, Basis: "observed_checkpoint"}}
	if _, err := captureapp.NewService(postgres.NewCaptureStore(pool)).Ingest(ctx, subject, source); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, "UPDATE commits SET confirmed_at=now()")
	if err != nil {
		t.Fatal(err)
	}
	texts := map[int]string{}
	var firstCursor *pack.MessageCursor
	for pages := 0; pages < 10; pages++ {
		page, err := store.Read(ctx, principal, q)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(page)
		if len(raw) > 64*1024 {
			t.Fatal("unbounded page", len(raw))
		}
		if len(page.MergeRequests.Items) != 1 || page.MergeRequests.Items[0].IID != 7 {
			t.Fatal("PR scope lost", page)
		}
		if len(page.Captures.Items) != 1 || len(page.Captures.Items[0].MessageRanges) != 1 {
			t.Fatal("foreign interval included", page.Captures)
		}
		if len(page.Captures.Items[0].ConfirmedCommits) != 1 || page.Captures.Items[0].ConfirmedCommits[0] != source.Commits[0] {
			t.Fatal("foreign commit metadata included", page.Captures)
		}
		for _, item := range page.Transcript.Items {
			if item.Index >= 30 || strings.Contains(item.Message.Text, "foreign") {
				t.Fatal("foreign PR text", item.Index)
			}
			texts[item.Index] += item.Message.Text
		}
		if page.Transcript.Next == nil {
			break
		}
		if firstCursor == nil {
			copy := *page.Transcript.Next
			firstCursor = &copy
		}
		q.MessageAfter = page.Transcript.Next
	}
	if len(texts) != 30 || texts[0] != source.Messages[0].Text {
		t.Fatal("message/Unicode fragment lost", len(texts), len(texts[0]))
	}
	compactQuery := pack.Query{ProjectID: project, RepositoryID: repository, MR: 7, DialogueID: source.SessionID, MessageView: "compact"}
	preview, err := store.Read(ctx, principal, compactQuery)
	if err != nil {
		t.Fatal(err)
	}
	var detail *pack.MessageCursor
	for _, item := range preview.Transcript.Items {
		if item.Index == 5 {
			if !item.TextPreview || item.FullTextAfter == nil || strings.Contains(item.Message.Text, "DetailedToolTailMarker") || len(item.Message.Text) > 2000 {
				t.Fatal("unbounded or unmarked preview")
			}
			detail = item.FullTextAfter
		}
	}
	// The first page can end in the long user message; compact tools follow its cursor.
	for tries := 0; tries < 10 && detail == nil && preview.Transcript.Next != nil; tries++ {
		compactQuery.MessageAfter = preview.Transcript.Next
		preview, err = store.Read(ctx, principal, compactQuery)
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range preview.Transcript.Items {
			if item.Index == 5 {
				if !item.TextPreview || item.FullTextAfter == nil || len(item.Message.Text) > 2000 {
					t.Fatal("invalid preview")
				}
				detail = item.FullTextAfter
			}
		}
	}
	if detail == nil {
		t.Fatal("tool preview missing")
	}
	compactQuery.MessageView = "full"
	compactQuery.MessageAfter = detail
	expanded, err := store.Read(ctx, principal, compactQuery)
	if err != nil || len(expanded.Transcript.Items) == 0 || expanded.Transcript.Items[0].Index != 5 || expanded.Transcript.Items[0].TextPreview || !strings.HasPrefix(source.Messages[5].Text, expanded.Transcript.Items[0].Message.Text) {
		t.Fatal("exact detail unavailable", err)
	}
	bad := *firstCursor
	bad.Digest = strings.Repeat("f", 64)
	q.MessageAfter = &bad
	if _, err := store.Read(ctx, principal, q); !errors.Is(err, pack.ErrInvalid) {
		t.Fatal("forged revision cursor", err)
	}
	q.MessageAfter = firstCursor
	_, err = pool.Exec(ctx, "DELETE FROM memberships WHERE project_id=$1 AND user_id=$2", project, reader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(ctx, principal, q); !errors.Is(err, access.ErrDenied) {
		t.Fatal("revoked scoped read", err)
	}
}
