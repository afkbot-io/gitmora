// Package redact removes common credential forms before evidence is persisted.
// Pattern matching is best effort and does not establish that arbitrary text is secret-free.
package redact

import (
	"regexp"
	"strings"
)

var privateKey = regexp.MustCompile(`(?s)-----BEGIN (?:[A-Z]+ )?PRIVATE KEY-----.*?-----END (?:[A-Z]+ )?PRIVATE KEY-----`)
var bearer = regexp.MustCompile(`(?i)(\bBearer\s+)[a-z0-9._~+/=-]+`)

// URL-safe tokens can end in '-'; a trailing word boundary would skip a valid credential.
var providerToken = regexp.MustCompile(`\b(?:wg_[a-zA-Z0-9_-]{43}|ttp_mcp_[a-zA-Z0-9_-]{20,}|glpat-[a-zA-Z0-9_-]{12,}|gh[pousr]_[a-zA-Z0-9]{20,}|github_pat_[a-zA-Z0-9_]{20,}|sk-[a-zA-Z0-9_-]{20,})`)
var assignment = regexp.MustCompile(`(?i)(\b(?:[a-z0-9]+[_-])*(?:password|passwd|secret|token|api[_-]?key|access[_-]?key)["']?\s*[:=]\s*)(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s,;}]+)`)

// Text deterministically redacts recognized secrets and preserves already redacted values.
func Text(input string) string {
	input = privateKey.ReplaceAllString(input, "[REDACTED]")
	input = bearer.ReplaceAllString(input, "${1}[REDACTED]")
	input = providerToken.ReplaceAllString(input, "[REDACTED]")
	return assignment.ReplaceAllString(input, "${1}[REDACTED]")
}

// Lines removes known secrets while preserving newline counts for code citations.
// Retained rune offsets identify the sanitized projection, while original line numbers stay stable.
func Lines(input string) string {
	input = privateKey.ReplaceAllStringFunc(input, func(match string) string { return "[REDACTED]" + strings.Repeat("\n", strings.Count(match, "\n")) })
	input = bearer.ReplaceAllString(input, "${1}[REDACTED]")
	input = providerToken.ReplaceAllString(input, "[REDACTED]")
	return assignment.ReplaceAllString(input, "${1}[REDACTED]")
}
