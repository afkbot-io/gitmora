// Package secretbox provides versioned authenticated encryption for recoverable provider secrets.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"regexp"
)

// ErrInvalid never includes secret material or cryptographic implementation details.
var ErrInvalid = errors.New("invalid encrypted secret or encryption configuration")

// Box retains an active encryption key and historical decryption keys for rotation.
type Box struct {
	active string
	keys   map[string]cipher.AEAD
}

var keyID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// New requires independent 256-bit keys and takes ownership of constructed cipher state.
func New(active string, keys map[string][]byte) (*Box, error) {
	if !keyID.MatchString(active) || len(keys) == 0 || len(keys) > 32 {
		return nil, ErrInvalid
	}
	box := &Box{active: active, keys: map[string]cipher.AEAD{}}
	for id, key := range keys {
		if !keyID.MatchString(id) || len(key) != 32 {
			return nil, ErrInvalid
		}
		block, err := aes.NewCipher(key)
		if err != nil {
			return nil, ErrInvalid
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return nil, ErrInvalid
		}
		box.keys[id] = aead
	}
	if box.keys[active] == nil {
		return nil, ErrInvalid
	}
	return box, nil
}

// Seal uses a fresh nonce and binds the ciphertext to an explicit immutable resource scope.
func (b *Box) Seal(scope string, plaintext []byte) ([]byte, error) {
	if scope == "" || len(scope) > 256 || len(plaintext) == 0 || len(plaintext) > 16384 {
		return nil, ErrInvalid
	}
	aead := b.keys[b.active]
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, ErrInvalid
	}
	envelope := append([]byte{1, byte(len(b.active))}, []byte(b.active)...)
	envelope = append(envelope, nonce...)
	aad := append([]byte(scope+"\x00"), envelope[:2+len(b.active)]...)
	return aead.Seal(envelope, nonce, plaintext, aad), nil
}

// Open rejects modified envelopes, missing historical keys and cross-resource substitution.
func (b *Box) Open(scope string, envelope []byte) ([]byte, error) {
	if scope == "" || len(scope) > 256 || len(envelope) < 2 || len(envelope) > 16640 || envelope[0] != 1 {
		return nil, ErrInvalid
	}
	n := int(envelope[1])
	if n < 1 || n > 64 || len(envelope) < 2+n {
		return nil, ErrInvalid
	}
	aead := b.keys[string(envelope[2:2+n])]
	if aead == nil || len(envelope) < 2+n+aead.NonceSize()+aead.Overhead() {
		return nil, ErrInvalid
	}
	nonce := envelope[2+n : 2+n+aead.NonceSize()]
	aad := append([]byte(scope+"\x00"), envelope[:2+n]...)
	plaintext, err := aead.Open(nil, nonce, envelope[2+n+aead.NonceSize():], aad)
	if err != nil {
		return nil, ErrInvalid
	}
	return plaintext, nil
}
