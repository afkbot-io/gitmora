// Package native owns native-client authentication and secret-store contracts without OS/HTTP dependencies.
package native

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/identifier"
	"workgraph.local/platform/internal/identity/domain"
	"workgraph.local/platform/internal/jsonwire"
)

var (
	ErrInvalid        = errors.New("invalid native authentication input or secret-store record")
	ErrNotFound       = errors.New("no saved credential for this server")
	ErrVault          = errors.New("system secret store unavailable")
	ErrBusy           = errors.New("another authentication operation is active for this server")
	ErrUnauthorized   = errors.New("saved credential is expired, revoked or unavailable")
	ErrForbidden      = errors.New("credential operation is not permitted")
	ErrUnavailable    = errors.New("authentication server unavailable")
	ErrProtocol       = errors.New("authentication server response invalid")
	ErrConflict       = errors.New("authentication intent conflicts with existing state")
	ErrRejected       = errors.New("CLI enrollment was rejected")
	ErrEnrollmentGone = errors.New("CLI enrollment window is unavailable")
)

// CanonicalServer retains the exact application path while normalizing its scheme/host/default port identity.
func CanonicalServer(value string) (string, error) {
	u, e := domain.URL(strings.TrimSpace(value))
	if e != nil {
		return "", ErrInvalid
	}
	return domain.Origin(u) + strings.TrimSuffix(u.EscapedPath(), "/"), nil
}

// Account is safe current-server credential metadata, never an authorization cache for the API.
type Account struct {
	UserID      string              `json:"user_id"`
	TokenID     string              `json:"token_id"`
	DisplayName string              `json:"display_name"`
	Scopes      []access.Permission `json:"scopes"`
	ExpiresAt   time.Time           `json:"expires_at"`
}

// Validate checks owned metadata without rejecting an expired record that must still be removable/status-visible.
func (a Account) Validate() error {
	if !identifier.Valid(a.UserID) || !identifier.Valid(a.TokenID) || !utf8.ValidString(a.DisplayName) || strings.TrimSpace(a.DisplayName) == "" || len(a.DisplayName) > 512 || a.ExpiresAt.IsZero() || len(a.Scopes) < 1 || len(a.Scopes) > 32 {
		return ErrInvalid
	}
	for _, p := range a.Scopes {
		if !access.SupportedPermission(p) {
			return ErrInvalid
		}
	}
	return nil
}

// Enrollment is immutable device-owned recovery state apart from the first acknowledged server deadline.
type Enrollment struct {
	Request   domain.CLIRequest `json:"-"`
	Device    string            `json:"-"`
	Proof     string            `json:"-"`
	Code      string            `json:"-"`
	ExpiresAt time.Time         `json:"-"`
}

// String and GoString hide proof material when a pending record is used in diagnostics.
func (Enrollment) String() string   { return "Workgraph enrollment (private)" }
func (Enrollment) GoString() string { return "Workgraph enrollment (private)" }

// Record belongs only in a system secret store; generic formatting/marshaling intentionally hides key material.
type Record struct {
	Server  string
	Secret  string
	Pending *Enrollment
	Account *Account
}

// NewPendingRecord owns independent candidate/proofs and one immutable normalized enrollment intent.
func NewPendingRecord(server, label string, scopes []access.Permission, ttl int64) (Record, error) {
	canonical, e := CanonicalServer(server)
	if e != nil {
		return Record{}, e
	}
	s, e := domain.NewCLISecrets()
	if e != nil {
		return Record{}, e
	}
	id, e := identifier.New()
	if e != nil {
		return Record{}, e
	}
	r, e := (domain.CLIRequest{ID: id, DeviceHash: domain.SecretHash(domain.CLIDevicePrefix, s.Device), ProofHash: domain.SecretHash(domain.CLIProofPrefix, s.Proof), CredentialHash: access.CredentialHash(s.Credential), CodeHash: domain.CLICodeHash(s.Code), Label: label, Scopes: scopes, TTLSeconds: ttl}).Normalize()
	if e != nil {
		return Record{}, ErrInvalid
	}
	return Record{Server: canonical, Secret: s.Credential, Pending: &Enrollment{Request: r, Device: s.Device, Proof: s.Proof, Code: s.Code}}, nil
}

// Validate binds recovery proofs to their exact proposed key, scope and destination; incomplete/mixed states fail closed.
func (r Record) Validate() error {
	server, e := CanonicalServer(r.Server)
	if e != nil || server != r.Server || access.CredentialHash(r.Secret) == "" || (r.Pending == nil) == (r.Account == nil) {
		return ErrInvalid
	}
	if r.Account != nil {
		return r.Account.Validate()
	}
	p := r.Pending
	n, e := p.Request.Normalize()
	if e != nil || n.DeviceHash != domain.SecretHash(domain.CLIDevicePrefix, p.Device) || n.ProofHash != domain.SecretHash(domain.CLIProofPrefix, p.Proof) || n.CredentialHash != access.CredentialHash(r.Secret) || n.CodeHash != domain.CLICodeHash(p.Code) || !slices.Equal(n.Scopes, p.Request.Scopes) || n.Label != p.Request.Label {
		return ErrInvalid
	}
	return nil
}

// MarshalJSON reports safe local metadata only; the secret-store codec is deliberately separate.
func (r Record) MarshalJSON() ([]byte, error) {
	state := "active"
	id := ""
	if r.Pending != nil {
		state = "pending"
		id = r.Pending.Request.ID
	}
	return json.Marshal(struct {
		Server    string   `json:"server"`
		State     string   `json:"state"`
		RequestID string   `json:"request_id,omitempty"`
		Account   *Account `json:"account,omitempty"`
	}{r.Server, state, id, r.Account})
}

// String and GoString prevent accidental diagnostic interpolation of device secrets.
func (Record) String() string   { return "Workgraph credential record (private)" }
func (Record) GoString() string { return "Workgraph credential record (private)" }

type secretEnvelope struct {
	Version int               `json:"version"`
	Server  string            `json:"server"`
	Secret  string            `json:"secret"`
	Pending *secretEnrollment `json:"pending,omitempty"`
	Account *Account          `json:"account,omitempty"`
}
type secretEnrollment struct {
	Request   domain.CLIRequest `json:"request"`
	Device    string            `json:"device"`
	Proof     string            `json:"proof"`
	Code      string            `json:"code"`
	ExpiresAt time.Time         `json:"expires_at"`
}

// EncodeSecretStoreRecord is the sole plaintext codec for an OS-protected record, never a file/API/log format.
func EncodeSecretStoreRecord(r Record) ([]byte, error) {
	if e := r.Validate(); e != nil {
		return nil, e
	}
	v := secretEnvelope{Version: 1, Server: r.Server, Secret: r.Secret, Account: r.Account}
	if r.Pending != nil {
		p := r.Pending
		v.Pending = &secretEnrollment{Request: p.Request, Device: p.Device, Proof: p.Proof, Code: p.Code, ExpiresAt: p.ExpiresAt}
	}
	raw, e := json.Marshal(v)
	if e != nil || len(raw) > 16384 {
		return nil, ErrInvalid
	}
	return raw, nil
}

// DecodeSecretStoreRecord rejects changed destination, unknown versions/fields and malformed or retargeted key commitments.
func DecodeSecretStoreRecord(server string, raw []byte) (Record, error) {
	canonical, e := CanonicalServer(server)
	if e != nil || len(raw) > 16384 {
		return Record{}, ErrInvalid
	}
	var v secretEnvelope
	if jsonwire.DecodeObject(raw, &v) != nil || v.Version != 1 || v.Server != canonical {
		return Record{}, ErrInvalid
	}
	r := Record{Server: v.Server, Secret: v.Secret, Account: v.Account}
	// The core migration narrows existing server grants. Migrate only known retired
	// metadata here; unknown authority still fails validation and pending intent is immutable.
	if r.Account != nil {
		r.Account.Scopes = CoreScopes(r.Account.Scopes)
	}
	if v.Pending != nil {
		p := v.Pending
		r.Pending = &Enrollment{Request: p.Request, Device: p.Device, Proof: p.Proof, Code: p.Code, ExpiresAt: p.ExpiresAt}
	}
	if e = r.Validate(); e != nil {
		return Record{}, e
	}
	return r, nil
}

// CoreScopes narrows only known pre-core metadata. Unknown capabilities remain for strict validation.
// It is not an authorization grant; current authority always comes from the selected server.
func CoreScopes(scopes []access.Permission) []access.Permission {
	return slices.DeleteFunc(slices.Clone(scopes), func(p access.Permission) bool {
		switch p {
		case "knowledge:read", "knowledge:write", "knowledge:review", "review:read", "review:run":
			return true
		}
		return false
	})
}
