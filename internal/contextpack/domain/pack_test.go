package domain_test

import (
	"strings"
	"testing"
	"workgraph.local/platform/internal/contextpack/domain"
	forge "workgraph.local/platform/internal/forge/domain"
)

const project = "55555555-5555-4555-8555-555555555555"
const repository = "11111111-1111-4111-8111-111111111111"

func TestSelectorsAreBoundedAndCannotCrossScope(t *testing.T) {
	valid := domain.Query{ProjectID: project, RepositoryID: repository, MR: 7, ExpectedHead: strings.Repeat("a", 40)}
	if got, err := domain.Normalize(valid); err != nil || got.Limit != 6 {
		t.Fatal(got, err)
	}
	for _, bad := range []domain.Query{{ProjectID: project}, {ProjectID: project, MR: 7}, {ProjectID: project, RepositoryID: repository, SessionID: "bad"}, {ProjectID: project, RepositoryID: repository, MR: 7, ExpectedHead: "short"}, {ProjectID: project, RepositoryID: repository, MR: 7, Limit: 13}, {ProjectID: project, RepositoryID: repository, CommitSHA: strings.Repeat("b", 40), ExpectedHead: strings.Repeat("a", 40)}} {
		if _, err := domain.Normalize(bad); err == nil {
			t.Fatal("accepted", bad)
		}
	}
}
func TestPipelineSuccessNeverAppliesToAnotherHeadOrProviderProject(t *testing.T) {
	mr := forge.MergeRequest{Head: strings.Repeat("a", 40), SourceProjectID: 42}
	p := forge.Pipeline{SHA: mr.Head, ProjectID: 42, Status: "success"}
	got := domain.PipelineView(p, mr, strings.Repeat("b", 40))
	if !got.MatchesObservedHead || got.MatchesExpectedHead {
		t.Fatal(got)
	}
	p.ProjectID = 43
	if domain.PipelineView(p, mr, mr.Head).MatchesObservedHead {
		t.Fatal("wrong project CI attached")
	}
	p.ProjectID = 42
	p.SHA = strings.Repeat("b", 40)
	if domain.PipelineView(p, mr, mr.Head).MatchesObservedHead {
		t.Fatal("historical success attached")
	}
}
