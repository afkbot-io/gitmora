package domain_test

import (
	"testing"

	"workgraph.local/platform/internal/forge/domain"
)

func TestMRLinkAcceptsOnlyItsProviderOriginPathAndExactIID(t *testing.T) {
	repo := "11111111-1111-4111-8111-111111111111"
	link, err := domain.NewMRLink(repo, "https://gitlab.internal/gitlab", 42, 7, "https://gitlab.internal/gitlab/team/project/-/merge_requests/7")
	if err != nil || link.Validate() != nil || link.Title != "GitLab MR !7" {
		t.Fatal(link, err)
	}
	for _, target := range []string{"https://other.internal/gitlab/p/-/merge_requests/7", "https://gitlab.internal/p/-/merge_requests/7", "https://gitlab.internal/gitlab/p/-/merge_requests/8", "https://gitlab.internal/gitlab/p/-/merge_requests/7?token=private", "https://user:password@gitlab.internal/gitlab/p/-/merge_requests/7", "https://gitlab.internal/gitlab/p/-/merge_requests/7#private"} {
		if _, err := domain.NewMRLink(repo, link.BaseURL, 42, 7, target); err == nil {
			t.Fatal("unsafe MR link accepted", target)
		}
	}
}

func TestGitHubPullRequestLinkKeepsExactRepositoryAndNumber(t *testing.T) {
	repo := "11111111-1111-4111-8111-111111111111"
	base := "https://github.com/example/project"
	link, err := domain.NewMRLink(repo, base, 42, 7, base+"/pull/7")
	if err != nil || link.Validate() != nil || link.Title != "GitHub PR #7" {
		t.Fatal(link, err)
	}
	for _, target := range []string{base + "/pull/8", base + "/pull/7?token=secret", base + "/pull/7#comment", "https://github.com/other/project/pull/7", base + "/-/merge_requests/7"} {
		if _, err := domain.NewMRLink(repo, base, 42, 7, target); err == nil {
			t.Fatal("foreign or ambiguous PR link accepted", target)
		}
	}
}
