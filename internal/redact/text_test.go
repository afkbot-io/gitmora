package redact_test

import (
	"strings"
	"testing"
	"workgraph.local/platform/internal/redact"
)

func TestKnownSecretFormsAreRemovedAndIdempotent(t *testing.T) {
	for _, input := range []string{
		`DB_PASSWORD=research-secret`, `{"api_key": "private-value"}`, `token: 'quoted private value'`,
		`Authorization: Bearer abc.def-123`, "glpat-" + strings.Repeat("a", 20),
		"wg_" + strings.Repeat("A", 43), "ttp_mcp_" + strings.Repeat("b", 32),
		"-----BEGIN PRIVATE KEY-----\nprivate-material\n-----END PRIVATE KEY-----",
	} {
		got := redact.Text(input)
		if got == input || !strings.Contains(got, "[REDACTED]") {
			t.Errorf("secret form was not redacted: %q", input)
		}
		if redact.Text(got) != got {
			t.Errorf("redaction is not idempotent: %q", got)
		}
	}
}

func TestOrdinaryEvidencePreserved(t *testing.T) {
	input := "The queue retries three times. token_count=42; src/token.go changed."
	if redact.Text(input) != input {
		t.Fatal("non-secret evidence changed")
	}
}

func TestQuotedAssignmentSuffixesRemainStable(t *testing.T) {
	for _, input := range []string{
		`connect(password="synthetic-value")`,
		`connect(token="synthetic-value").call()`,
		`token="synthetic-value"secret="another synthetic value"`,
		"connect(password='synthetic-value')\nnext()",
	} {
		for _, project := range []func(string) string{redact.Text, redact.Lines} {
			got := project(input)
			if strings.Contains(got, "synthetic") || project(got) != got {
				t.Fatalf("quoted assignment was not stably redacted: %q", got)
			}
		}
		if strings.Count(redact.Lines(input), "\n") != strings.Count(input, "\n") {
			t.Fatal("code citation line numbers changed")
		}
	}
}

func FuzzSecretProjectionIdempotence(f *testing.F) {
	for _, seed := range []string{
		`connect(password="synthetic-value")`,
		`token="one"secret="two words"`,
		`{"api_key":"synthetic-value"}`,
		"Authorization: Bearer synthetic-value\nnext()",
		"-----BEGIN PRIVATE KEY-----\nsynthetic\n-----END PRIVATE KEY-----",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		for _, project := range []func(string) string{redact.Text, redact.Lines} {
			got := project(input)
			if project(got) != got {
				t.Fatal("sanitized projection changed on validation")
			}
		}
	})
}

func TestProviderTokenSuffixesAreCompletelyRemoved(t *testing.T) {
	for _, token := range []string{
		"wg_" + strings.Repeat("A", 42) + "-",
		"ttp_mcp_" + strings.Repeat("b", 31) + "-",
		"glpat-" + strings.Repeat("c", 19) + "-",
		"sk-" + strings.Repeat("d", 23) + "-",
	} {
		for _, suffix := range []string{"", " ", "\n", `",`, "/path"} {
			input := "Observed credential: " + token + suffix
			want := "Observed credential: [REDACTED]" + suffix
			for _, project := range []func(string) string{redact.Text, redact.Lines} {
				if got := project(input); got != want {
					t.Errorf("non-word token suffix was retained (token length %d)", len(token))
				}
			}
		}
	}
}
