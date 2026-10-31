// Package gitlocal defines the process policy for local metadata and immutable source acquisition.
package gitlocal

import (
	"os"
	"strings"
)

// Environment removes inherited Git routing/configuration and denies every transport, including
// file/ext/custom helpers. GIT_NO_LAZY_FETCH additionally avoids fetch attempts on newer Git; the
// protocol denial also protects Git versions that do not implement that variable or CLI flag.
func Environment() []string {
	result := make([]string, 0, len(os.Environ())+9)
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, "GIT_") {
			result = append(result, value)
		}
	}
	return append(result,
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_NO_REPLACE_OBJECTS=1",
		"GIT_NO_LAZY_FETCH=1", "GIT_ALLOW_PROTOCOL=", "GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0", "GIT_LITERAL_PATHSPECS=1")
}
