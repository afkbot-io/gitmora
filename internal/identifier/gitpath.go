package identifier

import (
	"path"
	"strings"
	"unicode/utf8"
)

// ValidGitPath accepts a bounded canonical repository-relative path, never a local filesystem destination.
func ValidGitPath(value string) bool {
	return len(value) > 0 && len(value) <= 4096 && utf8.ValidString(value) && !strings.ContainsAny(value, "\\\x00\r\n") && !path.IsAbs(value) && path.Clean(value) == value && value != "." && value != ".." && !strings.HasPrefix(value, "../") && value != ".git" && !strings.HasPrefix(value, ".git/")
}
