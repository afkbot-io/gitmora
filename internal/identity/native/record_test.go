package native_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/identity/native"
)

func TestNativePendingCredentialIsServerBoundAndOnlyExplicitSecretStoreEncodingContainsSecrets(t *testing.T) {
	r, e := native.NewPendingRecord("https://WORKGRAPH.internal:443/", "Laptop", []access.Permission{access.WriteCapture, access.ManageCredentials}, 86400)
	if e != nil {
		t.Fatal(e)
	}
	if r.Server != "https://workgraph.internal" || r.Pending == nil || r.Account != nil {
		t.Fatal("invalid initial state")
	}
	public, e := json.Marshal(r)
	if e != nil {
		t.Fatal(e)
	}
	for _, secret := range []string{r.Secret, r.Pending.Device, r.Pending.Proof, r.Pending.Request.CredentialHash, r.Pending.Request.DeviceHash} {
		if bytes.Contains(public, []byte(secret)) {
			t.Fatal("public metadata exposed key/proof")
		}
	}
	privateDiagnostics, _ := json.Marshal(r.Pending)
	for _, value := range []string{r.Pending.Device, r.Pending.Proof, r.Pending.Request.CredentialHash} {
		if bytes.Contains(privateDiagnostics, []byte(value)) || strings.Contains(fmt.Sprintf("%+v %#v", r.Pending, r.Pending), value) {
			t.Fatal("pending diagnostic serialization exposed a proof")
		}
	}
	encoded, e := native.EncodeSecretStoreRecord(r)
	if e != nil || !bytes.Contains(encoded, []byte(r.Secret)) {
		t.Fatal("private encoder did not preserve candidate")
	}
	back, e := native.DecodeSecretStoreRecord(r.Server, encoded)
	if e != nil || back.Secret != r.Secret || back.Pending.Proof != r.Pending.Proof || back.Pending.Request.ID != r.Pending.Request.ID {
		t.Fatal("secret-store round trip failed", e)
	}
	if _, e = native.DecodeSecretStoreRecord("https://another.internal", encoded); e == nil {
		t.Fatal("credential retargeted to another server")
	}
	bad := bytes.Replace(encoded, []byte(r.Pending.Request.CredentialHash), []byte(strings.Repeat("e", 64)), 1)
	if _, e = native.DecodeSecretStoreRecord(r.Server, bad); e == nil || strings.Contains(e.Error(), r.Secret) {
		t.Fatal("altered intent accepted or leaked")
	}
}

func TestNativeServerIdentityRejectsCredentialBearingOrAmbiguousDestinations(t *testing.T) {
	for _, s := range []string{"http://workgraph.internal", "https://token@workgraph.internal", "https://workgraph.internal?token=secret", "https://workgraph.internal/#token", "https://workgraph.internal/a/../b", "//workgraph.internal"} {
		if _, e := native.CanonicalServer(s); e == nil {
			t.Fatal("invalid destination accepted")
		}
	}
	for _, s := range []string{"https://workgraph.internal", "http://127.0.0.1:1234", "http://[::1]:1234", "https://workgraph.internal/prefix"} {
		if _, e := native.CanonicalServer(s); e != nil {
			t.Fatal(e)
		}
	}
}

func TestNativeCoreUpgradeReadsPriorAccountWithoutRetiredCapabilities(t *testing.T) {
	r, e := native.NewPendingRecord("https://gitmora.internal", "Laptop", []access.Permission{access.ReadCapture, access.ManageCredentials}, 86400)
	if e != nil {
		t.Fatal(e)
	}
	r.Pending = nil
	r.Account = &native.Account{UserID: "11111111-1111-4111-8111-111111111111", TokenID: "22222222-2222-4222-8222-222222222222", DisplayName: "Engineer", Scopes: []access.Permission{access.ReadCapture}, ExpiresAt: time.Now().Add(time.Hour)}
	raw, e := native.EncodeSecretStoreRecord(r)
	if e != nil {
		t.Fatal(e)
	}
	legacy := bytes.Replace(raw, []byte(`"capture:read"`), []byte(`"capture:read","knowledge:read","review:run"`), 1)
	migrated, e := native.DecodeSecretStoreRecord(r.Server, legacy)
	if e != nil || migrated.Secret != r.Secret || migrated.Account == nil || !slices.Equal(migrated.Account.Scopes, []access.Permission{access.ReadCapture}) {
		t.Fatal("saved collector root lost", e)
	}
	unknown := bytes.Replace(raw, []byte(`"capture:read"`), []byte(`"capture:read","unknown:manage"`), 1)
	if _, e = native.DecodeSecretStoreRecord(r.Server, unknown); e == nil {
		t.Fatal("unknown capability accepted")
	}
}
