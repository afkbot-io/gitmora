// Package domain defines immutable capture evidence independently of storage and transport.
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"workgraph.local/platform/internal/identifier"
)

// ErrInvalid identifies capture input that violates the versioned domain contract.
var ErrInvalid = errors.New("invalid capture")

// MaxSnapshotBytes bounds the canonical representation of a complete session revision.
const MaxSnapshotBytes = 2 * 1024 * 1024

// ValidID reports whether id is a canonical nonzero UUID. IDs never grant access.
func ValidID(id string) bool {
	return identifier.Valid(id)
}

// ValidCommit reports whether sha is a full SHA-1 or SHA-256 Git object identifier.
func ValidCommit(sha string) bool {
	return identifier.ValidCommit(sha)
}

// Agent identifies the client integration and locally configured model.
type Agent struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	Model   string `json:"model"`
}

// TaskRef retains the legacy namespace in immutable version-1 captures. It has no remote task operations.
type TaskRef struct {
	CountryID string `json:"country_id"`
	TaskID    string `json:"task_id"`
}

// Message is exposed conversation or tool evidence, not hidden model reasoning.
type Message struct {
	Role     string `json:"role"`
	Text     string `json:"text"`
	ToolName string `json:"tool_name,omitempty"`
	Kind     string `json:"kind,omitempty"`
}

// Snapshot is the versioned ingestion DTO; NewSession validates and takes ownership of a copy.
type Snapshot struct {
	SchemaVersion int           `json:"schema_version"`
	RepositoryID  string        `json:"repository_id"`
	SessionID     string        `json:"session_id"`
	Revision      int64         `json:"revision"`
	Agent         Agent         `json:"agent"`
	StartedAt     time.Time     `json:"started_at"`
	EndedAt       *time.Time    `json:"ended_at,omitempty"`
	Task          *TaskRef      `json:"task,omitempty"`
	Messages      []Message     `json:"messages"`
	Files         []string      `json:"files"`
	Commits       []string      `json:"commits"`
	Continuation  *Continuation `json:"continuation,omitempty"`
	Origin        *ImportOrigin `json:"origin,omitempty"`
}

// Session is a validated, canonical and immutable revision of client-observed evidence.
type Session struct {
	snapshot  Snapshot
	canonical []byte
	digest    string
}

// NewSession rejects invalid or excessive input and canonicalizes unordered associations.
func NewSession(input Snapshot) (Session, error) {
	invalid := func(field string) (Session, error) { return Session{}, fmt.Errorf("%w: %s", ErrInvalid, field) }
	if input.SchemaVersion != 1 {
		return invalid("schema_version")
	}
	if !ValidID(input.RepositoryID) {
		return invalid("repository_id")
	}
	if !ValidID(input.SessionID) {
		return invalid("session_id")
	}
	if input.Revision < 1 || input.Revision > 1_000_000_000 {
		return invalid("revision")
	}
	if !boundedText(input.Agent.Name, 120, true) || !boundedText(input.Agent.Model, 240, true) || !boundedText(input.Agent.Version, 120, false) {
		return invalid("agent")
	}
	if input.StartedAt.IsZero() {
		return invalid("started_at")
	}
	if input.EndedAt != nil && (input.EndedAt.IsZero() || input.EndedAt.Before(input.StartedAt)) {
		return invalid("ended_at")
	}
	if input.Task != nil && (!ValidID(input.Task.CountryID) || !ValidID(input.Task.TaskID)) {
		return invalid("task")
	}
	if input.Continuation != nil && input.Continuation.Validate(input.SessionID) != nil {
		return invalid("continuation")
	}
	if len(input.Messages) == 0 || len(input.Messages) > 5000 {
		return invalid("messages count")
	}
	for _, message := range input.Messages {
		if message.Kind != "" && !((message.Kind == "agent_summary" && message.Role == "assistant") ||
			(message.Kind == "import_notice" && message.Role == "system" && input.Origin != nil) ||
			(message.Kind == "provenance_report" && message.Role == "system" && input.Origin != nil && input.Origin.System == "git-ai")) {
			return invalid("message kind")
		}
		switch message.Role {
		case "system", "user", "assistant", "tool":
		default:
			return invalid("message role")
		}
		if !boundedText(message.Text, 128*1024, false) || !boundedText(message.ToolName, 160, message.Role == "tool") {
			return invalid("message content")
		}
	}
	if len(input.Files) > 5000 || len(input.Commits) > 2000 {
		return invalid("association count")
	}
	for _, file := range input.Files {
		if !ValidFilePath(file) {
			return invalid("file path")
		}
	}
	for _, sha := range input.Commits {
		if !ValidCommit(sha) {
			return invalid("commit sha")
		}
	}
	if validateOrigin(input.Origin, input) != nil {
		return invalid("import origin")
	}
	owned := cloneSnapshot(input)
	owned.StartedAt = owned.StartedAt.UTC()
	if owned.EndedAt != nil {
		utc := owned.EndedAt.UTC()
		owned.EndedAt = &utc
	}
	owned.Files = canonicalSet(owned.Files)
	owned.Commits = canonicalSet(owned.Commits)
	canonical, err := json.Marshal(owned)
	if err != nil {
		return invalid("serialization")
	}
	if len(canonical) > MaxSnapshotBytes {
		return invalid("snapshot bytes")
	}
	hash := sha256.Sum256(canonical)
	return Session{snapshot: owned, canonical: canonical, digest: hex.EncodeToString(hash[:])}, nil
}

// Snapshot returns a detached copy suitable for transport or persistence.
func (s Session) Snapshot() Snapshot { return cloneSnapshot(s.snapshot) }

// Digest identifies canonical content, not user authority or provider confirmation.
func (s Session) Digest() string { return s.digest }

// CanonicalJSON returns detached normalized bytes for this revision.
func (s Session) CanonicalJSON() []byte { return slices.Clone(s.canonical) }

func boundedText(value string, max int, required bool) bool {
	return utf8.ValidString(value) && len(value) <= max && !strings.ContainsRune(value, 0) && (!required || strings.TrimSpace(value) != "")
}

func canonicalSet(values []string) []string {
	copy := append([]string{}, values...)
	slices.Sort(copy)
	return slices.Compact(copy)
}

func cloneSnapshot(s Snapshot) Snapshot {
	s.Origin = cloneOrigin(s.Origin)
	if s.Continuation != nil {
		copy := *s.Continuation
		s.Continuation = &copy
	}
	s.Messages = slices.Clone(s.Messages)
	s.Files = slices.Clone(s.Files)
	s.Commits = slices.Clone(s.Commits)
	if s.Task != nil {
		task := *s.Task
		s.Task = &task
	}
	if s.EndedAt != nil {
		ended := *s.EndedAt
		s.EndedAt = &ended
	}
	return s
}

// ValidFilePath accepts only bounded canonical repository-relative evidence paths.
func ValidFilePath(file string) bool {
	return boundedText(file, 1024, true) && file != "." && file != ".." && !strings.HasPrefix(file, "../") && !strings.ContainsAny(file, "\\\x00\r\n") && !path.IsAbs(file) && path.Clean(file) == file && file != ".git" && !strings.HasPrefix(file, ".git/")
}
