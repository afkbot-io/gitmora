package domain_test

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"workgraph.local/platform/internal/compatibility/domain"
)

func scope() domain.Scope {
	return domain.Scope{ObjectID: strings.Repeat("b", 40), RefObject: strings.Repeat("c", 40), TargetCommit: strings.Repeat("a", 40), Time: time.Date(2026, 10, 30, 10, 0, 0, 0, time.UTC)}
}
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestEntireV1SlicesActualCheckpointAndMakesOmissionsExplicit(t *testing.T) {
	s := scope()
	s.Ref = "refs/entire/checkpoints/XV/01M63VCB10XSZD4BQ2PQMWGPXV"
	p, err := domain.ParseEntire(s, "01M63VCB10XSZD4BQ2PQMWGPXV", fixture(t, "entire-0.12.2.metadata.json"), fixture(t, "entire-0.12.2.transcript.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	windows, err := p.Windows()
	if err != nil || len(windows) != 1 {
		t.Fatal(err)
	}
	w := windows[0]
	text := ""
	for _, m := range w.Messages {
		text += m.Text
	}
	if strings.Contains(text, "Earlier checkpoint") || strings.Contains(text, "excluded") || strings.Contains(text, "never-persist") {
		t.Fatal("wrong slice/redaction/reasoning policy", text)
	}
	if !strings.Contains(text, "Applying change") || !strings.Contains(text, "Success") || !strings.Contains(text, "Completed") {
		t.Fatal("source text lost", text)
	}
	if w.Origin.OmittedBlocks != 2 || w.Origin.SourceOffset != 1 || w.Origin.SourceSessionID != "65f60b2a-3de8-40fb-8c70-60d04b43b155" {
		t.Fatal(w.Origin)
	}
}
func TestGitAIMixedSourceClaimsDoNotInventHumanAttributionOrFetchTranscripts(t *testing.T) {
	s := scope()
	s.Ref = "refs/notes/ai"
	p, err := domain.ParseGitAI(s, fixture(t, "git-ai-authorship-3.note"))
	if err != nil {
		t.Fatal(err)
	}
	windows, err := p.Windows()
	if err != nil {
		t.Fatal(err)
	}
	w := windows[0]
	if len(w.Origin.Claims) != 4 || !w.Origin.ExternalTranscripts || w.Origin.BaseCommit == w.Origin.TargetCommit {
		t.Fatal(w.Origin)
	}
	human := 0
	for _, claim := range w.Origin.Claims {
		if claim.Kind == "known_human" {
			human++
		}
		if claim.Start == 4 || claim.Start == 8 || claim.Start == 11 {
			t.Fatal("unknown gap inferred")
		}
	}
	if human != 1 {
		t.Fatal("director/untracked ranges treated as human")
	}
	for _, m := range w.Messages {
		if m.Kind != "provenance_report" || strings.Contains(m.Text, "never-fetch") || strings.Contains(m.Text, "director@") {
			t.Fatal("fabricated conversation or retained external pointer")
		}
	}
}
func TestGitAIRejectsMalformedVersionsLookupsAndConflictingRanges(t *testing.T) {
	raw := string(fixture(t, "git-ai-authorship-3.note"))
	s := scope()
	s.Ref = "refs/notes/ai"
	for _, input := range []string{
		strings.Replace(raw, "authorship/3.0.0", "authorship/4.0.0", 1),
		strings.Replace(raw, "9-10", "3-10", 1),
		strings.Replace(raw, "src/main.go", "../outside", 1),
		strings.Replace(raw, "1-3,7", "0-3,7", 1),
		strings.Replace(raw, "s_1234567890abcd::t_abcdef12345678", "s_00000000000000::t_abcdef12345678", 1),
		strings.Replace(raw, "\"schema_version\":", "\"schema_version\":\"changed\",\"schema_version\":", 1),
	} {
		if _, err := domain.ParseGitAI(s, []byte(input)); err == nil {
			t.Fatal("invalid note accepted")
		}
	}
}
func TestEntireRejectsFutureVersionAndIncompleteSourceBeforePlanningWindows(t *testing.T) {
	s := scope()
	s.Ref = "refs/entire/checkpoints/XV/01M63VCB10XSZD4BQ2PQMWGPXV"
	metadata := fixture(t, "entire-0.12.2.metadata.json")
	raw := string(fixture(t, "entire-0.12.2.transcript.jsonl"))
	for _, input := range []string{strings.Replace(raw, "\"v\":1", "\"v\":2", 1), raw + "{broken", strings.Replace(raw, "\"name\":\"Write\"", "\"name\":42", 1)} {
		if _, err := domain.ParseEntire(s, "01M63VCB10XSZD4BQ2PQMWGPXV", metadata, []byte(input)); err == nil {
			t.Fatal("invalid source accepted")
		}
	}
}

func TestEntireLongUnicodeTextKeepsEveryRuneAcrossIndependentWindows(t *testing.T) {
	s := scope()
	s.Ref = "refs/entire/checkpoints/ab/abcdef1234ab"
	metadata := []byte(`{"checkpoint_id":"abcdef1234ab","session_id":"native","created_at":"2026-10-30T10:00:00Z","agent":"local","model":"local","files_touched":[]}`)
	text := strings.Repeat("Контекст🙂", 40000)
	raw, _ := json.Marshal(map[string]any{"v": 1, "type": "user", "content": text})
	projection, err := domain.ParseEntire(s, "abcdef1234ab", metadata, raw)
	if err != nil {
		t.Fatal(err)
	}
	windows, err := projection.Windows()
	if err != nil || len(windows) < 2 {
		t.Fatal("source was not windowed", err)
	}
	joined := ""
	for i, w := range windows {
		if w.Origin.Part != i+1 || w.Origin.Parts != len(windows) {
			t.Fatal("wrong window identity")
		}
		for _, m := range w.Messages {
			joined += m.Text
		}
	}
	if joined != text {
		t.Fatal("Unicode text clipped or repeated")
	}
	windows[0].Origin.Agent.Model = "changed"
	fresh, err := projection.Windows()
	if err != nil || fresh[0].Origin.Agent.Model != "local" {
		t.Fatal("projection leaked mutable origin", err)
	}
}

func TestSourceBudgetsAndEmptyExposedSliceAreExplicit(t *testing.T) {
	s := scope()
	s.Ref = "refs/notes/ai"
	if _, err := domain.ParseGitAI(s, make([]byte, domain.MaxSourceBytes+1)); !errors.Is(err, domain.ErrLimit) {
		t.Fatal("raw limit not enforced", err)
	}
	s.Ref = "refs/entire/checkpoints/ab/abcdef1234ab"
	metadata := []byte(`{"checkpoint_id":"abcdef1234ab","session_id":"native","created_at":"2026-10-30T10:00:00Z","agent":"local","model":"local","files_touched":[],"compact_transcript_start":1}`)
	projection, err := domain.ParseEntire(s, "abcdef1234ab", metadata, []byte("{\"v\":1,\"type\":\"assistant\",\"content\":null}\n"))
	if err != nil {
		t.Fatal(err)
	}
	windows, err := projection.Windows()
	if err != nil || len(windows) != 1 || windows[0].Messages[0].Kind != "import_notice" || windows[0].Origin.LineStart != 0 {
		t.Fatal("empty slice invented conversation/source row", err)
	}
}

func TestLongSourceSecretIsRedactedBeforeFragmentBoundaries(t *testing.T) {
	s := scope()
	s.Ref = "refs/entire/checkpoints/ab/abcdef1234ab"
	metadata := []byte(`{"checkpoint_id":"abcdef1234ab","session_id":"native","created_at":"2026-10-30T10:00:00Z","agent":"local","model":"local","files_touched":[]}`)
	// Construct a synthetic provider-token shape at the fragment boundary.
	token := strings.Join([]string{"glpat", "SecretBoundaryMarker1234567890"}, "-")
	text := strings.Repeat("x", 100*1024-4) + " " + token + " suffix"
	raw, _ := json.Marshal(map[string]any{"v": 1, "type": "user", "content": text})
	projection, err := domain.ParseEntire(s, "abcdef1234ab", metadata, raw)
	if err != nil {
		t.Fatal(err)
	}
	windows, err := projection.Windows()
	if err != nil {
		t.Fatal(err)
	}
	joined := ""
	for _, window := range windows {
		for _, message := range window.Messages {
			joined += message.Text
		}
	}
	if strings.Contains(joined, "SecretBoundaryMarker") {
		t.Fatal("known source credential crossed a fragment boundary before redaction")
	}
}
