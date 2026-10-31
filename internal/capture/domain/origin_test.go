package domain_test

import (
	"strings"
	"testing"

	"workgraph.local/platform/internal/capture/domain"
)

func importedSnapshot() domain.Snapshot {
	s := fixture()
	s.Agent.Name = "git-ai-import"
	s.Commits = []string{strings.Repeat("a", 40)}
	s.Messages = []domain.Message{{Role: "system", Kind: "provenance_report", Text: "Source-reported line provenance; uncovered lines are unknown."}}
	s.Origin = &domain.ImportOrigin{System: "git-ai", Format: "authorship/3.0.0", SourceID: s.Commits[0], ObjectID: strings.Repeat("b", 40), Ref: "refs/notes/ai", RefObject: strings.Repeat("c", 40), SourceDigest: strings.Repeat("d", 64), TargetCommit: s.Commits[0], BaseCommit: strings.Repeat("f", 40), Part: 1, Parts: 1, TimeKind: "commit_recorded", Claims: []domain.LineClaim{{Path: "src/main.go", Start: 1, End: 3, Key: "s_1234567890abcd::t_1234567890abcd", Kind: "ai", Agent: &domain.SourceAgent{Tool: "opencode", SessionID: "native", Model: "local"}}}}
	s.Files = []string{"src/main.go"}
	return s
}

func TestImportOriginIsOwnedAndKeepsExplicitUnknownCoverage(t *testing.T) {
	input := importedSnapshot()
	session, err := domain.NewSession(input)
	if err != nil {
		t.Fatal(err)
	}
	input.Origin.Claims[0].Agent.Model = "changed"
	input.Origin.Claims[0].End = 9
	owned := session.Snapshot()
	if owned.Origin.Claims[0].End != 3 || owned.Origin.Claims[0].Agent.Model != "local" {
		t.Fatal("origin alias")
	}
	owned.Origin.Claims[0].Agent.Tool = "changed"
	if session.Snapshot().Origin.Claims[0].Agent.Tool != "opencode" {
		t.Fatal("returned origin alias")
	}
	if len(session.Snapshot().Origin.Claims) != 1 {
		t.Fatal("untracked lines inferred")
	}
}

func TestImportOriginRejectsUnsupportedOrRetargetedClaims(t *testing.T) {
	tests := []struct {
		name   string
		change func(*domain.Snapshot)
	}{
		{"future version", func(s *domain.Snapshot) { s.Origin.Format = "authorship/4.0.0" }},
		{"foreign target", func(s *domain.Snapshot) { s.Origin.TargetCommit = strings.Repeat("e", 40) }},
		{"unknown kind", func(s *domain.Snapshot) { s.Origin.Claims[0].Kind = "human" }},
		{"bad path", func(s *domain.Snapshot) { s.Origin.Claims[0].Path = "../outside" }},
		{"empty range", func(s *domain.Snapshot) { s.Origin.Claims[0].Start = 0 }},
		{"invalid human", func(s *domain.Snapshot) { s.Origin.Claims[0].Kind = "known_human" }},
		{"wrong metadata role", func(s *domain.Snapshot) { s.Messages[0].Role = "assistant" }},
		{"missing origin", func(s *domain.Snapshot) { s.Origin = nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := importedSnapshot()
			tt.change(&s)
			if _, err := domain.NewSession(s); err == nil {
				t.Fatal("invalid import accepted")
			}
		})
	}
}
