package domain

import (
	"encoding/json"
	"regexp"

	capture "workgraph.local/platform/internal/capture/domain"
	"workgraph.local/platform/internal/identifier"
	"workgraph.local/platform/internal/redact"
)

// MaxPendingFrontiers bounds incomplete tool recovery per worktree, including unknown ends.
const MaxPendingFrontiers = 64

// CheckpointBinding pins a tool to a half-open endpoint in one normalized source window.
// MessageEnd counts stored messages, not native parts, bytes or user-visible turns.
type CheckpointBinding struct {
	ID         string
	MessageEnd int
}

var agentLocator = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,119}$`)
var frontierKey = regexp.MustCompile(`^[0-9a-f]{64}$`)

// ValidAgentName permits bounded non-secret adapter names in local source and consent metadata.
func ValidAgentName(name string) bool {
	return agentLocator.MatchString(name) && redact.Text(name) == name
}

// NativeSource is private recovery metadata with the original, possibly absent task scope.
type NativeSource struct {
	Worktree  string           `json:"worktree"`
	Agent     string           `json:"agent"`
	SessionID string           `json:"source_session_id"`
	Task      *capture.TaskRef `json:"task"`
}

// Validate rejects malformed source locators and task identities before persistence.
func (s NativeSource) Validate() error {
	if !boundedID(s.Worktree, 256) || !ValidAgentName(s.Agent) || !boundedID(s.SessionID, 200) ||
		redact.Text(s.Agent) != s.Agent || redact.Text(s.SessionID) != s.SessionID ||
		(s.Task != nil && (!identifier.Valid(s.Task.CountryID) || !identifier.Valid(s.Task.TaskID))) {
		return ErrInvalid
	}
	return nil
}

// Key matches the historic task-independent native mapping key byte for byte.
func (s NativeSource) Key(repository string) string {
	return NativeSessionKey(repository, s.Worktree, s.Agent, s.SessionID, nil)
}

// Frontier retains the first observed boundaries and the exact accepted capture target.
// After=nil means unobserved; After pointing to an empty string means observed unborn HEAD.
type Frontier struct {
	ID              string       `json:"id"`
	NativeKey       string       `json:"native_key"`
	Source          NativeSource `json:"source"`
	Before          string       `json:"before"`
	After           *string      `json:"after"`
	TargetSessionID string       `json:"target_session_id"`
	MessageEnd      *int         `json:"message_end,omitempty"`
	Applied         bool         `json:"applied"`
	Abandoned       bool         `json:"abandoned"`
	Reason          string       `json:"reason"`
}

// BeginFrontier derives a scoped stable call identity; arbitrary raw call IDs are never stored.
func BeginFrontier(repository string, source NativeSource, call, before string) (Frontier, error) {
	if !identifier.Valid(repository) || source.Validate() != nil || !boundedID(call, 200) || !validHead(before) {
		return Frontier{}, ErrInvalid
	}
	native := source.Key(repository)
	raw, _ := json.Marshal([]string{native, call})
	f := Frontier{ID: hash(raw), NativeKey: native, Source: source, Before: before}
	return RestoreFrontier(f)
}

// RestoreFrontier checks persisted state and returns an owned copy.
func RestoreFrontier(f Frontier) (Frontier, error) {
	if (f.MessageEnd != nil && (*f.MessageEnd < 1 || *f.MessageEnd > 5000 || f.TargetSessionID == "")) || !frontierKey.MatchString(f.ID) || !frontierKey.MatchString(f.NativeKey) || f.Source.Validate() != nil || !validHead(f.Before) ||
		(f.After != nil && !validHead(*f.After)) || (f.TargetSessionID != "" && !identifier.Valid(f.TargetSessionID)) ||
		(f.TargetSessionID != "" && f.After == nil) || (f.Applied && (f.TargetSessionID == "" || f.Abandoned)) ||
		(f.Reason != "" && (!boundedID(f.Reason, 240) || redact.Text(f.Reason) != f.Reason)) || (f.Abandoned && f.Reason == "") {
		return Frontier{}, ErrInvalid
	}
	if f.MessageEnd != nil {
		end := *f.MessageEnd
		f.MessageEnd = &end
	}
	if f.After != nil {
		end := *f.After
		f.After = &end
	}
	if f.Source.Task != nil {
		task := *f.Source.Task
		f.Source.Task = &task
	}
	return f, nil
}

// Status reports the recovery stage without exposing conversation or tool contents.
func (f Frontier) Status() string {
	switch {
	case f.Abandoned:
		return "abandoned"
	case f.Applied:
		return "applied"
	case f.TargetSessionID != "":
		return "bound"
	case f.After != nil:
		return "ended"
	default:
		return "started"
	}
}

// End freezes the first observed end; callback retries cannot extend a finished interval.
func (f *Frontier) End(after string) error {
	if !validHead(after) {
		return ErrInvalid
	}
	if f.Abandoned {
		return ErrConflict
	}
	if f.After == nil {
		f.After = &after
	}
	return nil
}

// Resolve explicitly supplies a previously unobserved end and an immutable operator reason.
func (f *Frontier) Resolve(after, reason string) error {
	reason = redact.Text(reason)
	if !capture.ValidCommit(after) || !boundedID(reason, 240) {
		return ErrInvalid
	}
	if f.After != nil {
		if *f.After == after && f.Reason == reason && !f.Abandoned {
			return nil
		}
		return ErrConflict
	}
	if err := f.End(after); err != nil {
		return err
	}
	f.Reason = reason
	return nil
}

// Bind pins the exact segment accepted with this frontier in the same ledger transaction.
func (f *Frontier) Bind(id string) error {
	if !identifier.Valid(id) {
		return ErrInvalid
	}
	if f.After == nil || f.Abandoned || (f.TargetSessionID != "" && f.TargetSessionID != id) {
		return ErrConflict
	}
	f.TargetSessionID = id
	return nil
}

// Apply records successful atomic association without granting publication consent.
func (f *Frontier) Apply() error {
	if f.TargetSessionID == "" || f.After == nil || f.Abandoned {
		return ErrConflict
	}
	f.Applied = true
	return nil
}

// Abandon closes an unresolved association while retaining replay identity and existing evidence.
func (f *Frontier) Abandon(reason string) error {
	reason = redact.Text(reason)
	if !boundedID(reason, 240) {
		return ErrInvalid
	}
	if f.Applied || (f.Abandoned && f.Reason != reason) {
		return ErrConflict
	}
	f.Abandoned, f.Reason = true, reason
	return nil
}

func validHead(sha string) bool { return sha == "" || capture.ValidCommit(sha) }

// ValidFrontierID accepts only the opaque journal identifier, never a filesystem path or URL.
func ValidFrontierID(id string) bool { return frontierKey.MatchString(id) }
