package secretbox_test

import (
	"bytes"
	"testing"
	"workgraph.local/platform/internal/platform/secretbox"
)

func TestEnvelopeBindsCiphertextToItsRepositoryAndSupportsKeyRotation(t *testing.T) {
	keys := map[string][]byte{"old": bytes.Repeat([]byte{1}, 32), "current": bytes.Repeat([]byte{2}, 32)}
	old, err := secretbox.New("old", keys)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := old.Seal("gitlab:repository-a", []byte("never-store-cleartext"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := old.Seal("gitlab:repository-a", []byte("never-store-cleartext"))
	if err != nil || bytes.Equal(sealed, second) || bytes.Contains(sealed, []byte("never-store-cleartext")) {
		t.Fatal("nonce reuse or plaintext storage")
	}
	rotated, err := secretbox.New("current", keys)
	if err != nil {
		t.Fatal(err)
	}
	clear, err := rotated.Open("gitlab:repository-a", sealed)
	if err != nil || string(clear) != "never-store-cleartext" {
		t.Fatal("old encrypted data lost after rotation")
	}
	if _, err := rotated.Open("gitlab:repository-b", sealed); err == nil {
		t.Fatal("ciphertext moved across repositories")
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := rotated.Open("gitlab:repository-a", sealed); err == nil {
		t.Fatal("tampered ciphertext accepted")
	}
}
