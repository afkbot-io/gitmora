package domain_test

import (
	"errors"
	"strings"
	"testing"

	"workgraph.local/platform/internal/collector/domain"
)

func TestFrontierKeepsFirstObservedBoundaries(t *testing.T) {
	source := domain.NativeSource{Worktree: "tree", Agent: "opencode", SessionID: "opencode:ses_1"}
	a, b, c := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	f, err := domain.BeginFrontier(repoID, source, "call", a)
	if err != nil {
		t.Fatal(err)
	}
	if f.Status() != "started" {
		t.Fatal(f.Status())
	}
	if err = f.End(b); err != nil {
		t.Fatal(err)
	}
	if err = f.End(c); err != nil || *f.After != b {
		t.Fatal("end retry replaced observed boundary", err)
	}
	if err = f.Resolve(c, "operator chose another end"); !errors.Is(err, domain.ErrConflict) {
		t.Fatal(err)
	}
	if err = f.Bind("22222222-2222-4222-8222-222222222222"); err != nil {
		t.Fatal(err)
	}
	if err = f.Apply(); err != nil || f.Status() != "applied" {
		t.Fatal(err)
	}
	if _, err = domain.RestoreFrontier(f); err != nil {
		t.Fatal(err)
	}
}

func TestFrontierUnknownEndNeedsExplicitResolution(t *testing.T) {
	source := domain.NativeSource{Worktree: "tree", Agent: "opencode", SessionID: "opencode:ses_1"}
	f, err := domain.BeginFrontier(repoID, source, "call", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Bind(repoID); !errors.Is(err, domain.ErrConflict) {
		t.Fatal(err)
	}
	if err = f.Apply(); !errors.Is(err, domain.ErrConflict) {
		t.Fatal(err)
	}
	if err = f.Resolve(strings.Repeat("b", 40), ""); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal(err)
	}
	if err = f.Resolve(strings.Repeat("b", 40), "verified from actual tool commit"); err != nil {
		t.Fatal(err)
	}
	if f.Status() != "ended" || f.Reason == "" {
		t.Fatal(f)
	}
	other, err := domain.BeginFrontier(repoID, source, "other", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = other.Abandon("tool never executed"); err != nil || other.Status() != "abandoned" {
		t.Fatal(err)
	}
	if err = other.End(strings.Repeat("b", 40)); !errors.Is(err, domain.ErrConflict) {
		t.Fatal(err)
	}
	if err = other.Abandon("different reason"); !errors.Is(err, domain.ErrConflict) {
		t.Fatal(err)
	}
}

func TestFrontierOperatorReasonRedactsAndLocatorsRejectKnownSecrets(t *testing.T) {
	source := domain.NativeSource{Worktree: "tree", Agent: "opencode", SessionID: "opencode:ses_1"}
	f, err := domain.BeginFrontier(repoID, source, "call", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Resolve(strings.Repeat("b", 40), "DB_PASSWORD=never-store-this"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(f.Reason, "never-store-this") {
		t.Fatal("raw reason persisted")
	}
	if err := f.Resolve(strings.Repeat("b", 40), "DB_PASSWORD=never-store-this"); err != nil {
		t.Fatal("sanitized retry changed intent", err)
	}
	source.SessionID = "token=never-store-this"
	if _, err := domain.BeginFrontier(repoID, source, "call", ""); !errors.Is(err, domain.ErrInvalid) {
		t.Fatal("credential-shaped locator retained", err)
	}
}
