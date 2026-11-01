package postgres

import (
	"errors"
	"reflect"
	"testing"
)

type matchRow struct {
	base, url, provider string
	err                 error
}

func (r matchRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	values := []any{
		"11111111-1111-4111-8111-111111111111",
		"22222222-2222-4222-8222-222222222222",
		r.base, int64(42), int64(7), r.url,
		"33333333-3333-4333-8333-333333333333",
		"44444444-4444-4444-8444-444444444444",
		int64(1), "capture-digest", "commit-sha", "mr-digest", "code-key", r.provider,
	}
	for i, value := range values {
		reflect.ValueOf(dest[i]).Elem().Set(reflect.ValueOf(value))
	}
	return nil
}

func TestReadLinkMatchUsesProviderTitle(t *testing.T) {
	for _, test := range []struct{ name, base, url, title string }{
		{"github", "https://github.com/example/project", "https://github.com/example/project/pull/7", "GitHub PR #7"},
		{"enterprise", "https://github.example.com/code/team/project", "https://github.example.com/code/team/project/pull/7", "GitHub PR #7"},
		{"gitlab-prefix", "https://gitlab.example.com/code/team", "https://gitlab.example.com/code/team/group/project/-/merge_requests/7", "GitLab MR !7"},
		{"gitlab", "https://gitlab.example.com", "https://gitlab.example.com/group/project/-/merge_requests/7", "GitLab MR !7"},
	} {
		t.Run(test.name, func(t *testing.T) {
			match, err := readLinkMatch(matchRow{base: test.base, url: test.url, provider: map[string]string{"github": "github", "enterprise": "github"}[test.name]})
			if err != nil {
				t.Fatal(err)
			}
			if match.Link.Title != test.title || match.Link.Validate() != nil {
				t.Fatalf("invalid provider link: %+v", match.Link)
			}
			if match.Evidence.CaptureDigest != "capture-digest" || match.Evidence.MRCodeKey != "code-key" {
				t.Fatal("match evidence lost", match.Evidence)
			}
		})
	}
	if match, err := readLinkMatch(matchRow{base: "https://github.com/example/project", url: "https://github.com/other/project/pull/7"}); err != nil || match.Link.Validate() == nil || match.Link.RepositoryID == "" {
		t.Fatal("invalid row lost its identity or became deliverable", match, err)
	}
	sentinel := errors.New("scan failed")
	if _, err := readLinkMatch(matchRow{err: sentinel}); !errors.Is(err, sentinel) {
		t.Fatalf("scan error lost: %v", err)
	}
}
