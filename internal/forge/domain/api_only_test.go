package domain_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"workgraph.local/platform/internal/forge/domain"
)

func TestAPIOnlyRequiresExplicitDisabledWebhooksAndNeverAuthenticatesDelivery(t *testing.T) {
	var secrets domain.Secrets
	if err := json.Unmarshal([]byte(`{"api_token":"synthetic-api-token","webhook_mode":"disabled"}`), &secrets); err != nil {
		t.Fatal(err)
	}
	if err := secrets.Validate(); err != nil || secrets.Mode() != "disabled" {
		t.Fatal("explicit API-only connection rejected", err)
	}
	for _, delivery := range []domain.Delivery{
		{ID: "empty-auth", Body: []byte(`{"object_kind":"push","project_id":42}`)},
		{ID: "api-as-hook", LegacyToken: secrets.APIToken, Body: []byte(`{"object_kind":"push","project_id":42}`)},
		{ID: "old-signature", Timestamp: "1793300000", Signature: "v1,stale", LegacyToken: "synthetic-old-hook", Body: []byte(`{"object_kind":"push","project_id":42}`)},
	} {
		if err := domain.AuthenticateDelivery(secrets, delivery, time.Unix(1793300000, 0)); !errors.Is(err, domain.ErrUnauthenticated) {
			t.Fatal("API-only configuration authenticated a webhook", err)
		}
	}
}

func TestWebhookModeRejectsAccidentalDisableAmbiguityAndUnknownValues(t *testing.T) {
	for _, raw := range []string{
		`{"api_token":"synthetic-api-token"}`,
		`{"api_token":"synthetic-api-token","webhook_mode":"disabled","legacy_webhook_token":"synthetic-old-hook"}`,
		`{"api_token":"synthetic-api-token","webhook_mode":"disabled","webhook_signing_token":"whsec_bad"}`,
		`{"api_token":"synthetic-api-token","webhook_mode":"unknown","legacy_webhook_token":"synthetic-old-hook"}`,
		`{"api_token":"synthetic-api-token","webhook_mode":"signature","legacy_webhook_token":"synthetic-old-hook"}`,
		`{"api_token":"bad token","webhook_mode":"disabled"}`,
	} {
		var secrets domain.Secrets
		if err := json.Unmarshal([]byte(raw), &secrets); err != nil {
			t.Fatal(err)
		}
		if !errors.Is(secrets.Validate(), domain.ErrInvalid) {
			t.Fatal("ambiguous, weak or unknown connection accepted")
		}
	}
}
