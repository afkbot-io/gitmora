package domain_test

import (
	"errors"
	"strings"
	"testing"

	"workgraph.local/platform/internal/distribution/domain"
)

func TestExactInstallReplacementAndReplay(t *testing.T) {
	a, b := strings.Repeat("a", 64), strings.Repeat("b", 64)
	for _, tc := range []struct {
		current, incoming, expected string
		change                      bool
		err                         error
	}{
		{"", a, "", true, nil}, {a, a, "", false, nil}, {a, b, "", false, domain.ErrConflict},
		{a, b, b, false, domain.ErrConflict}, {a, b, a, true, nil}, {b, b, a, false, nil},
		{"", a, b, false, domain.ErrConflict}, {a, "bad", "", false, domain.ErrInvalid},
		{a, b, "bad", false, domain.ErrInvalid},
	} {
		change, err := domain.Replacement(tc.current, tc.incoming, tc.expected)
		if change != tc.change || !errors.Is(err, tc.err) {
			t.Fatalf("%+v: %v %v", tc, change, err)
		}
	}
}
