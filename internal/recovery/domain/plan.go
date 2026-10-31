// Package domain defines the authority transition required before a restored database may serve traffic.
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/identifier"
)

var (
	ErrInvalid     = errors.New("invalid recovery authority intent")
	ErrConflict    = errors.New("recovery authority epoch or intent conflicts")
	ErrSchema      = errors.New("recovery application or database schema does not match")
	ErrUnavailable = errors.New("recovery authority transaction unavailable")
	digest         = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Provenance pins the source snapshot, exact application schema and previous recovery epoch.
type Provenance struct {
	BackupID string `json:"backup_id"`
	Schema   string `json:"schema"`
	ParentID string `json:"parent_id,omitempty"`
}

// CredentialProof carries only a new independent hash commitment, never a bearer or restored credential.
type CredentialProof struct {
	ID        string    `json:"id"`
	Hash      string    `json:"hash"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Plan is an immutable offline intent. It grants metadata administration without content permissions.
type Plan struct {
	Version  int             `json:"version"`
	ID       string          `json:"id"`
	BackupID string          `json:"backup_id"`
	Schema   string          `json:"schema"`
	ParentID string          `json:"parent_id,omitempty"`
	UserID   string          `json:"user_id"`
	Name     string          `json:"name"`
	Key      CredentialProof `json:"key"`
}

// MetadataScopes returns an owned minimal installation-administration capability set.
func MetadataScopes() []access.Permission {
	return []access.Permission{access.ReadDirectory, access.ManageDirectory, access.ManagePlatform, access.ManageCredentials}
}

// Normalize validates bounded fresh authority and canonicalizes text/time without mutating the caller.
func (p Plan) Normalize(now time.Time) (Plan, error) {
	if p.Version != 1 || !identifier.Valid(p.ID) || !identifier.Valid(p.BackupID) || !identifier.Valid(p.UserID) || !identifier.Valid(p.Key.ID) || !digest.MatchString(p.Schema) || !digest.MatchString(p.Key.Hash) || now.IsZero() {
		return Plan{}, ErrInvalid
	}
	if p.ParentID != "" && (!identifier.Valid(p.ParentID) || p.ParentID == p.ID) {
		return Plan{}, ErrInvalid
	}
	if !utf8.ValidString(p.Name) || len(p.Name) > 200 || strings.ContainsAny(p.Name, "\x00\r\n") || strings.TrimSpace(p.Name) == "" {
		return Plan{}, ErrInvalid
	}
	duration := p.Key.ExpiresAt.Sub(p.Key.CreatedAt)
	if p.Key.CreatedAt.IsZero() || p.Key.CreatedAt.After(now.Add(30*time.Second)) || !p.Key.ExpiresAt.After(now) || duration < time.Minute || duration > 24*time.Hour {
		return Plan{}, ErrInvalid
	}
	p.Name = strings.TrimSpace(p.Name)
	p.Key.CreatedAt = p.Key.CreatedAt.UTC()
	p.Key.ExpiresAt = p.Key.ExpiresAt.UTC()
	return p, nil
}

// Fingerprint binds every identity, hash, deadline and source/epoch field to exact retry semantics.
func (p Plan) Fingerprint() string {
	raw, _ := json.Marshal(p)
	sum := sha256.Sum256(append([]byte("gitmora-recovery-authority-v1\x00"), raw...))
	return hex.EncodeToString(sum[:])
}

// Counts records content-free displaced authority/work totals, not payloads or provider secrets.
type Counts struct {
	Users                int64 `json:"users"`
	Tokens               int64 `json:"tokens"`
	Memberships          int64 `json:"memberships"`
	Operators            int64 `json:"operators"`
	Bindings             int64 `json:"bindings"`
	Flows                int64 `json:"flows"`
	GitLabConnections    int64 `json:"gitlab_connections"`
	TasktopiaConnections int64 `json:"tasktopia_connections"`
	TaskWrites           int64 `json:"task_writes"`
	TaskReads            int64 `json:"task_reads"`
}

// Receipt is durable first-commit evidence; replay never renews or restores the issued authority.
type Receipt struct {
	ID       string    `json:"id"`
	BackupID string    `json:"backup_id"`
	UserID   string    `json:"user_id"`
	TokenID  string    `json:"token_id"`
	At       time.Time `json:"at"`
	Counts   Counts    `json:"counts"`
}
