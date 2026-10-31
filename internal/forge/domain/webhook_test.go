package domain_test

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"strings"
	"testing"
	"time"
	"workgraph.local/platform/internal/forge/domain"
)

func TestSignedWebhookChecksBodyIdentityTimeAndConfiguredMode(t *testing.T) {
	now := time.Unix(1793300000, 0)
	key := strings.Repeat("a", 32)
	secrets := domain.Secrets{APIToken: "synthetic-api-token", SigningToken: "whsec_" + base64.StdEncoding.EncodeToString([]byte(key))}
	delivery := domain.Delivery{ID: "delivery-1", Timestamp: strconv.FormatInt(now.Unix(), 10), Body: []byte(`{"object_kind":"push","project_id":42,"after":"` + strings.Repeat("b", 40) + `"}`), Event: "Push Hook"}
	mac := hmac.New(sha256.New, []byte(key))
	_, _ = mac.Write([]byte(delivery.ID + "." + delivery.Timestamp + "."))
	_, _ = mac.Write(delivery.Body)
	delivery.Signature = "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if err := domain.AuthenticateDelivery(secrets, delivery, now); err != nil {
		t.Fatal(err)
	}
	valid := delivery.Signature
	delivery.Signature = "v0,ignored " + valid + " v1,invalid"
	if err := domain.AuthenticateDelivery(secrets, delivery, now); err != nil {
		t.Fatal("valid rotated signature was not accepted")
	}
	delivery.Signature = valid
	event, err := domain.ParseEvent(delivery, 42)
	if err != nil || event.After != strings.Repeat("b", 40) {
		t.Fatal("valid event rejected")
	}
	if _, err := domain.ParseEvent(delivery, 43); err == nil {
		t.Fatal("cross-project event accepted")
	}
	if err := domain.AuthenticateDelivery(secrets, delivery, now.Add(6*time.Minute)); err == nil {
		t.Fatal("stale signature accepted")
	}
	delivery.LegacyToken = "synthetic-legacy-token"
	delivery.Signature = ""
	if err := domain.AuthenticateDelivery(secrets, delivery, now); err == nil {
		t.Fatal("signature mode downgraded to legacy")
	}
}

func TestLegacyAuthenticationRequiresExplicitLegacyModeAndConstantTokenIdentity(t *testing.T) {
	secrets := domain.Secrets{APIToken: "synthetic-provider-token", LegacyToken: "synthetic-legacy-token"}
	delivery := domain.Delivery{ID: "legacy-delivery", LegacyToken: secrets.LegacyToken, Body: []byte(`{"object_kind":"push","project_id":42}`)}
	if err := domain.AuthenticateDelivery(secrets, delivery, time.Now()); err != nil {
		t.Fatal(err)
	}
	delivery.LegacyToken = "wrong"
	if err := domain.AuthenticateDelivery(secrets, delivery, time.Now()); err == nil {
		t.Fatal("wrong legacy token accepted")
	}
}
