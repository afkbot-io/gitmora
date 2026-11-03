package domain_test

import (
	"strings"
	"testing"
	"workgraph.local/platform/internal/capture/domain"
)

func TestMessageMetadataIsOwnedValidatedAndRedacted(t *testing.T) {
	input := fixture()
	input.Messages = append(input.Messages, domain.Message{Role: "tool", ToolName: "edit", Text: "done", Model: "api_key=" + strings.Join([]string{"synthetic", "model", "secret"}, "-"), Provider: "local", NativeID: "native", Changes: []domain.FileChange{{Path: "src/queue.go"}}})
	session, err := domain.Sanitize(input)
	if err != nil {
		t.Fatal(err)
	}
	input.Messages[2].Changes[0].Path = "mutated.go"
	value := session.Snapshot()
	value.Messages[2].Changes[0].Path = "again.go"
	stable := session.Snapshot()
	if stable.Messages[2].Changes[0].Path != "src/queue.go" || strings.Contains(stable.Messages[2].Model, "synthetic-model-secret") {
		t.Fatal("metadata aliases input or contains credentials")
	}
	stable.Messages[2].Changes[0].Path = "../outside"
	if _, err := domain.NewSession(stable); err == nil {
		t.Fatal("out-of-repository change allowed")
	}
	invalid := fixture()
	invalid.Messages[0].Model = "not-a-user-model"
	if _, err := domain.NewSession(invalid); err == nil {
		t.Fatal("model attributed to a user")
	}
}

func TestMetadataEnrichmentDoesNotPermitChangingKnownSourceFacts(t *testing.T) {
	old := domain.Message{Role: "tool", ToolName: "edit", Text: "source"}
	enriched := old
	enriched.Model = "alpha"
	enriched.NativeID = "part"
	enriched.Changes = []domain.FileChange{{Path: "a.go"}}
	if !enriched.Enriches(old) || enriched.Equal(old) {
		t.Fatal("additive metadata rejected or ignored")
	}
	changed := enriched
	changed.Model = "beta"
	if changed.Enriches(enriched) {
		t.Fatal("known execution model overwritten")
	}
	changed = enriched
	changed.Text = "rewritten"
	if changed.Enriches(old) {
		t.Fatal("enrichment rewrote source content")
	}
	changed = enriched
	changed.Changes = []domain.FileChange{{Path: "other.go"}}
	if changed.Enriches(enriched) {
		t.Fatal("known file association overwritten")
	}
}
