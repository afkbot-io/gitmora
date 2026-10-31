package identifier

import (
	"regexp"
	"strings"
)

var gitObjectPattern = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)

// ValidCommit reports whether sha is a full nonzero SHA-1 or SHA-256 object identifier.
// Syntax does not establish provider existence, reachability or authorship.
func ValidCommit(sha string) bool {
	return gitObjectPattern.MatchString(sha) && strings.Trim(sha, "0") != ""
}
