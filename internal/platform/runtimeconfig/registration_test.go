package runtimeconfig_test

import (
	"testing"
	"workgraph.local/platform/internal/platform/runtimeconfig"
)

func TestRegistrationIsExplicitAndFailClosed(t *testing.T) {
	for _, tc := range []struct {
		value            string
		enabled, invalid bool
	}{{"", false, false}, {"false", false, false}, {"true", true, false}, {"1", false, true}, {"TRUE", false, true}, {" true ", false, true}} {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv("WORKGRAPH_REGISTRATION_ENABLED", tc.value)
			enabled, e := runtimeconfig.RegistrationEnabled()
			if enabled != tc.enabled || (e != nil) != tc.invalid {
				t.Fatal("unexpected registration policy", enabled, e)
			}
		})
	}
}
