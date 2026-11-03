package domain_test

import (
	"strings"
	"testing"

	"workgraph.local/platform/internal/retrieval/domain"
)

const project = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
const repo = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
const session = "cccccccc-cccc-4ccc-8ccc-cccccccccccc"

func TestChunksPreserveUnicodeProvenanceAndOverlap(t *testing.T) {
	ref := domain.Source{Kind: "capture", ProjectID: project, RepositoryID: repo, SessionID: session, Revision: 4, Digest: strings.Repeat("a", 64)}
	text := strings.Repeat("Авторизация🧪 ", 600)
	chunks, err := domain.Chunks(ref, "session", "messages/2", "Historical tool observation", text)
	if err != nil || len(chunks) < 2 {
		t.Fatal(err)
	}
	runes := []rune(text)
	for i, c := range chunks {
		if c.Content != string(runes[c.Start:c.End]) || len(c.ID) != 64 || len(c.ContentDigest) != 64 {
			t.Fatal("invalid citation", c)
		}
		if i > 0 && c.Start >= chunks[i-1].End {
			t.Fatal("missing overlap")
		}
	}
	retry, _ := domain.Chunks(ref, "session", "messages/2", "Historical tool observation", text)
	if retry[0].ID != chunks[0].ID {
		t.Fatal("unstable expansion identity")
	}
	changed := ref
	changed.Revision++
	other, _ := domain.Chunks(changed, "session", "messages/2", "Historical tool observation", text)
	if other[0].ID == chunks[0].ID {
		t.Fatal("revision identity collapsed")
	}
}

func TestQueriesAreBoundedAndDoNotTurnCodeFiltersIntoAuthority(t *testing.T) {
	valid := domain.Query{ProjectID: project, Text: "refreshToken авторизация", Limit: 8}
	q, err := domain.NormalizeQuery(valid)
	if err != nil || q.Terms == q.Text {
		t.Fatal("code tokenization missing", q, err)
	}
	for _, bad := range []domain.Query{{ProjectID: project}, {ProjectID: "12", Text: "test"}, {ProjectID: project, Text: "test", Limit: 51}, {ProjectID: project, Text: "x", Kinds: []string{"unknown"}}, {ProjectID: project, Text: strings.Repeat("x", 513)}} {
		if _, err := domain.NormalizeQuery(bad); err == nil {
			t.Fatal("unbounded/ambiguous query accepted", bad)
		}
	}
	sha := strings.Repeat("a", 40)
	q, err = domain.NormalizeQuery(domain.Query{ProjectID: project, Text: sha})
	if err != nil || q.CommitSHA != sha || q.Text != "" {
		t.Fatal("SHA not normalized to an exact confirmed relation filter", q, err)
	}
}

func TestExpansionUsesRuneLimitsAndExactContentDigest(t *testing.T) {
	c := domain.Chunk{ID: strings.Repeat("a", 64), Content: "АБ🧪ГД", Start: 40, End: 45}
	expanded, err := domain.Expand(c, domain.Expansion{EvidenceID: c.ID, Offset: 1, Limit: 2})
	if err != nil || expanded.Content != "Б🧪" || expanded.Start != 41 || expanded.End != 43 || expanded.NextOffset != 3 {
		t.Fatal(expanded, err)
	}
	if _, err := domain.Expand(c, domain.Expansion{EvidenceID: c.ID, Offset: 6}); err == nil {
		t.Fatal("invalid range accepted")
	}
}

func TestCodeChunksAndPartialExpansionsPreserveLinesAcrossOverlap(t *testing.T) {
	ref := domain.Source{Kind: "code", ProjectID: project, RepositoryID: repo, CommitSHA: strings.Repeat("a", 40), FilePath: "src/auth.go", BlobID: strings.Repeat("b", 40), Digest: strings.Repeat("c", 64)}
	text := strings.Repeat("// Авторизация🧪\n\n", 300)
	chunks, err := domain.Chunks(ref, "code", "content", ref.FilePath, text)
	if err != nil || len(chunks) < 2 {
		t.Fatal(chunks, err)
	}
	runes := []rune(text)
	for _, c := range chunks {
		if c.LineStart != 1+strings.Count(string(runes[:c.Start]), "\n") || c.LineEnd != 1+strings.Count(string(runes[:c.End-1]), "\n") {
			t.Fatal("chunk overlap shifted original lines", c)
		}
		quote, err := domain.Expand(c, domain.Expansion{EvidenceID: c.ID, Offset: 13, Limit: 42})
		if err != nil || quote.Content != string(runes[quote.Start:quote.End]) || quote.LineStart != 1+strings.Count(string(runes[:quote.Start]), "\n") || quote.LineEnd != 1+strings.Count(string(runes[:quote.End-1]), "\n") {
			t.Fatal("partial quote shifted original lines", quote, err)
		}
		empty, err := domain.Expand(c, domain.Expansion{EvidenceID: c.ID, Offset: len([]rune(c.Content))})
		if err != nil || empty.Content != "" || empty.LineStart != 0 || empty.LineEnd != 0 {
			t.Fatal("empty quote claimed source lines", empty, err)
		}
	}
}

func TestExactSessionSelectorUsesExplicitProjectAndOptionalRepository(t *testing.T) {
	q := domain.Query{ProjectID: project, RepositoryID: repo, SessionID: "22222222-2222-4222-8222-222222222222"}
	if _, err := domain.NormalizeQuery(q); err != nil {
		t.Fatal(err)
	}
	q.RepositoryID = ""
	if _, err := domain.NormalizeQuery(q); err != nil {
		t.Fatal("project-scoped session selector rejected", err)
	}
}

func TestSearchSupportsSessionUUIDAndBoundedMetadataFilters(t *testing.T) {
	q, err := domain.NormalizeQuery(domain.Query{ProjectID: project, Text: "22222222-2222-4222-8222-222222222222", Agent: " CODEX "})
	if err != nil || q.SessionID != "22222222-2222-4222-8222-222222222222" || q.Agent != "codex" || q.Terms != "" {
		t.Fatal(q, err)
	}
	q, err = domain.NormalizeQuery(domain.Query{ProjectID: project, Text: "ABC1234"})
	if err != nil || q.CommitPrefix != "abc1234" || q.Terms != "" {
		t.Fatal(q, err)
	}
	if _, err = domain.NormalizeQuery(domain.Query{ProjectID: project, Model: "alpha"}); err != nil {
		t.Fatal("model-only query rejected", err)
	}
	if _, err = domain.NormalizeQuery(domain.Query{ProjectID: project, Role: "unknown"}); err == nil {
		t.Fatal("invalid role accepted")
	}
	if _, err = domain.NormalizeQuery(domain.Query{ProjectID: project, CommitPrefix: "a"}); err == nil {
		t.Fatal("unbounded/short prefix accepted")
	}
}
