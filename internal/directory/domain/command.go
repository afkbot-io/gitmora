// Package domain defines local account/catalog identities, versioned administration and limited credential delegation.
package domain

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"workgraph.local/platform/internal/access"
	"workgraph.local/platform/internal/endpoint"
	"workgraph.local/platform/internal/identifier"
	"workgraph.local/platform/internal/redact"
)

var (
	ErrInvalid      = errors.New("invalid directory input")
	ErrNotFound     = errors.New("directory entry unavailable")
	ErrConflict     = errors.New("directory version or identity conflict")
	ErrRetired      = errors.New("directory identity retired")
	ErrLastAdmin    = errors.New("last enabled project administrator")
	ErrLastOperator = errors.New("last enabled installation operator")
	ErrDelegation   = errors.New("credential delegation exceeds parent grant")
)

// MaxVersion keeps directory versions/cursors exact in JavaScript clients.
const MaxVersion int64 = 9007199254740991

// Action names one supported local state transition; arbitrary actions fail closed.
type Action string

const (
	CreateProject    Action = "create_project"
	RenameProject    Action = "rename_project"
	DeleteProject    Action = "delete_project"
	CreateRepository Action = "create_repository"
	RenameRepository Action = "rename_repository"
	DeleteRepository Action = "delete_repository"
	CreateUser       Action = "create_user"
	UpdateUser       Action = "update_user"
	DeleteUser       Action = "delete_user"
	SetMember        Action = "set_member"
	RemoveMember     Action = "remove_member"
	SetOperator      Action = "set_operator"
	RevokeToken      Action = "revoke_token"
)

// Command is an internal discriminated union. HTTP uses separate strict body types and route-owned actions/identities.
type Command struct {
	RequestID       string  `json:"request_id"`
	Action          Action  `json:"action"`
	ProjectID       string  `json:"project_id,omitempty"`
	EntityID        string  `json:"entity_id"`
	ExpectedVersion int64   `json:"expected_version,omitempty"`
	Name            *string `json:"name,omitempty"`
	GitLabURL       string  `json:"gitlab_url,omitempty"`
	GitLabProjectID int64   `json:"gitlab_project_id,omitempty"`
	Role            string  `json:"role,omitempty"`
	Enabled         *bool   `json:"enabled,omitempty"`
	Operator        *bool   `json:"operator,omitempty"`
}

// Normalize rejects mixed commands and normalizes/redacts before command fingerprinting.
func Normalize(c Command) (Command, error) {
	if !identifier.Valid(c.RequestID) || !identifier.Valid(c.EntityID) || c.ExpectedVersion < 0 || c.ExpectedVersion > MaxVersion {
		return Command{}, ErrInvalid
	}
	extra := c
	extra.RequestID = ""
	extra.Action = ""
	extra.EntityID = ""
	extra.ExpectedVersion = 0
	creation := false
	requiredName := false
	projectScoped := false
	switch c.Action {
	case CreateProject:
		creation = true
		requiredName = true
		extra.Name = nil
	case RenameProject:
		requiredName = true
		extra.Name = nil
	case DeleteProject:
	case CreateRepository:
		creation = true
		requiredName = true
		projectScoped = true
		extra.Name = nil
		extra.GitLabURL = ""
		extra.GitLabProjectID = 0
		base, ok := endpoint.GitLabBase(c.GitLabURL)
		if !ok || c.GitLabProjectID < 1 {
			return Command{}, ErrInvalid
		}
		c.GitLabURL = base
	case RenameRepository:
		requiredName = true
		projectScoped = true
		extra.Name = nil
	case DeleteRepository:
		projectScoped = true
	case CreateUser:
		creation = true
		requiredName = true
		extra.Name = nil
	case UpdateUser:
		extra.Name = nil
		extra.Enabled = nil
		if c.Name == nil && c.Enabled == nil {
			return Command{}, ErrInvalid
		}
	case DeleteUser:
	case SetMember:
		projectScoped = true
		extra.Role = ""
		if c.Role != "reader" && c.Role != "writer" && c.Role != "admin" {
			return Command{}, ErrInvalid
		}
	case RemoveMember:
		projectScoped = true
	case SetOperator:
		extra.Operator = nil
		if c.Operator == nil {
			return Command{}, ErrInvalid
		}
	case RevokeToken:
		creation = true
	default:
		return Command{}, ErrInvalid
	}
	if projectScoped {
		extra.ProjectID = ""
		if !identifier.Valid(c.ProjectID) {
			return Command{}, ErrInvalid
		}
	}
	if creation && c.ExpectedVersion != 0 || !creation && c.Action != SetMember && c.ExpectedVersion == 0 {
		return Command{}, ErrInvalid
	}
	if requiredName && c.Name == nil {
		return Command{}, ErrInvalid
	}
	if c.Name != nil {
		value, e := Name(*c.Name, 200)
		if e != nil {
			return Command{}, e
		}
		c.Name = &value
	}
	if c.Enabled != nil {
		value := *c.Enabled
		c.Enabled = &value
	}
	if c.Operator != nil {
		value := *c.Operator
		c.Operator = &value
	}
	if extra != (Command{}) {
		return Command{}, ErrInvalid
	}
	return c, nil
}

// Name accepts a bounded plain UTF-8 display value; stored metadata never receives known credential assignments.
func Name(raw string, max int) (string, error) {
	value := strings.TrimSpace(redact.Text(raw))
	if value == "" || !utf8.ValidString(value) || len(value) > max || strings.ContainsFunc(value, unicode.IsControl) {
		return "", ErrInvalid
	}
	return value, nil
}

// MatchVersion applies optimistic concurrency to an existing edition.
func MatchVersion(expected, current int64) error {
	if expected < 1 || expected > MaxVersion || expected != current {
		return ErrConflict
	}
	return nil
}

// CheckAdminRemoval protects the final enabled project administrator.
func CheckAdminRemoval(role string, enabled bool, next string, activeAdmins int) error {
	if role == "admin" && enabled && next != "admin" && activeAdmins <= 1 {
		return ErrLastAdmin
	}
	return nil
}

// CheckOperatorRemoval protects the final enabled installation operator.
func CheckOperatorRemoval(operator, enabled, next bool, activeOperators int) error {
	if operator && enabled && !next && activeOperators <= 1 {
		return ErrLastOperator
	}
	return nil
}
func (c Command) Kind() string {
	switch c.Action {
	case CreateProject, RenameProject, DeleteProject:
		return "project"
	case CreateRepository, RenameRepository, DeleteRepository:
		return "repository"
	case SetMember, RemoveMember:
		return "membership"
	case RevokeToken:
		return "token"
	default:
		return "user"
	}
}
func (c Command) ScopeProject() string {
	if c.Kind() == "project" {
		return c.EntityID
	}
	return c.ProjectID
}
func (c Command) Platform() bool { return c.Kind() == "user" || c.Action == CreateProject }
func (c Command) Deleted() bool {
	return c.Action == DeleteProject || c.Action == DeleteRepository || c.Action == DeleteUser || c.Action == RemoveMember || c.Action == RevokeToken
}
func (c Command) Canonical() []byte { b, _ := json.Marshal(c); return b }

// Receipt contains no names, content, credentials or provider URLs. Exact replay cannot perform another mutation.
type Receipt struct {
	RequestID      string    `json:"request_id"`
	Action         Action    `json:"action"`
	Kind           string    `json:"kind"`
	EntityID       string    `json:"entity_id"`
	ProjectID      string    `json:"project_id,omitempty"`
	Version        int64     `json:"version"`
	ProjectVersion int64     `json:"project_version,omitempty"`
	Deleted        bool      `json:"deleted"`
	ActorID        string    `json:"actor_id"`
	Sequence       int64     `json:"sequence"`
	At             time.Time `json:"at"`
}
type Event struct {
	Receipt
	Origin  string `json:"origin"`
	TokenID string `json:"token_id,omitempty"`
}

// Cursor is an opaque-ID pagination position, never an authorization grant.
type Cursor struct {
	AfterID string `json:"after_id,omitempty"`
	Limit   int    `json:"limit,omitempty"`
}

func (c Cursor) Normalize() (Cursor, error) {
	if c.AfterID != "" && !identifier.Valid(c.AfterID) || c.Limit < 0 || c.Limit > 100 {
		return Cursor{}, ErrInvalid
	}
	if c.Limit == 0 {
		c.Limit = 30
	}
	return c, nil
}

// Page contains only authorized items; NextID is the last returned ID when more exist.
type Page[T any] struct {
	Items  []T    `json:"items"`
	NextID string `json:"next_id,omitempty"`
}
type EventQuery struct {
	ProjectID     string `json:"project_id,omitempty"`
	AfterSequence int64  `json:"after_sequence,omitempty"`
	Limit         int    `json:"limit,omitempty"`
}
type EventPage struct {
	Items        []Event `json:"items"`
	NextSequence int64   `json:"next_sequence,omitempty"`
}

// Project is mutable local metadata, with an immutable ID and the caller's actual membership role.
type Project struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Version   int64     `json:"version"`
	Role      string    `json:"role,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// Repository pins a Workgraph project and provider origin/numeric ID; only its display name can change.
type Repository struct {
	ID              string    `json:"id"`
	ProjectID       string    `json:"project_id"`
	Name            string    `json:"name"`
	GitLabURL       string    `json:"gitlab_url"`
	GitLabProjectID int64     `json:"gitlab_project_id"`
	Version         int64     `json:"version"`
	CreatedAt       time.Time `json:"created_at"`
}

// User retains an opaque historical actor ID after logical deletion; enabling never restores revoked credentials.
type User struct {
	ID          string     `json:"id"`
	DisplayName string     `json:"display_name"`
	Enabled     bool       `json:"enabled"`
	Operator    bool       `json:"operator"`
	Version     int64      `json:"version"`
	CreatedAt   time.Time  `json:"created_at"`
	DeletedAt   *time.Time `json:"deleted_at,omitempty"`
}

// Member describes a local grant; its globally monotonic version prevents stale mutations after re-adding it.
type Member struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name"`
	Enabled     bool   `json:"enabled"`
	Role        string `json:"role"`
	Version     int64  `json:"version"`
}

// Me describes the current credential/account, without granting project content from operator status.
type Me struct {
	User      User                `json:"user"`
	TokenID   string              `json:"token_id"`
	Scopes    []access.Permission `json:"scopes"`
	ExpiresAt time.Time           `json:"expires_at"`
}

// TokenInput permits only limited self-delegation; no credential secret appears in its fingerprint or stored views.
type TokenInput struct {
	RequestID  string              `json:"request_id"`
	Label      string              `json:"label"`
	Scopes     []access.Permission `json:"scopes"`
	TTLSeconds int64               `json:"ttl_seconds"`
}

func (t TokenInput) Normalize() (TokenInput, error) {
	if !identifier.Valid(t.RequestID) || t.TTLSeconds < 60 || t.TTLSeconds > 90*24*60*60 || len(t.Scopes) < 1 || len(t.Scopes) > 32 {
		return TokenInput{}, ErrInvalid
	}
	label, e := Name(t.Label, 120)
	if e != nil {
		return TokenInput{}, e
	}
	t.Label = label
	t.Scopes = slices.Clone(t.Scopes)
	slices.Sort(t.Scopes)
	t.Scopes = slices.Compact(t.Scopes)
	for _, cap := range t.Scopes {
		if !access.SupportedPermission(cap) {
			return TokenInput{}, ErrInvalid
		}
	}
	return t, nil
}

// Token exposes owned credential metadata, including its revocation parent, but never the hash or bearer.
type Token struct {
	Kind      string              `json:"kind"`
	ParentID  string              `json:"parent_id,omitempty"`
	ID        string              `json:"id"`
	Label     string              `json:"label"`
	Scopes    []access.Permission `json:"scopes"`
	CreatedAt time.Time           `json:"created_at"`
	ExpiresAt time.Time           `json:"expires_at"`
	RevokedAt *time.Time          `json:"revoked_at,omitempty"`
}

// IssuedToken includes a bearer once, after durable first acceptance; replay returns metadata only.
type IssuedToken struct {
	Token   Token  `json:"token"`
	Secret  string `json:"secret,omitempty"`
	Created bool   `json:"created"`
}
