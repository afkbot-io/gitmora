package domain_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	capture "workgraph.local/platform/internal/capture/domain"
	"workgraph.local/platform/internal/collector/domain"
)

const repoID = "11111111-1111-4111-8111-111111111111"
const sessionID = "22222222-2222-4222-8222-222222222222"

func observation() domain.Observation {
	return domain.Observation{SchemaVersion: 1, SourceSessionID: "native-session", EventID: "turn-1", Agent: capture.Agent{Name: "local", Model: "local-model"}, Messages: []capture.Message{{Role: "user", Text: "DB_PASSWORD=never-persist"}}, ChangedFiles: []string{"a.go", "b.go"}}
}

func TestRestoreOwnsNativeIdentityMetadata(t *testing.T) {
	o, err := domain.Normalize(observation())
	if err != nil {
		t.Fatal(err)
	}
	r, err := domain.NewRecording(repoID, sessionID, "tree", nil, o, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	state := r.State()
	state.NativeMessageIDs = []string{strings.Repeat("a", 64)}
	restored, err := domain.Restore(state)
	if err != nil {
		t.Fatal(err)
	}
	state.NativeMessageIDs[0] = strings.Repeat("b", 64)
	if restored.State().NativeMessageIDs[0] != strings.Repeat("a", 64) {
		t.Fatal("restore retained a mutable native identity alias")
	}
}

func TestRecordingSanitizesSealsAndCarriesOnlyPendingPaths(t *testing.T) {
	input, err := domain.Normalize(observation())
	if err != nil {
		t.Fatal(err)
	}
	recording, err := domain.NewRecording(repoID, sessionID, "worktree", nil, input, time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(input.CanonicalJSON()), "never-persist") {
		t.Fatal("unsanitized observation")
	}
	if !associate(t, recording, strings.Repeat("a", 40), []string{"a.go"}, nil) {
		t.Fatal("observed path did not associate")
	}
	first, changed, err := recording.Seal()
	if err != nil || !changed || first.Snapshot().Revision != 1 {
		t.Fatalf("seal: %v", err)
	}
	if _, changed, err := recording.Seal(); err != nil || changed {
		t.Fatal("unchanged recording created another revision")
	}
	if associate(t, recording, strings.Repeat("b", 40), []string{"a.go"}, nil) {
		t.Fatal("later unrelated human edit was attributed")
	}
	if !associate(t, recording, strings.Repeat("c", 40), []string{"b.go"}, []string{"b.go"}) {
		t.Fatal("remaining observed path was lost")
	}
	if !associate(t, recording, strings.Repeat("d", 40), []string{"b.go"}, nil) {
		t.Fatal("partial commit carry-forward was lost")
	}
	second, changed, err := recording.Seal()
	if err != nil || !changed || len(second.Snapshot().Commits) != 3 || second.Snapshot().Revision != 2 {
		t.Fatal("multi-commit evidence lost")
	}
}

func TestNativeSessionMappingIncludesWorktreeAndTaskScope(t *testing.T) {
	input, err := domain.Normalize(observation())
	if err != nil {
		t.Fatal(err)
	}
	base := domain.SessionKey(repoID, "tree-a", nil, input)
	if base == domain.SessionKey(repoID, "tree-b", nil, input) {
		t.Fatal("worktrees share native session mapping")
	}
	task := &capture.TaskRef{CountryID: repoID, TaskID: sessionID}
	if base == domain.SessionKey(repoID, "tree-a", task, input) {
		t.Fatal("task binding omitted from identity")
	}
	copy := input.Value()
	copy.EventID = "different-event"
	other, err := domain.Normalize(copy)
	if err != nil {
		t.Fatal(err)
	}
	if base != domain.SessionKey(repoID, "tree-a", nil, other) {
		t.Fatal("event identity incorrectly split one session")
	}
}

func TestRestoreAndRewritePreserveEvidence(t *testing.T) {
	input, _ := domain.Normalize(observation())
	recording, err := domain.NewRecording(repoID, sessionID, "tree", nil, input, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	old := strings.Repeat("a", 40)
	newSHA := strings.Repeat("b", 40)
	associate(t, recording, old, []string{"a.go"}, nil)
	if _, _, err := recording.Seal(); err != nil {
		t.Fatal(err)
	}
	restored, err := domain.Restore(recording.State())
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := restored.Rewrite(map[string]string{old: newSHA}); err != nil || !changed {
		t.Fatal("rewrite ignored linked commit")
	}
	sealed, changed, err := restored.Seal()
	if err != nil || !changed || len(sealed.Snapshot().Commits) != 2 {
		t.Fatal("rewrite discarded original evidence")
	}
	state := restored.State()
	state.Snapshot.Messages[0].Text = "mutated"
	if restored.State().Snapshot.Messages[0].Text == "mutated" {
		t.Fatal("state is not detached")
	}
}

func TestObservationRejectsInvalidIdentityPayloadAndPaths(t *testing.T) {
	for _, mutate := range []func(*domain.Observation){
		func(o *domain.Observation) { o.SchemaVersion = 2 }, func(o *domain.Observation) { o.EventID = "" },
		func(o *domain.Observation) { o.SourceSessionID = strings.Repeat("x", 201) }, func(o *domain.Observation) { o.Messages = nil },
		func(o *domain.Observation) { o.ChangedFiles = []string{"../outside"} }, func(o *domain.Observation) { o.Messages[0].Text = strings.Repeat("x", 128*1024+1) },
	} {
		input := observation()
		mutate(&input)
		if _, err := domain.Normalize(input); err == nil {
			t.Fatal("invalid observation accepted")
		}
	}
}

func associate(t *testing.T, r *domain.Recording, sha string, changed, remaining []string) bool {
	t.Helper()
	associated, err := r.Associate(sha, changed, remaining)
	if err != nil {
		t.Fatal(err)
	}
	return associated
}

func TestRejectedAssociationLeavesAggregateValid(t *testing.T) {
	input, _ := domain.Normalize(observation())
	recording, err := domain.NewRecording(repoID, sessionID, "tree", nil, input, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	state := recording.State()
	state.Snapshot.Commits = make([]string, 2000)
	for i := range state.Snapshot.Commits {
		state.Snapshot.Commits[i] = fmt.Sprintf("%040x", i+1)
	}
	recording, err = domain.Restore(state)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recording.Associate(strings.Repeat("f", 40), []string{"a.go"}, nil); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal("oversized association accepted")
	}
	if _, err := domain.Restore(recording.State()); err != nil {
		t.Fatal("failed transition corrupted aggregate")
	}
}

func TestOlderOrEditedObservationCannotEraseRecordedConversation(t *testing.T) {
	input, _ := domain.Normalize(observation())
	recording, err := domain.NewRecording(repoID, sessionID, "tree", nil, input, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	next := observation()
	next.EventID = "turn-2"
	next.Messages = append(next.Messages, capture.Message{Role: "assistant", Text: "Completed checks"})
	normalized, err := domain.Normalize(next)
	if err != nil {
		t.Fatal(err)
	}
	if err := recording.Observe(normalized); err != nil {
		t.Fatal(err)
	}
	if err := recording.Observe(input); !errors.Is(err, domain.ErrConflict) {
		t.Fatal("late older observation silently erased new evidence")
	}
	if len(recording.State().Snapshot.Messages) != 2 {
		t.Fatal("rejected observation changed current state")
	}
}

func TestIdenticalObservationDoesNotCreateAContentFreeRevision(t *testing.T) {
	input, _ := domain.Normalize(observation())
	recording, err := domain.NewRecording(repoID, sessionID, "tree", nil, input, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := recording.Seal(); err != nil {
		t.Fatal(err)
	}
	next := observation()
	next.EventID = "turn-2"
	next.ChangedFiles = nil
	normalized, err := domain.Normalize(next)
	if err != nil {
		t.Fatal(err)
	}
	if err := recording.Observe(normalized); err != nil {
		t.Fatal(err)
	}
	if _, changed, err := recording.Seal(); err != nil || changed {
		t.Fatal("identical evidence created a redundant revision")
	}
}

func TestInvalidObservedCommitWindowDoesNotPartiallyMutateRecording(t *testing.T) {
	input, _ := domain.Normalize(observation())
	recording, err := domain.NewRecording(repoID, sessionID, "tree", nil, input, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := recording.LinkObservedCommits([]string{strings.Repeat("a", 40), "invalid"}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal("invalid observed commit accepted")
	}
	if len(recording.State().Snapshot.Commits) != 0 {
		t.Fatal("failed observed window mutated recording")
	}
}
