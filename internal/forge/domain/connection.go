// Package domain defines provider connection, delivery authentication and reconciliation invariants.
package domain

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	capture "workgraph.local/platform/internal/capture/domain"
	"workgraph.local/platform/internal/jsonwire"
)

var (
	// ErrInvalid identifies malformed provider configuration or events without echoing their content.
	ErrInvalid = errors.New("invalid forge input")
	// ErrUnauthenticated identifies invalid, stale or disabled webhook credentials.
	ErrUnauthenticated = errors.New("invalid webhook authentication")
	// ErrConflict identifies a delivery identity reused for changed content or a stale connection lease.
	ErrConflict = errors.New("forge state conflict")
	// ErrNotFound identifies missing authorized integration state.
	ErrNotFound = errors.New("forge connection not found")
	// ErrCredentialUnavailable identifies a removed, blocked or rotated saved token selection.
	ErrCredentialUnavailable = errors.New("saved forge credential unavailable")
	// ErrCredentialEndpoint prevents forwarding a saved token across provider endpoint boundaries.
	ErrCredentialEndpoint = errors.New("saved forge credential endpoint mismatch")
)

// Repository holds the immutable provider identity owned by the catalog.
type Repository struct {
	ID        string
	BaseURL   string
	ProjectID int64
	Provider  string
}

// CredentialScope keeps existing GitLab envelopes compatible and isolates GitHub credentials.
func (r Repository) CredentialScope() string {
	if r.Provider == "github" {
		return "github:" + r.ID
	}
	return "gitlab:" + r.ID
}

// Secrets is encrypted as one scoped envelope and is never returned by read endpoints.
type Secrets struct {
	APIToken     string `json:"api_token"`
	WebhookMode  string `json:"webhook_mode,omitempty"`
	SigningToken string `json:"webhook_signing_token,omitempty"`
	LegacyToken  string `json:"legacy_webhook_token,omitempty"`
	Anonymous    bool   `json:"anonymous,omitempty"`
}

// Validate rejects ambiguous authentication modes and header-unsafe or weak key material.
func (s Secrets) Validate() error {
	if s.Anonymous {
		if s.APIToken != "" || s.WebhookMode != "disabled" || s.SigningToken != "" || s.LegacyToken != "" {
			return ErrInvalid
		}
		return nil
	}
	if !token(s.APIToken, 8, 4096) {
		return ErrInvalid
	}
	if s.WebhookMode == "disabled" {
		if s.SigningToken != "" || s.LegacyToken != "" {
			return ErrInvalid
		}
		return nil
	}
	if (s.SigningToken == "") == (s.LegacyToken == "") || (s.WebhookMode != "" && s.WebhookMode != s.Mode()) {
		return ErrInvalid
	}
	if s.SigningToken != "" {
		if _, err := signingKey(s.SigningToken); err != nil {
			return err
		}
	} else if !token(s.LegacyToken, 16, 256) {
		return ErrInvalid
	}
	return nil
}

// ValidateForProvider never treats an absent private token as a public fallback.
func (s Secrets) ValidateForProvider(provider string) error {
	if s.Validate() != nil {
		return ErrInvalid
	}
	if provider == "github" {
		if s.Mode() != "disabled" {
			return ErrInvalid
		}
		return nil
	}
	if provider != "" || s.Anonymous {
		return ErrInvalid
	}
	return nil
}

// Mode retains existing webhook credentials; API-only requires an explicit disabled policy.
func (s Secrets) Mode() string {
	if s.WebhookMode == "disabled" {
		return "disabled"
	}
	if s.SigningToken != "" {
		return "signature"
	}
	return "legacy"
}

func signingKey(value string) ([]byte, error) {
	if !strings.HasPrefix(value, "whsec_") || len(value) > 256 {
		return nil, ErrInvalid
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(value[6:])
	if err != nil || len(raw) < 32 || len(raw) > 128 {
		return nil, ErrInvalid
	}
	return raw, nil
}
func token(value string, min, max int) bool {
	if len(value) < min || len(value) > max {
		return false
	}
	for _, c := range []byte(value) {
		if c < 33 || c > 126 {
			return false
		}
	}
	return true
}

// Delivery preserves raw signed bytes only until authentication/parsing; inbox storage uses Event.
type Delivery struct {
	ID, Timestamp, Signature, LegacyToken, Event string
	Body                                         []byte
}

// AuthenticateDelivery denies disabled webhooks and verifies configured credentials with bounded replay tolerance.
func AuthenticateDelivery(secrets Secrets, d Delivery, now time.Time) error {
	if secrets.Validate() != nil || secrets.Mode() == "disabled" || !token(d.ID, 1, 200) || len(d.Body) == 0 || len(d.Body) > 2*1024*1024 {
		return ErrUnauthenticated
	}
	if secrets.Mode() == "legacy" {
		if d.Signature != "" {
			return ErrUnauthenticated
		}
		expected := sha256.Sum256([]byte(secrets.LegacyToken))
		actual := sha256.Sum256([]byte(d.LegacyToken))
		if subtle.ConstantTimeCompare(expected[:], actual[:]) != 1 {
			return ErrUnauthenticated
		}
		return nil
	}
	stamp, err := strconv.ParseInt(d.Timestamp, 10, 64)
	if err != nil || strconv.FormatInt(stamp, 10) != d.Timestamp || stamp < 1 || now.Sub(time.Unix(stamp, 0)) > 5*time.Minute || time.Unix(stamp, 0).Sub(now) > 5*time.Minute || len(d.Signature) > 4096 {
		return ErrUnauthenticated
	}
	key, err := signingKey(secrets.SigningToken)
	if err != nil {
		return ErrUnauthenticated
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(d.ID + "." + d.Timestamp + "."))
	_, _ = mac.Write(d.Body)
	expected := mac.Sum(nil)
	matched := false
	for _, signature := range strings.Fields(d.Signature) {
		kind, encoded, ok := strings.Cut(signature, ",")
		if !ok || kind != "v1" {
			continue
		}
		raw, err := base64.StdEncoding.Strict().DecodeString(encoded)
		if err == nil && hmac.Equal(expected, raw) {
			matched = true
		}
	}
	if !matched {
		return ErrUnauthenticated
	}
	return nil
}

// Event is the minimal retained webhook hint. Raw CI variables and message bodies are not persisted.
type Event struct {
	DeliveryKey, Digest, Kind, After string
	MergeRequestIID                  int64
}

// ParseEvent checks provider project identity; a webhook hint never confirms a commit.
func ParseEvent(d Delivery, projectID int64) (Event, error) {
	kinds := map[string]string{"Push Hook": "push", "Tag Push Hook": "tag_push", "Merge Request Hook": "merge_request", "Pipeline Hook": "pipeline", "Deployment Hook": "deployment", "Note Hook": "note", "Release Hook": "release"}
	var body map[string]json.RawMessage
	if jsonwire.DecodeExternalObject(d.Body, &body) != nil {
		return Event{}, ErrInvalid
	}
	var kind string
	if json.Unmarshal(body["object_kind"], &kind) != nil || kinds[d.Event] == "" || kind != kinds[d.Event] {
		return Event{}, ErrInvalid
	}
	var top int64
	if raw, ok := body["project_id"]; ok && json.Unmarshal(raw, &top) != nil {
		return Event{}, ErrInvalid
	}
	var project struct {
		ID int64 `json:"id"`
	}
	if raw, ok := body["project"]; ok && json.Unmarshal(raw, &project) != nil {
		return Event{}, ErrInvalid
	}
	if (top != 0 && top != projectID) || (project.ID != 0 && project.ID != projectID) || (top == 0 && project.ID == 0) {
		return Event{}, ErrUnauthenticated
	}
	event := Event{Kind: kind}
	if raw, ok := body["after"]; ok {
		if json.Unmarshal(raw, &event.After) != nil {
			return Event{}, ErrInvalid
		}
		if len(event.After) != 40 && len(event.After) != 64 {
			return Event{}, ErrInvalid
		}
		if strings.Trim(event.After, "0") == "" {
			event.After = ""
		} else if !capture.ValidCommit(event.After) {
			return Event{}, ErrInvalid
		}
	}
	if kind == "merge_request" {
		var attrs struct {
			IID int64 `json:"iid"`
		}
		if json.Unmarshal(body["object_attributes"], &attrs) != nil || attrs.IID < 1 {
			return Event{}, ErrInvalid
		}
		event.MergeRequestIID = attrs.IID
	}
	if !utf8.ValidString(d.ID) || !token(d.ID, 1, 200) {
		return Event{}, ErrInvalid
	}
	event.DeliveryKey = hash([]byte(d.ID))
	event.Digest = hash(d.Body)
	return event, nil
}
func hash(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
