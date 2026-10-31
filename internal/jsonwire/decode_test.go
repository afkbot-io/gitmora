package jsonwire_test

import (
	"encoding/json"
	"strings"
	"testing"
	"workgraph.local/platform/internal/jsonwire"
)

func TestExternalProviderKeysDoNotWeakenTheCanonicalAPIContract(t *testing.T) {
	raw := []byte(`{"id":42,"metadata":{"UPPER_CASE_PROVIDER_KEY":true}}`)
	var value map[string]json.RawMessage
	if jsonwire.DecodeObject(raw, &value) == nil {
		t.Fatal("canonical API accepted noncanonical keys")
	}
	if err := jsonwire.DecodeExternalObject(raw, &value); err != nil {
		t.Fatal("valid provider metadata was rejected")
	}
	for _, invalid := range []string{`{"id":1,"id":2}`, `{"meta":{"key":1,"key":2}}`, `{} {}`, strings.Repeat(`{"child":`, 34) + `{}` + strings.Repeat("}", 34)} {
		if jsonwire.DecodeExternalObject([]byte(invalid), &value) == nil {
			t.Fatal("ambiguous or excessive provider JSON accepted")
		}
	}
}
