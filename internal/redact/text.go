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

// Conversation-only patterns never change Text/Lines canonical policies used to verify old snapshots.
var conversationAssignment = regexp.MustCompile(`(?i)((?:\bpassword|\bpasswd|пароль)\*{0,2}[ \t]*[:=]\*{0,2}[ \t]*)(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s,;}]+)`)
var conversationPassword = regexp.MustCompile(`(?i)((?:пароль[ \t]+(?:(?:пока|временно)[ \t]+)?(?:сделай|установи|поставь|задай|будет)|set[ \t]+(?:the[ \t]+)?password|password[ \t]+is)[ \t]+)(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s,;}]+)`)

// Conversation masks explicit natural-language and Markdown credential declarations before native windowing.
// It deliberately leaves the historical canonical Text/Lines projection unchanged.
func Conversation(input string) string {
	input = Text(input)
	lower := strings.ToLower(input)
	if !strings.Contains(lower, "пароль") && !strings.Contains(lower, "password") && !strings.Contains(lower, "passwd") {
		return input
	}
	changed := false
	for pass := 0; pass < 2; pass++ {
		previous := input
		input = conversationPassword.ReplaceAllString(input, "${1}[REDACTED]")
		input = conversationAssignment.ReplaceAllString(input, "${1}[REDACTED]")
		if input == previous {
			break
		}
		changed = true
	}
	if changed {
		return Text(input)
	}
	return input
}

// Text deterministically redacts recognized secrets and preserves already redacted values.
func Text(input string) string {
	input = privateKey.ReplaceAllString(input, "[REDACTED]")
	input = bearer.ReplaceAllString(input, "${1}[REDACTED]")
	input = providerToken.ReplaceAllString(input, "[REDACTED]")
	return assignments(input)
}

// Lines removes known secrets while preserving newline counts for code citations.
// Retained rune offsets identify the sanitized projection, while original line numbers stay stable.
func Lines(input string) string {
	input = privateKey.ReplaceAllStringFunc(input, func(match string) string { return "[REDACTED]" + strings.Repeat("\n", strings.Count(match, "\n")) })
	input = bearer.ReplaceAllString(input, "${1}[REDACTED]")
	input = providerToken.ReplaceAllString(input, "[REDACTED]")
	return assignments(input)
}

// Removing a quoted value can join its placeholder to suffix punctuation or
// another assignment. Canonicalize that span before returning a retained value.
// The second pass leaves each placeholder followed by a value delimiter, so
// validating an already sanitized projection does not change it again.
func assignments(input string) string {
	input = assignment.ReplaceAllString(input, "${1}[REDACTED]")
	return assignment.ReplaceAllString(input, "${1}[REDACTED]")
}
