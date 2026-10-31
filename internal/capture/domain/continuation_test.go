package domain_test

import (
	"strings"
	"testing"

	"workgraph.local/platform/internal/capture/domain"
)

func TestContinuationIsImmutableTypedEvidenceAndSummariesAreExplicit(t *testing.T) {
	input := fixture()
	input.Continuation = &domain.Continuation{RootSessionID: "33333333-3333-4333-8333-333333333333", PreviousSessionID: "33333333-3333-4333-8333-333333333333", PreviousRevision: 4, PreviousDigest: strings.Repeat("a", 64), Segment: 2, Reason: "compaction"}
	input.Messages = []domain.Message{{Role: "assistant", Kind: "agent_summary", Text: "Agent-produced summary"}}
	session, err := domain.NewSession(input)
	if err != nil {
		t.Fatal(err)
	}
	copy := session.Snapshot()
	copy.Continuation.Reason = "history_replacement"
	if session.Snapshot().Continuation.Reason != "compaction" {
		t.Fatal("mutable continuation alias")
	}
	for _, reason := range []string{"unknown", ""} {
		input.Continuation.Reason = reason
		if _, err := domain.NewSession(input); err == nil {
			t.Fatal("invalid continuation reason accepted")
		}
	}
	input.Continuation.Reason = "compaction"
	input.Continuation.PreviousSessionID = input.SessionID
	if _, err := domain.NewSession(input); err == nil {
		t.Fatal("self parent accepted")
	}
}
