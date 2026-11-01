package endpoint

import (
	"net/url"
	"regexp"
	"strings"
)

var githubOwner = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,37}[a-zA-Z0-9])?$`)
var githubName = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,100}$`)

// GitHubRepository normalizes a web identity; an operator allowlist separately selects API destinations.
func GitHubRepository(value string) (string, bool) {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Opaque != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", false
	}
	if strings.EqualFold(u.Hostname(), "api.github.com") || strings.EqualFold(u.Hostname(), "github.com") && (u.Host != "github.com" || strings.Count(u.Path, "/") != 2) {
		return "", false
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(parts) < 2 || !githubOwner.MatchString(parts[len(parts)-2]) {
		return "", false
	}
	parts[len(parts)-1] = strings.TrimSuffix(parts[len(parts)-1], ".git")
	if !githubName.MatchString(parts[len(parts)-1]) || parts[len(parts)-1] == "." || parts[len(parts)-1] == ".." {
		return "", false
	}
	base, ok := GitLabBase(u.Scheme + "://" + u.Host + "/" + strings.Join(parts[:len(parts)-2], "/"))
	if !ok {
		return "", false
	}
	return base + "/" + strings.ToLower(strings.Join(parts[len(parts)-2:], "/")), true
}
