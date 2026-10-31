package domain_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/identity/domain"
)

func cliInput() domain.CLIRequest {
	return domain.CLIRequest{ID: "11111111-1111-4111-8111-111111111111", DeviceHash: strings.Repeat("a", 64), ProofHash: strings.Repeat("b", 64), CredentialHash: strings.Repeat("c", 64), CodeHash: strings.Repeat("d", 64), Label: "Development laptop", Scopes: []access.Permission{access.WriteCapture, access.ManageCredentials}, TTLSeconds: 86400}
}

func TestCLIEnrollmentOwnsNormalizedLeastPrivilegeIntentAndBoundedLifetime(t *testing.T) {
	i := cliInput()
	i.Label = "  Development laptop  "
	i.Scopes = append(i.Scopes, access.WriteCapture)
	n, e := i.Normalize()
	if e != nil || n.Label != "Development laptop" || len(n.Scopes) != 2 {
		t.Fatal(n, e)
	}
	i.Scopes[0] = access.ManagePlatform
	if slices.Contains(n.Scopes, access.ManagePlatform) {
		t.Fatal("request aliases caller scopes")
	}
	for _, mutate := range []func(*domain.CLIRequest){
		func(v *domain.CLIRequest) {
			v.Scopes = []access.Permission{access.ManagePlatform, access.ManageCredentials}
		},
		func(v *domain.CLIRequest) {
			v.Scopes = []access.Permission{access.ManageDirectory, access.ManageCredentials}
		},
		func(v *domain.CLIRequest) { v.Scopes = []access.Permission{access.WriteCapture} },
		func(v *domain.CLIRequest) { v.TTLSeconds = 90*86400 + 1 }, func(v *domain.CLIRequest) { v.TTLSeconds = 59 },
		func(v *domain.CLIRequest) { v.Label = "\x00bad" }, func(v *domain.CLIRequest) { v.CredentialHash = "not a hash" },
		func(v *domain.CLIRequest) { v.ID = "not an id" }, func(v *domain.CLIRequest) { v.ProofHash = v.DeviceHash },
	} {
		v := cliInput()
		mutate(&v)
		if _, e := v.Normalize(); e == nil {
			t.Fatal("unsafe enrollment accepted", v.Label)
		}
	}
}

func TestCLIEnrollmentProofsArePurposeBoundAndPublicViewsContainNoProofOrKeyMaterial(t *testing.T) {
	secrets, e := domain.NewCLISecrets()
	if e != nil {
		t.Fatal(e)
	}
	if access.CredentialHash(secrets.Credential) == "" || domain.SecretHash(domain.CLIDevicePrefix, secrets.Device) == "" || domain.SecretHash(domain.CLIProofPrefix, secrets.Proof) == "" {
		t.Fatal("invalid entropy")
	}
	if secrets.Device == secrets.Proof || secrets.Credential == secrets.Proof {
		t.Fatal("shared entropy")
	}
	code, e := domain.NormalizeCLICode(strings.ToLower(secrets.Code))
	if e != nil || code != secrets.Code {
		t.Fatal(code, e)
	}
	if len(code) != 9 || code[4] != '-' {
		t.Fatal("invalid human code")
	}
	i := cliInput()
	i.DeviceHash = domain.SecretHash(domain.CLIDevicePrefix, secrets.Device)
	i.ProofHash = domain.SecretHash(domain.CLIProofPrefix, secrets.Proof)
	i.CredentialHash = access.CredentialHash(secrets.Credential)
	i.CodeHash = domain.CLICodeHash(code)
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	v := domain.CLIEnrollment{Request: i, Configuration: strings.Repeat("f", 64), CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute), Status: "pending"}
	if !v.Matches(secrets.Device, secrets.Proof) || v.Matches(secrets.Proof, secrets.Device) {
		t.Fatal("proof purpose confusion")
	}
	first := v.Fingerprint()
	v.Request.Label = "different"
	if first == v.Fingerprint() {
		t.Fatal("label not pinned")
	}
	v.Request = i
	v.CreatedAt = now.Add(time.Second)
	if first == v.Fingerprint() {
		t.Fatal("new flow not pinned")
	}
	raw, _ := json.Marshal(v.View())
	for _, private := range []string{secrets.Credential, secrets.Device, secrets.Proof, i.CredentialHash, i.DeviceHash, i.ProofHash, i.CodeHash, "credential_hash", "configuration"} {
		if strings.Contains(string(raw), private) {
			t.Fatal("private proof leaked in view")
		}
	}
	expiry := now.Add(time.Hour)
	v.CredentialExpiresAt = &expiry
	owned := v.View()
	*owned.CredentialExpiresAt = now
	owned.Scopes[0] = access.ManagePlatform
	if !v.CredentialExpiresAt.Equal(expiry) || v.Request.Scopes[0] == access.ManagePlatform || expiry.Equal(now) {
		t.Fatal("public metadata aliases private enrollment state")
	}
	for _, bad := range []string{"", "0000-0000", "ABCD-EFGHI", "ABCD/EFGH", "ABCD\nEFGH"} {
		if _, e := domain.NormalizeCLICode(bad); e == nil {
			t.Fatal("malformed code accepted")
		}
	}
}
