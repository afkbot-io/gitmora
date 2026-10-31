package domain_test

import (
	"strings"
	"testing"
	"time"
	"workgraph.local/platform/internal/forge/domain"
)

func TestMergeRequestCodeIdentityPreservesHeadsAndSeparatesMetadataFromLineage(t *testing.T) {
	model := domain.MergeRequest{ID: 100, IID: 1, ProjectID: 42, SourceProjectID: 42, State: "opened", Head: strings.Repeat("a", 40), Base: strings.Repeat("b", 40), Start: strings.Repeat("c", 40), Title: "DB_PASSWORD=never-persist", UpdatedAt: time.Now()}
	normalized, err := domain.NormalizeMR(model)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(normalized.Title, "never-persist") {
		t.Fatal("raw provider secret retained")
	}
	changed := normalized
	changed.Title = "A new title"
	changed.UpdatedAt = changed.UpdatedAt.Add(time.Second)
	if changed.CodeKey() != normalized.CodeKey() || changed.Digest() == normalized.Digest() {
		t.Fatal("metadata update replaced code identity")
	}
	changed.Head = strings.Repeat("d", 40)
	if changed.CodeKey() == normalized.CodeKey() {
		t.Fatal("head change did not invalidate code identity")
	}
	pipeline := domain.Pipeline{ProjectID: 42, SHA: normalized.Head, Status: "success"}
	if !pipeline.AppliesTo(normalized) || pipeline.AppliesTo(changed) {
		t.Fatal("historical CI success was applied to a changed head")
	}
	fork := normalized
	fork.SourceProjectID = 99
	if pipeline.AppliesTo(fork) {
		t.Fatal("target pipeline was applied to a fork's matching SHA")
	}
	changed = normalized
	changed.State = "merged"
	changed.Squash = strings.Repeat("e", 40)
	if changed.CodeKey() == normalized.CodeKey() {
		t.Fatal("merge result was conflated with an open head")
	}
}

func TestProviderRedactionCannotExpandMetadataPastItsBound(t *testing.T) {
	_, err := domain.NormalizeMR(domain.MergeRequest{ID: 1, IID: 1, ProjectID: 42, State: "opened", Description: strings.Repeat("DB_PASSWORD=x ", 60000), UpdatedAt: time.Now()})
	if err == nil {
		t.Fatal("sanitized provider metadata exceeds the declared bound")
	}
}
