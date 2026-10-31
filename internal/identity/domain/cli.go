package domain

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/identifier"
)

const (
	// CLIDevicePrefix and CLIProofPrefix distinguish independent enrollment proofs from API/browser credentials.
	CLIDevicePrefix = "wcd_"
	CLIProofPrefix  = "wcp_"
	CLIPolicy       = "cli-enrollment-v1"
	CLIFlowTTL      = 10 * time.Minute
	CLIFreshSession = 5 * time.Minute
)

// ErrFreshLogin requires a newly verified local OIDC session before independent long-lived enrollment.
var ErrFreshLogin = errors.New("fresh browser login required")
var cliDigest = regexp.MustCompile(`^[0-9a-f]{64}$`)

const cliAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// CLIRequest registers only hash commitments; the candidate API bearer remains on its originating device.
type CLIRequest struct {
	ID             string              `json:"id"`
	DeviceHash     string              `json:"device_hash"`
	ProofHash      string              `json:"proof_hash"`
	CredentialHash string              `json:"credential_hash"`
	CodeHash       string              `json:"code_hash"`
	Label          string              `json:"label"`
	Scopes         []access.Permission `json:"scopes"`
	TTLSeconds     int64               `json:"ttl_seconds"`
}

// CLIScope permits engineering operations and owned credential management, never installation/provider administration.
func CLIScope(p access.Permission) bool {
	return slices.Contains([]access.Permission{access.ReadCapture, access.WriteCapture, access.ReadIntegration, access.ReadDirectory, access.ManageCredentials}, p)
}

// Normalize owns sorted capabilities and bounds both requested authority and independent-root lifetime.
func (r CLIRequest) Normalize() (CLIRequest, error) {
	if !identifier.Valid(r.ID) || !utf8.ValidString(r.Label) || len(r.Label) > 120 || strings.ContainsAny(r.Label, "\x00\r\n") || len(r.Scopes) < 1 || len(r.Scopes) > 16 || r.TTLSeconds < 60 || r.TTLSeconds > 90*86400 {
		return CLIRequest{}, ErrInvalid
	}
	r.Label = strings.TrimSpace(r.Label)
	if r.Label == "" {
		return CLIRequest{}, ErrInvalid
	}
	hashes := []string{r.DeviceHash, r.ProofHash, r.CredentialHash, r.CodeHash}
	for i, h := range hashes {
		if !cliDigest.MatchString(h) || slices.Contains(hashes[:i], h) {
			return CLIRequest{}, ErrInvalid
		}
	}
	r.Scopes = slices.Clone(r.Scopes)
	slices.Sort(r.Scopes)
	r.Scopes = slices.Compact(r.Scopes)
	if !slices.Contains(r.Scopes, access.ManageCredentials) {
		return CLIRequest{}, ErrInvalid
	}
	for _, p := range r.Scopes {
		if !CLIScope(p) {
			return CLIRequest{}, ErrInvalid
		}
	}
	return r, nil
}

// NormalizeCLICode accepts the printable eight-character code with an optional visual separator.
func NormalizeCLICode(value string) (string, error) {
	value = strings.ToUpper(strings.TrimSpace(value))
	if len(value) == 9 && value[4] == '-' {
		value = value[:4] + value[5:]
	}
	if len(value) != 8 {
		return "", ErrInvalid
	}
	for _, c := range value {
		if !strings.ContainsRune(cliAlphabet, c) {
			return "", ErrInvalid
		}
	}
	return value[:4] + "-" + value[4:], nil
}

// CLICodeHash domain-separates the public human code from device and bearer hashes.
func CLICodeHash(code string) string {
	n, e := NormalizeCLICode(code)
	if e != nil {
		return ""
	}
	return Hash([]byte(CLIPolicy + "\x00code\x00" + n))
}

// CLISecrets is device-owned entropy. It is never an HTTP response or a server persistence model.
type CLISecrets struct {
	Device     string `json:"-"`
	Proof      string `json:"-"`
	Credential string `json:"-"`
	Code       string `json:"-"`
}

// NewCLISecrets independently generates three 256-bit secrets and a 40-bit human approval code.
func NewCLISecrets() (CLISecrets, error) {
	var s CLISecrets
	var e error
	if s.Device, e = NewSecret(CLIDevicePrefix); e != nil {
		return CLISecrets{}, e
	}
	if s.Proof, e = NewSecret(CLIProofPrefix); e != nil {
		return CLISecrets{}, e
	}
	if s.Credential, e = NewSecret("wg_"); e != nil {
		return CLISecrets{}, e
	}
	var raw [8]byte
	if _, e = rand.Read(raw[:]); e != nil {
		return CLISecrets{}, e
	}
	for i := range raw {
		raw[i] = cliAlphabet[raw[i]&31]
	}
	s.Code = string(raw[:4]) + "-" + string(raw[4:])
	return s, nil
}

// CLIEnrollment preserves one immutable registration and its terminal human decision.
type CLIEnrollment struct {
	Request             CLIRequest
	Configuration       string
	CreatedAt           time.Time
	ExpiresAt           time.Time
	Status              string
	UserID              string
	TokenID             string
	CredentialExpiresAt *time.Time
}

// Fingerprint pins the complete request, exact deployment and this flow's creation, never its mutable decision.
func (v CLIEnrollment) Fingerprint() string {
	raw, _ := json.Marshal(struct {
		Policy        string
		Request       CLIRequest
		Configuration string
		CreatedAt     time.Time
	}{CLIPolicy, v.Request, v.Configuration, v.CreatedAt.UTC()})
	return Hash(raw)
}

// Matches checks both purpose-specific independent polling proofs without exposing their stored commitments.
func (v CLIEnrollment) Matches(device, proof string) bool {
	d, p := SecretHash(CLIDevicePrefix, device), SecretHash(CLIProofPrefix, proof)
	return d != "" && p != "" && subtle.ConstantTimeCompare([]byte(d), []byte(v.Request.DeviceHash)) == 1 && subtle.ConstantTimeCompare([]byte(p), []byte(v.Request.ProofHash)) == 1
}

// CLIView is browser/device metadata only. No raw secret or hash commitment is serialized.
type CLIView struct {
	ID                  string              `json:"id"`
	Label               string              `json:"label"`
	Scopes              []access.Permission `json:"scopes"`
	TTLSeconds          int64               `json:"ttl_seconds"`
	CreatedAt           time.Time           `json:"created_at"`
	ExpiresAt           time.Time           `json:"expires_at"`
	Fingerprint         string              `json:"fingerprint"`
	Status              string              `json:"status"`
	UserID              string              `json:"user_id,omitempty"`
	TokenID             string              `json:"token_id,omitempty"`
	CredentialExpiresAt *time.Time          `json:"credential_expires_at,omitempty"`
}

// View returns owned metadata and an exact confirmation precondition, without any enrollment proof.
func (v CLIEnrollment) View() CLIView {
	if v.CredentialExpiresAt != nil {
		expiry := *v.CredentialExpiresAt
		v.CredentialExpiresAt = &expiry
	}
	return CLIView{ID: v.Request.ID, Label: v.Request.Label, Scopes: slices.Clone(v.Request.Scopes), TTLSeconds: v.Request.TTLSeconds, CreatedAt: v.CreatedAt, ExpiresAt: v.ExpiresAt, Fingerprint: v.Fingerprint(), Status: v.Status, UserID: v.UserID, TokenID: v.TokenID, CredentialExpiresAt: v.CredentialExpiresAt}
}
