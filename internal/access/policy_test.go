package access_test

import (
	"testing"
	"workgraph.local/platform/internal/access"
)

func TestCaptureRolePolicyFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		role       string
		permission access.Permission
		allowed    bool
	}{
		{"reader", access.ReadCapture, true}, {"reader", access.WriteCapture, false},
		{"writer", access.ReadCapture, true}, {"writer", access.WriteCapture, true},
		{"admin", access.WriteCapture, true}, {"", access.ReadCapture, false},
		{"ADMIN", access.WriteCapture, false}, {"admin", access.Permission("invented"), false},
		{"writer", access.ManageIntegration, false}, {"reader", access.ReadIntegration, true}, {"admin", access.ManageIntegration, true},
		{"reader", access.ReadDirectory, true}, {"writer", access.ManageDirectory, false}, {"admin", access.ManageDirectory, true}, {"admin", access.ManagePlatform, false}, {"admin", access.ManageCredentials, false},
	} {
		if got := access.Allows(tc.role, tc.permission); got != tc.allowed {
			t.Errorf("role=%q permission=%q: got %v", tc.role, tc.permission, got)
		}
	}
}
