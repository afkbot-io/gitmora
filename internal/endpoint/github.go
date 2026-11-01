package endpoint

import (
	"net/url"
	"regexp"
	"strings"
)

var githubOwner = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,37}[a-zA-Z0-9])?$`)
var githubName = regexp.MustCompile(`^[a-zA-Z0-9_.-]{1,100}$`)

// GitHubRepository pins a GitHub.com web repository, never an arbitrary API destination.
func GitHubRepository(value string) (string, bool) {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.Opaque != "" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", false
	}
	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(parts) != 2 || !githubOwner.MatchString(parts[0]) {
		return "", false
	}
	parts[1] = strings.TrimSuffix(parts[1], ".git")
	if !githubName.MatchString(parts[1]) || parts[1] == "." || parts[1] == ".." {
		return "", false
	}
	return "https://github.com/" + strings.ToLower(strings.Join(parts, "/")), true
}
