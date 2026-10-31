package domain_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"workgraph.local/platform/internal/capture/domain"
)

func fixture() domain.Snapshot {
	return domain.Snapshot{
		SchemaVersion: 1, RepositoryID: "11111111-1111-4111-8111-111111111111",
		SessionID: "22222222-2222-4222-8222-222222222222", Revision: 1,
		Agent:     domain.Agent{Name: "local-agent", Model: "local-model"},
		StartedAt: time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC),
		Messages:  []domain.Message{{Role: "user", Text: "Fix the retry behavior"}, {Role: "assistant", Text: "Preserve failed deliveries"}},
		Files:     []string{"src/queue.go"}, Commits: []string{strings.Repeat("a", 40)},
	}
}

func TestSessionCanonicalIdentityAndImmutableSnapshot(t *testing.T) {
	input := fixture()
	input.Files = []string{"src/z.go", "src/a.go", "src/z.go"}
	input.Commits = []string{strings.Repeat("b", 40), strings.Repeat("a", 40), strings.Repeat("b", 40)}
	session, err := domain.NewSession(input)
	if err != nil {
		t.Fatal(err)
	}
	input.Messages[0].Text = "mutated after validation"
	output := session.Snapshot()
	output.Files[0] = "mutated.go"
	output.Messages[0].Text = "also mutated"
	stable := session.Snapshot()
	if stable.Messages[0].Text != "Fix the retry behavior" || stable.Files[0] != "src/a.go" {
		t.Fatal("aggregate shares mutable storage with caller")
	}
	if len(stable.Files) != 2 || len(stable.Commits) != 2 {
		t.Fatal("duplicate associations were not normalized")
	}
	reordered := fixture()
	reordered.Files = []string{"src/a.go", "src/z.go"}
	reordered.Commits = []string{strings.Repeat("a", 40), strings.Repeat("b", 40)}
	again, err := domain.NewSession(reordered)
	if err != nil {
		t.Fatal(err)
	}
	if again.Digest() != session.Digest() {
		t.Fatal("equivalent snapshots have different digests")
	}
	var decoded domain.Snapshot
	if err := json.Unmarshal(session.CanonicalJSON(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.SessionID != input.SessionID || len(session.Digest()) != 64 {
		t.Fatal("canonical representation lost identity")
	}
	bytes := session.CanonicalJSON()
	bytes[0] = '!'
	if session.CanonicalJSON()[0] != '{' {
		t.Fatal("canonical bytes are mutable")
	}
}

func TestSessionRejectsUntrustedOrUnboundedInput(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*domain.Snapshot)
	}{
		{"unsupported schema", func(s *domain.Snapshot) { s.SchemaVersion = 2 }},
		{"invalid repository", func(s *domain.Snapshot) { s.RepositoryID = "../../other" }},
		{"nil session", func(s *domain.Snapshot) { s.SessionID = "00000000-0000-0000-0000-000000000000" }},
		{"zero revision", func(s *domain.Snapshot) { s.Revision = 0 }},
		{"huge revision", func(s *domain.Snapshot) { s.Revision = 1_000_000_001 }},
		{"missing agent", func(s *domain.Snapshot) { s.Agent.Name = "" }},
		{"zero timestamp", func(s *domain.Snapshot) { s.StartedAt = time.Time{} }},
		{"time reversal", func(s *domain.Snapshot) { end := s.StartedAt.Add(-time.Second); s.EndedAt = &end }},
		{"missing task country", func(s *domain.Snapshot) { s.Task = &domain.TaskRef{TaskID: s.SessionID} }},
		{"no messages", func(s *domain.Snapshot) { s.Messages = nil }},
		{"unknown role", func(s *domain.Snapshot) { s.Messages[0].Role = "admin" }},
		{"tool without name", func(s *domain.Snapshot) { s.Messages[0].Role = "tool" }},
		{"oversized message", func(s *domain.Snapshot) { s.Messages[0].Text = strings.Repeat("x", 128*1024+1) }},
		{"invalid utf8", func(s *domain.Snapshot) { s.Messages[0].Text = string([]byte{0xff}) }},
		{"too many messages", func(s *domain.Snapshot) { s.Messages = make([]domain.Message, 5001) }},
		{"absolute path", func(s *domain.Snapshot) { s.Files = []string{"/etc/passwd"} }},
		{"parent path", func(s *domain.Snapshot) { s.Files = []string{"src/../../outside"} }},
		{"git internals", func(s *domain.Snapshot) { s.Files = []string{".git/config"} }},
		{"windows separator", func(s *domain.Snapshot) { s.Files = []string{`..\private`} }},
		{"noncanonical path", func(s *domain.Snapshot) { s.Files = []string{"src/../queue.go"} }},
		{"abbreviated sha", func(s *domain.Snapshot) { s.Commits = []string{"abc1234"} }},
		{"excess total bytes", func(s *domain.Snapshot) {
			s.Messages = make([]domain.Message, 20)
			for i := range s.Messages {
				s.Messages[i] = domain.Message{Role: "assistant", Text: strings.Repeat("x", 128*1024)}
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := fixture()
			tc.mutate(&input)
			if _, err := domain.NewSession(input); err == nil {
				t.Fatal("accepted invalid snapshot")
			}
		})
	}
}

func TestDigestIncludesEvidenceAndTaskScope(t *testing.T) {
	original, err := domain.NewSession(fixture())
	if err != nil {
		t.Fatal(err)
	}
	changed := fixture()
	changed.Messages[1].Text = "Different evidence"
	next, err := domain.NewSession(changed)
	if err != nil {
		t.Fatal(err)
	}
	if original.Digest() == next.Digest() {
		t.Fatal("evidence was omitted from digest")
	}
	changed = fixture()
	changed.Task = &domain.TaskRef{CountryID: changed.RepositoryID, TaskID: changed.SessionID}
	linked, err := domain.NewSession(changed)
	if err != nil {
		t.Fatal(err)
	}
	if original.Digest() == linked.Digest() {
		t.Fatal("task binding was omitted from digest")
	}
}

func TestUTCNormalizationAndSHA256Commit(t *testing.T) {
	input := fixture()
	input.StartedAt = input.StartedAt.In(time.FixedZone("MSK", 3*60*60))
	input.Commits = []string{strings.Repeat("b", 64)}
	session, err := domain.NewSession(input)
	if err != nil {
		t.Fatal(err)
	}
	if session.Snapshot().StartedAt.Location() != time.UTC {
		t.Fatal("timestamp was not normalized")
	}
}

func TestSnapshotCopiesOptionalTaskAndEndTime(t *testing.T) {
	input := fixture()
	end := input.StartedAt.Add(time.Minute)
	input.EndedAt = &end
	input.Task = &domain.TaskRef{CountryID: input.RepositoryID, TaskID: input.SessionID}
	input.Messages = append(input.Messages, domain.Message{Role: "tool", ToolName: "shell", Text: "Checks passed"})
	session, err := domain.NewSession(input)
	if err != nil {
		t.Fatal(err)
	}
	input.Task.TaskID = input.RepositoryID
	end = end.Add(time.Hour)
	output := session.Snapshot()
	output.Task.CountryID = input.SessionID
	*output.EndedAt = output.EndedAt.Add(time.Hour)
	stable := session.Snapshot()
	if stable.Task.TaskID != input.SessionID || stable.Task.CountryID != input.RepositoryID || !stable.EndedAt.Equal(input.StartedAt.Add(time.Minute)) {
		t.Fatal("optional fields share mutable storage")
	}
}
