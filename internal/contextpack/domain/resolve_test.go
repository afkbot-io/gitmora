package domain_test

import (
	"testing"
	"workgraph.local/platform/internal/contextpack/domain"
)

func TestResolveNormalizesKnownMRPathsWithoutAcceptingAmbiguousURLs(t *testing.T) {
	p := "11111111-1111-4111-8111-111111111111"
	for _, v := range []struct{ in, out string }{
		{" https://CODE.example.com:8443/gitlab/team/repo/-/merge_requests/7/?tab=commits#changes ", "https://code.example.com:8443/gitlab/team/repo/-/merge_requests/7"},
		{"https://github.example.com/prefix/team/repo/pull/8", "https://github.example.com/prefix/team/repo/pull/8"},
	} {
		q, e := domain.NormalizeResolve(domain.ResolveQuery{ProjectID: p, URL: v.in})
		if e != nil || q.URL != v.out {
			t.Fatal(q, e)
		}
	}
	for _, v := range []string{"https://user:key@github.com/a/b/pull/7", "https://github.com/a/b/pull/0", "https://github.com/a/b/pull/07", "https://github.com/a/b/pull/7/files", "https://github.com/a%2fb/pull/7", "file:///a/pull/7", "https://github.com/a/b/pull/7\nHeader:value"} {
		if _, e := domain.NormalizeResolve(domain.ResolveQuery{ProjectID: p, URL: v}); e == nil {
			t.Fatal("accepted ambiguous URL", v)
		}
	}
}
