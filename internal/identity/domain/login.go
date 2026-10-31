// Package domain defines explicit external-subject bindings, one-use browser login proof and local sessions.
package domain

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"path"
	"strings"
	"time"

	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/identifier"
)

var (
	ErrInvalid  = errors.New("invalid identity input")
	ErrLogin    = errors.New("login proof invalid or unavailable")
	ErrProvider = errors.New("local identity provider unavailable")
	ErrCapacity = errors.New("pending login capacity reached")
	ErrConflict = errors.New("identity binding conflict")
)

const (
	Policy        = "oidc-browser-v1"
	StatePrefix   = "wos_"
	FlowPrefix    = "wob_"
	SessionPrefix = "wss_"
	CSRFPrefix    = "wcs_"
)

// Config contains only public operator settings; no provider token or client secret enters fingerprints.
type Config struct {
	RegistrationEnabled bool   `json:"registration_enabled,omitempty"`
	Issuer              string `json:"issuer"`
	ClientID            string `json:"client_id"`
	PublicURL           string `json:"public_url"`
	AuthStyle           string `json:"auth_style"`
	SessionSeconds      int    `json:"session_seconds"`
	Policy              string `json:"policy"`
}

// URL accepts HTTPS identity endpoints or loopback HTTP for local integration/development.
func URL(value string) (*url.URL, error) {
	u, e := url.Parse(value)
	if e != nil || len(value) > 2048 || u.Host == "" || u.User != nil || u.Opaque != "" || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || u.ForceQuery {
		return nil, ErrInvalid
	}
	if u.Path != "" && u.Path != "/" && path.Clean(u.Path) != strings.TrimSuffix(u.Path, "/") {
		return nil, ErrInvalid
	}
	loop := u.Hostname() == "localhost"
	if ip := net.ParseIP(u.Hostname()); ip != nil {
		loop = ip.IsLoopback()
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && loop) {
		return nil, ErrInvalid
	}
	return u, nil
}

// Origin canonicalizes default ports/case for strict browser-origin and server-egress comparisons.
func Origin(u *url.URL) string {
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if port == "443" && u.Scheme == "https" || port == "80" && u.Scheme == "http" {
		port = ""
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return u.Scheme + "://" + host
}
func NormalizeConfig(c Config) (Config, error) {
	if _, e := URL(c.Issuer); e != nil {
		return Config{}, e
	}
	public, e := URL(c.PublicURL)
	if e != nil || public.Path != "" && public.Path != "/" || c.ClientID == "" || len(c.ClientID) > 200 || strings.TrimSpace(c.ClientID) != c.ClientID || strings.ContainsAny(c.ClientID, "\x00\r\n") || c.SessionSeconds < 900 || c.SessionSeconds > 28800 || c.Policy != Policy || (c.AuthStyle != "basic" && c.AuthStyle != "post") {
		return Config{}, ErrInvalid
	}
	c.PublicURL = Origin(public)
	return c, nil
}

// Fingerprint fences login/credential authority; signup admission has a separate durable policy.
func (c Config) Fingerprint() string {
	c.RegistrationEnabled = false
	raw, _ := json.Marshal(c)
	return Hash(raw)
}
func (c Config) Secure() bool     { return strings.HasPrefix(c.PublicURL, "https://") }
func (c Config) Callback() string { return c.PublicURL + "/v1/auth/oidc/callback" }
func (c Config) CookieNames() (session, csrf, flow string) {
	if c.Secure() {
		return "__Host-workgraph_session", "__Host-workgraph_csrf", "__Host-workgraph_login"
	}
	return "workgraph_session_dev", "workgraph_csrf_dev", "workgraph_login_dev"
}

// NewSecret creates a canonical independently random opaque credential for one purpose.
func NewSecret(prefix string) (string, error) {
	var raw [32]byte
	if _, e := rand.Read(raw[:]); e != nil {
		return "", e
	}
	return prefix + base64.RawURLEncoding.EncodeToString(raw[:]), nil
}
func Hash(raw []byte) string { h := sha256.Sum256(raw); return hex.EncodeToString(h[:]) }
func SecretHash(prefix, value string) string {
	if len(value) != len(prefix)+43 || !strings.HasPrefix(value, prefix) {
		return ""
	}
	raw, e := base64.RawURLEncoding.DecodeString(value[len(prefix):])
	if e != nil || len(raw) != 32 || base64.RawURLEncoding.EncodeToString(raw) != value[len(prefix):] {
		return ""
	}
	return Hash([]byte(value))
}

// Proof derives PKCE and nonce with distinct domains from the browser-only random secret and unique exposed state.
// Only hashes are persisted; database readers cannot reconstruct either verifier from a pending row.
func Proof(state, browser string) (verifier, nonce string, err error) {
	if SecretHash(StatePrefix, state) == "" || SecretHash(FlowPrefix, browser) == "" {
		return "", "", ErrLogin
	}
	derive := func(label string) string {
		m := hmac.New(sha256.New, []byte(browser))
		m.Write([]byte(label + "\x00" + state))
		return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
	}
	return derive("pkce-v1"), derive("nonce-v1"), nil
}
func Subject(value string) bool {
	if len(value) < 1 || len(value) > 255 {
		return false
	}
	for _, b := range []byte(value) {
		if b < 33 || b > 126 {
			return false
		}
	}
	return true
}

// InteractiveScopes permits UI operations subject to every current project/operator authority check.
// A capability is never a project membership; API self-delegation remains bounded by the session grant/expiry.
func InteractiveScopes() []access.Permission {
	return []access.Permission{access.ReadCapture, access.WriteCapture, access.ReadIntegration, access.ManageIntegration, access.ReadDirectory, access.ManageDirectory, access.ManagePlatform, access.ManageCredentials}
}

type Flow struct{ StateHash, BrowserHash, Configuration string }
type Identity struct{ Issuer, Subject, DisplayName string }

// Binding is immutable in issuer/subject/user identity. Disabling/re-enabling creates a new version, never a retarget.
type Binding struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	Issuer    string    `json:"issuer"`
	Subject   string    `json:"subject"`
	Enabled   bool      `json:"enabled"`
	Version   int64     `json:"version"`
	CreatedAt time.Time `json:"created_at"`
}
type BindInput struct {
	RequestID string `json:"request_id"`
	ID        string `json:"id"`
	UserID    string `json:"user_id"`
	Subject   string `json:"subject"`
}

func (i BindInput) Validate() error {
	if !identifier.Valid(i.RequestID) || !identifier.Valid(i.ID) || !identifier.Valid(i.UserID) || !Subject(i.Subject) {
		return ErrInvalid
	}
	return nil
}

type BindingChange struct {
	RequestID       string `json:"request_id"`
	ExpectedVersion int64  `json:"expected_version"`
	Enabled         *bool  `json:"enabled"`
}

// Session keeps hash-only browser proof and a non-bearer API principal for existing application authorization ports.
type Session struct {
	ID        string
	BindingID string
	Principal access.Principal `json:"-"`
	CSRFHash  string           `json:"-"`
	ExpiresAt time.Time
}
type SessionSeed struct {
	ID, CookieHash, CSRFHash, Configuration string
	Binding                                 Binding
	Credential                              access.CredentialRecord
	OldCookieHash                           string
}

// Login is returned only to the HTTP cookie writer; raw secrets are never part of a JSON response.
type Login struct {
	Session       Session
	Cookie        string `json:"-"`
	CSRF          string `json:"-"`
	ReusedSession bool   `json:"-"`
}
