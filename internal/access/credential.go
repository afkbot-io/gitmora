package access

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"workgraph.local/platform/internal/identifier"
)

// ErrUnauthenticated identifies a malformed, expired, revoked or unknown credential.
var ErrUnauthenticated = errors.New("authentication required")

// Principal is the authenticated credential identity; project roles are checked separately.
type Principal struct {
	UserID  string
	TokenID string
	Scopes  []Permission
}

// Has reports whether this credential explicitly permits a supported capability.
func (p Principal) Has(permission Permission) bool {
	if !SupportedPermission(permission) {
		return false
	}
	return slices.Contains(p.Scopes, permission)
}

// Authenticator resolves a raw bearer credential without persisting or logging it.
type Authenticator interface {
	Authenticate(context.Context, string) (Principal, error)
}

// CredentialRecord contains only the non-recoverable digest of a bearer secret.
type CredentialRecord struct {
	ID        string
	UserID    string
	Label     string
	Hash      string `json:"-"`
	Scopes    []Permission
	CreatedAt time.Time
	ExpiresAt time.Time
}

// IssuedCredential exposes a secret once to the trusted issuer. It must never be logged.
type IssuedCredential struct {
	Record CredentialRecord
	Secret string `json:"-"`
}

// IssueCredential creates independent 256-bit credentials. Issuance authorization is the
// caller's responsibility; a token's scopes never override the user's repository membership.
func IssueCredential(userID, label string, scopes []Permission, now time.Time, ttl time.Duration) (IssuedCredential, error) {
	if !identifier.Valid(userID) || !utf8.ValidString(label) || strings.TrimSpace(label) == "" || len(label) > 120 || strings.ContainsAny(label, "\x00\r\n") || now.IsZero() || ttl < time.Minute || ttl > 365*24*time.Hour {
		return IssuedCredential{}, errors.New("invalid credential parameters")
	}
	owned := slices.Clone(scopes)
	slices.Sort(owned)
	owned = slices.Compact(owned)
	if len(owned) == 0 {
		return IssuedCredential{}, errors.New("credential scopes required")
	}
	for _, scope := range owned {
		if !SupportedPermission(scope) {
			return IssuedCredential{}, errors.New("unsupported credential scope")
		}
	}
	id, err := identifier.New()
	if err != nil {
		return IssuedCredential{}, err
	}
	var entropy [32]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		return IssuedCredential{}, err
	}
	secret := "wg_" + base64.RawURLEncoding.EncodeToString(entropy[:])
	return IssuedCredential{Secret: secret, Record: CredentialRecord{ID: id, UserID: userID, Label: strings.TrimSpace(label), Hash: CredentialHash(secret), Scopes: owned, CreatedAt: now.UTC(), ExpiresAt: now.UTC().Add(ttl)}}, nil
}

// CredentialHash accepts only canonical Workgraph secrets and returns a SHA-256 digest.
func CredentialHash(secret string) string {
	if len(secret) != 46 || !strings.HasPrefix(secret, "wg_") {
		return ""
	}
	decoded, err := base64.RawURLEncoding.DecodeString(secret[3:])
	if err != nil || len(decoded) != 32 || base64.RawURLEncoding.EncodeToString(decoded) != secret[3:] {
		return ""
	}
	digest := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(digest[:])
}
