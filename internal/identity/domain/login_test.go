package domain_test

import (
	"encoding/json"
	"strings"
	"testing"

	"workgraph.local/platform/internal/identity/domain"
)

func TestLoginProofBindsBrowserAndStateWithoutStoringVerifier(t *testing.T) {
	state, e := domain.NewSecret(domain.StatePrefix)
	if e != nil {
		t.Fatal(e)
	}
	browser, e := domain.NewSecret(domain.FlowPrefix)
	if e != nil {
		t.Fatal(e)
	}
	verifier, nonce, e := domain.Proof(state, browser)
	if e != nil || len(verifier) != 43 || len(nonce) != 43 || verifier == nonce {
		t.Fatal("invalid proof derivation", e)
	}
	other, _ := domain.NewSecret(domain.FlowPrefix)
	different, _, _ := domain.Proof(state, other)
	if different == verifier {
		t.Fatal("another browser reused verifier")
	}
	if _, _, e = domain.Proof(state, "malformed"); e == nil {
		t.Fatal("malformed browser accepted")
	}
	if domain.SecretHash(domain.SessionPrefix, browser) != "" {
		t.Fatal("flow cookie accepted as a session")
	}
	if strings.Contains(domain.SecretHash(domain.FlowPrefix, browser), browser) {
		t.Fatal("raw cookie retained")
	}
}

func TestLoginSecretsAndInternalPrincipalAreNotJSONFields(t *testing.T) {
	login := domain.Login{Cookie: "must-not-be-in-json", CSRF: "csrf-must-not-be-in-json", Session: domain.Session{CSRFHash: "internal-csrf-hash"}}
	raw, e := json.Marshal(login)
	if e != nil || strings.Contains(string(raw), "must-not") || strings.Contains(string(raw), "internal-csrf-hash") {
		t.Fatal("login material exposed through JSON", e)
	}
}

func TestConfigurationHasFixedCallbackOriginAndExactIssuer(t *testing.T) {
	valid := domain.Config{Issuer: "https://idp.internal/realms/workgraph", ClientID: "workgraph", PublicURL: "https://workgraph.internal", AuthStyle: "basic", SessionSeconds: 28800, Policy: domain.Policy}
	if _, e := domain.NormalizeConfig(valid); e != nil {
		t.Fatal(e)
	}
	for _, change := range []func(*domain.Config){func(c *domain.Config) { c.PublicURL = "https://workgraph.internal/path" }, func(c *domain.Config) { c.Issuer = "http://192.168.1.2" }, func(c *domain.Config) { c.ClientID = "" }, func(c *domain.Config) { c.SessionSeconds = 999999 }, func(c *domain.Config) { c.AuthStyle = "auto" }} {
		bad := valid
		change(&bad)
		if _, e := domain.NormalizeConfig(bad); e == nil {
			t.Fatal("unsafe config accepted", bad)
		}
	}
	loop := valid
	loop.Issuer = "http://127.0.0.1:1234/issuer"
	loop.PublicURL = "http://127.0.0.1:8080"
	normalized, e := domain.NormalizeConfig(loop)
	if e != nil || normalized.Secure() {
		t.Fatal(normalized, e)
	}
}

func TestRegistrationToggleDoesNotChangeExistingLoginAuthority(t *testing.T) {
	c := domain.Config{Issuer: "https://idp.internal", ClientID: "gitmora", PublicURL: "https://gitmora.internal", AuthStyle: "basic", SessionSeconds: 28800, Policy: domain.Policy}
	previous := c.Fingerprint()
	c.RegistrationEnabled = true
	if c.Fingerprint() != previous {
		t.Fatal("signup toggle invalidates all existing browser and CLI credentials")
	}
}
