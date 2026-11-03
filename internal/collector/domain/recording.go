// Package domain defines local observation and recording invariants without storage or Git I/O.
package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	capture "workgraph.local/platform/internal/capture/domain"
)

var (
	// ErrInvalid identifies malformed local capture/configuration input.
	ErrInvalid = errors.New("invalid collector input")
	// ErrConflict identifies reused event identity or changed local installation identity.
	ErrConflict = errors.New("collector identity conflict")
	// ErrDisabled stops capture, recovery and delivery under current local consent.
	ErrDisabled = errors.New("local collection is disabled")
	// ErrNotInitialized means explicit repository setup is required.
	ErrNotInitialized = errors.New("collector is not initialized")
)

// Observation is a complete conversation at an adapter boundary; ChangedFiles describes this turn.
type Observation struct {
	SchemaVersion   int                   `json:"schema_version"`
	SourceSessionID string                `json:"source_session_id"`
	EventID         string                `json:"event_id"`
	Agent           capture.Agent         `json:"agent"`
	StartedAt       *time.Time            `json:"started_at,omitempty"`
	EndedAt         *time.Time            `json:"ended_at,omitempty"`
	Messages        []capture.Message     `json:"messages"`
	ChangedFiles    []string              `json:"changed_files"`
	FileEvents      []FileEvent           `json:"file_events,omitempty"`
	History         *NativeHistory        `json:"native_history,omitempty"`
	Origin          *capture.ImportOrigin `json:"origin,omitempty"`
}

// FileEvent identifies an immutable native tool/path observation so history replays are harmless.
type FileEvent struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

// Key stores only a scoped hash of the native file event identity.
func (e FileEvent) Key() string { return hash([]byte("file\x00" + e.ID + "\x00" + e.Path)) }

// NormalizedObservation owns a sanitized bounded observation and stable replay digest.
type NormalizedObservation struct {
	value     Observation
	canonical []byte
	digest    string
}

// Normalize rejects unbounded input and applies the shared capture redaction contract.
func Normalize(input Observation) (NormalizedObservation, error) {
	if input.SchemaVersion != 1 || !boundedID(input.SourceSessionID, 200) || !boundedID(input.EventID, 200) {
		return NormalizedObservation{}, ErrInvalid
	}
	if len(input.FileEvents) > 5000 {
		return NormalizedObservation{}, ErrInvalid
	}
	if input.Origin != nil && len(input.FileEvents) > 0 {
		return NormalizedObservation{}, ErrInvalid
	}
	if input.History != nil && input.History.validate(input.Messages) != nil {
		return NormalizedObservation{}, ErrInvalid
	}
	paths := slices.Clone(input.ChangedFiles)
	for _, event := range input.FileEvents {
		if !boundedID(event.ID, 200) {
			return NormalizedObservation{}, ErrInvalid
		}
		paths = append(paths, event.Path)
	}
	started := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	if input.StartedAt != nil {
		started = *input.StartedAt
	}
	// Fixed opaque IDs validate the observation's common fields; real repository/session identity is assigned by the collector.
	session, err := capture.Sanitize(capture.Snapshot{SchemaVersion: 1, RepositoryID: "11111111-1111-4111-8111-111111111111", SessionID: "22222222-2222-4222-8222-222222222222", Revision: 1, Agent: input.Agent, StartedAt: started, EndedAt: input.EndedAt, Messages: input.Messages, Files: input.ChangedFiles, Origin: input.Origin, Commits: originCommits(input.Origin)})
	if err != nil {
		return NormalizedObservation{}, ErrInvalid
	}
	snapshot := session.Snapshot()
	validation := snapshot
	validation.Files = unique(paths)
	if _, err := capture.NewSession(validation); err != nil {
		return NormalizedObservation{}, ErrInvalid
	}
	input.Agent = snapshot.Agent
	input.Messages = snapshot.Messages
	input.ChangedFiles = snapshot.Files
	input.Origin = snapshot.Origin
	if input.StartedAt != nil {
		utc := input.StartedAt.UTC()
		input.StartedAt = &utc
	}
	input.EndedAt = snapshot.EndedAt
	canonical, err := json.Marshal(input)
	if err != nil || len(canonical) > 3*1024*1024 {
		return NormalizedObservation{}, ErrInvalid
	}
	sum := sha256.Sum256(canonical)
	return NormalizedObservation{value: input, canonical: canonical, digest: hex.EncodeToString(sum[:])}, nil
}

// Value returns a detached observation.
func (o NormalizedObservation) Value() Observation {
	var value Observation
	_ = json.Unmarshal(o.canonical, &value)
	return value
}

// CanonicalJSON returns detached sanitized adapter bytes.
func (o NormalizedObservation) CanonicalJSON() []byte { return slices.Clone(o.canonical) }

// Digest identifies replay content; raw native IDs are not needed in persistent event keys.
func (o NormalizedObservation) Digest() string { return o.digest }

// EventKey identifies the event within a recording without persisting an arbitrary raw event ID.
func (o NormalizedObservation) EventKey() string { return hash([]byte(o.value.EventID)) }

// SessionKey scopes native agent identity by repository, worktree and any immutable legacy namespace.
func SessionKey(repository, worktree string, task *capture.TaskRef, o NormalizedObservation) string {
	return NativeSessionKey(repository, worktree, o.value.Agent.Name, o.value.SourceSessionID, task)
}

// NativeSessionKey retains the existing mapping encoding before any observation is available.
func NativeSessionKey(repository, worktree, agent, native string, task *capture.TaskRef) string {
	bytes, _ := json.Marshal(struct {
		Repository, Worktree, Agent, Native string
		Task                                *capture.TaskRef
	}{repository, worktree, agent, native, task})
	return hash(bytes)
}

// State is the versioned persistence DTO for a current recording, not a server revision.
type State struct {
	Version          int              `json:"version"`
	Worktree         string           `json:"worktree"`
	Snapshot         capture.Snapshot `json:"snapshot"`
	PendingFiles     []string         `json:"pending_files"`
	LastRevision     int64            `json:"last_revision"`
	Dirty            bool             `json:"dirty"`
	CommitBoundary   *int             `json:"commit_boundary,omitempty"`
	NativeMessageIDs []string         `json:"native_message_ids,omitempty"`
}

// Recording separates current local observation from explicitly sealed immutable evidence.
type Recording struct{ state State }

// NewRecording binds a native observation to a new scoped Workgraph session.
func NewRecording(repository, id, worktree string, task *capture.TaskRef, o NormalizedObservation, now time.Time) (*Recording, error) {
	input := o.Value()
	started := now.UTC()
	if input.StartedAt != nil {
		started = *input.StartedAt
	}
	session, err := capture.Sanitize(capture.Snapshot{SchemaVersion: 1, RepositoryID: repository, SessionID: id, Revision: 1, Agent: input.Agent, StartedAt: started, EndedAt: input.EndedAt, Task: task, Messages: input.Messages, Files: input.ChangedFiles, Origin: input.Origin, Commits: originCommits(input.Origin)})
	if err != nil || !boundedID(worktree, 256) {
		return nil, ErrInvalid
	}
	state := State{Version: 1, Worktree: worktree, Snapshot: session.Snapshot(), PendingFiles: slices.Clone(input.ChangedFiles), Dirty: true}
	if input.Origin != nil {
		state.PendingFiles = []string{}
	}
	if input.History != nil {
		state.NativeMessageIDs = slices.Clone(input.History.MessageIDs)
	}
	return &Recording{state: state}, nil
}

// Restore validates stored recording bounds and consistency before exposing an aggregate.
func Restore(state State) (*Recording, error) {
	if state.CommitBoundary == nil && len(state.Snapshot.Commits) > 0 {
		// Old broad associations have no recoverable cursor. Future capture starts
		// after the known legacy observation instead of assigning it to a new commit.
		boundary := len(state.Snapshot.Messages)
		state.CommitBoundary = &boundary
	}
	if state.CommitBoundary != nil && (*state.CommitBoundary < 0 || *state.CommitBoundary > len(state.Snapshot.Messages)) {
		return nil, ErrInvalid
	}
	if state.Version != 1 || state.LastRevision < 0 || state.LastRevision > 1_000_000_000 || !boundedID(state.Worktree, 256) {
		return nil, ErrInvalid
	}
	if state.Snapshot.Revision != max(int64(1), state.LastRevision) {
		return nil, ErrInvalid
	}
	if len(state.NativeMessageIDs) > 0 {
		if len(state.NativeMessageIDs) != len(state.Snapshot.Messages) {
			return nil, ErrInvalid
		}
		for _, id := range state.NativeMessageIDs {
			if !nativeDigest(id) {
				return nil, ErrInvalid
			}
		}
	}
	session, err := capture.NewSession(state.Snapshot)
	if err != nil {
		return nil, ErrInvalid
	}
	for _, file := range state.PendingFiles {
		if !slices.Contains(state.Snapshot.Files, file) {
			return nil, ErrInvalid
		}
	}
	if len(state.PendingFiles) > len(state.Snapshot.Files) {
		return nil, ErrInvalid
	}
	if state.Snapshot.Origin != nil && len(state.PendingFiles) > 0 {
		return nil, ErrInvalid
	}
	state.Snapshot = session.Snapshot()
	state.PendingFiles = unique(state.PendingFiles)
	state.NativeMessageIDs = slices.Clone(state.NativeMessageIDs)
	return &Recording{state: state}, nil
}

// State returns detached persistence data.
func (r *Recording) State() State {
	state := r.state
	if state.CommitBoundary != nil {
		boundary := *state.CommitBoundary
		state.CommitBoundary = &boundary
	}
	session, _ := capture.NewSession(state.Snapshot)
	state.Snapshot = session.Snapshot()
	state.PendingFiles = slices.Clone(state.PendingFiles)
	state.NativeMessageIDs = slices.Clone(state.NativeMessageIDs)
	return state
}

// Observe replaces the complete conversation and accumulates newly observed changed paths.
func (r *Recording) Observe(o NormalizedObservation) error {
	input := o.Value()
	current := r.state.Snapshot
	before, err := capture.NewSession(current)
	if err != nil {
		return ErrInvalid
	}
	if input.Agent.Name != current.Agent.Name || (input.StartedAt != nil && !input.StartedAt.Equal(current.StartedAt)) {
		return ErrConflict
	}
	if !reflect.DeepEqual(input.Origin, current.Origin) {
		return ErrConflict
	}
	if input.Origin != nil && (!capture.MessagesEqual(input.Messages, current.Messages) || !slices.Equal(input.ChangedFiles, current.Files) || input.Agent != current.Agent) {
		return ErrConflict
	}
	if len(input.Messages) < len(current.Messages) {
		return ErrConflict
	}
	for i, message := range current.Messages {
		if !input.Messages[i].Enriches(message) {
			return ErrConflict
		}
	}
	current.Agent = input.Agent
	current.Messages = input.Messages
	current.EndedAt = input.EndedAt
	current.Files = unique(append(current.Files, input.ChangedFiles...))
	session, err := capture.Sanitize(current)
	if err != nil {
		return ErrInvalid
	}
	r.state.Snapshot = session.Snapshot()
	if input.Origin == nil {
		r.state.PendingFiles = unique(append(r.state.PendingFiles, input.ChangedFiles...))
	}
	r.state.Dirty = r.state.Dirty || session.Digest() != before.Digest()
	return nil
}

// Associate links observed paths to an actual commit and retains partially committed paths.
func (r *Recording) Associate(sha string, changed, remaining []string) (bool, error) {
	if !capture.ValidCommit(sha) {
		return false, ErrInvalid
	}
	matched := false
	pending := []string{}
	for _, file := range r.state.PendingFiles {
		if slices.Contains(changed, file) {
			matched = true
			if slices.Contains(remaining, file) {
				pending = append(pending, file)
			}
		} else {
			pending = append(pending, file)
		}
	}
	if !matched {
		return false, nil
	}
	candidate := r.State().Snapshot
	rangeCount := len(candidate.CommitRanges)
	r.addCommitRange(&candidate, sha, "observed_checkpoint")
	if len(candidate.CommitRanges) == rangeCount && !slices.Contains(candidate.Commits, sha) {
		r.state.PendingFiles = pending
		return false, nil
	}
	if !slices.Contains(candidate.Commits, sha) {
		candidate.Commits = append(candidate.Commits, sha)
	}
	validated, err := capture.NewSession(candidate)
	if err != nil {
		return false, ErrInvalid
	}
	before, _ := capture.NewSession(r.state.Snapshot)
	if before.Digest() != validated.Digest() {
		r.state.Snapshot = validated.Snapshot()
		r.state.Dirty = true
	}
	r.state.PendingFiles = pending
	boundary := len(r.state.Snapshot.Messages)
	r.state.CommitBoundary = &boundary
	return true, nil
}

// LinkObservedCommits records a native tool's observed HEAD transition as context, not line authorship.
func (r *Recording) LinkObservedCommits(commits []string) (bool, error) {
	candidate := r.State().Snapshot
	for _, sha := range commits {
		if !capture.ValidCommit(sha) {
			return false, ErrInvalid
		}
		if !slices.Contains(candidate.Commits, sha) {
			candidate.Commits = append(candidate.Commits, sha)
		}
	}
	validated, err := capture.NewSession(candidate)
	if err != nil {
		return false, ErrInvalid
	}
	changed := !slices.Equal(candidate.Commits, r.state.Snapshot.Commits)
	if changed {
		r.state.Snapshot = validated.Snapshot()
		r.state.Dirty = true
	}
	return changed, nil
}

// Rewrite retains source commits while adding explicit Git-provided replacement identities.
func (r *Recording) Rewrite(mapping map[string]string) (bool, error) {
	for old, replacement := range mapping {
		if !capture.ValidCommit(old) || !capture.ValidCommit(replacement) {
			return false, ErrInvalid
		}
	}
	changed := false
	candidate := r.State().Snapshot
	for _, old := range slices.Clone(candidate.Commits) {
		if replacement, ok := mapping[old]; ok && !slices.Contains(candidate.Commits, replacement) {
			candidate.Commits = append(candidate.Commits, replacement)
			changed = true
		}
	}
	for _, interval := range slices.Clone(candidate.CommitRanges) {
		replacement, ok := mapping[interval.CommitSHA]
		if !ok || replacement == interval.CommitSHA {
			continue
		}
		exists := false
		for _, prior := range candidate.CommitRanges {
			if prior.CommitSHA == replacement && prior.Start == interval.Start && prior.End == interval.End {
				exists = true
				break
			}
		}
		if !exists {
			interval.CommitSHA = replacement
			interval.Basis = "carry_forward"
			candidate.CommitRanges = append(candidate.CommitRanges, interval)
			changed = true
		}
	}
	if changed {
		validated, err := capture.NewSession(candidate)
		if err != nil {
			return false, ErrInvalid
		}
		r.state.Snapshot = validated.Snapshot()
		r.state.Dirty = true
	}
	return changed, nil
}

// Seal creates the next immutable revision only if current evidence has changed.
func (r *Recording) Seal() (capture.Session, bool, error) {
	if !r.state.Dirty && r.state.LastRevision > 0 {
		return capture.Session{}, false, nil
	}
	snapshot := r.state.Snapshot
	snapshot.Revision = r.state.LastRevision + 1
	session, err := capture.Sanitize(snapshot)
	if err != nil {
		return capture.Session{}, false, err
	}
	r.state.Snapshot = session.Snapshot()
	r.state.LastRevision = snapshot.Revision
	r.state.Dirty = false
	return session, true, nil
}

func boundedID(value string, max int) bool {
	return len(value) > 0 && len(value) <= max && utf8.ValidString(value) && !strings.ContainsAny(value, "\x00\r\n")
}
func hash(value []byte) string { sum := sha256.Sum256(value); return hex.EncodeToString(sum[:]) }
func originCommits(origin *capture.ImportOrigin) []string {
	if origin == nil {
		return nil
	}
	return []string{origin.TargetCommit}
}
func unique(values []string) []string {
	copy := append([]string{}, values...)
	slices.Sort(copy)
	return slices.Compact(copy)
}

// LinkCommitCheckpointAt attaches the not-yet-committed interval ending at the observed tool boundary.
func (r *Recording) LinkCommitCheckpointAt(commits []string, end int) (bool, error) {
	if end < 0 || end > len(r.state.Snapshot.Messages) {
		return false, ErrInvalid
	}
	start := 0
	if r.state.CommitBoundary != nil {
		start = *r.state.CommitBoundary
	}
	if end <= start {
		return false, nil
	}
	candidate := r.State().Snapshot
	before, _ := capture.NewSession(candidate)
	for _, sha := range commits {
		if !capture.ValidCommit(sha) {
			return false, ErrInvalid
		}
		if !slices.Contains(candidate.Commits, sha) {
			candidate.Commits = append(candidate.Commits, sha)
		}
		r.addCommitRangeAt(&candidate, sha, "observed_checkpoint", end)
	}
	after, err := capture.NewSession(candidate)
	if err != nil {
		return false, err
	}
	boundary := end
	r.state.CommitBoundary = &boundary
	changed := before.Digest() != after.Digest()
	if changed {
		r.state.Snapshot = after.Snapshot()
		r.state.Dirty = true
	}
	return changed, nil
}
func (r *Recording) addCommitRange(candidate *capture.Snapshot, sha, basis string) {
	r.addCommitRangeAt(candidate, sha, basis, len(candidate.Messages))
}
func (r *Recording) addCommitRangeAt(candidate *capture.Snapshot, sha, basis string, end int) {
	for _, existing := range candidate.CommitRanges {
		if existing.CommitSHA == sha && existing.End >= end {
			return
		}
	}
	start := 0
	if r.state.CommitBoundary != nil {
		start = *r.state.CommitBoundary
	}
	if start >= end {
		if start != end || len(candidate.CommitRanges) == 0 {
			return
		}
		prior := candidate.CommitRanges[len(candidate.CommitRanges)-1]
		if prior.End != end {
			return
		}
		start = prior.Start
		basis = "carry_forward"
	}
	candidate.CommitRanges = append(candidate.CommitRanges, capture.CommitRange{CommitSHA: sha, Start: start, End: end, Basis: basis})
}

// ReplaceRecoveredRanges rebuilds only recovery assertions; observed/carry-forward
// checkpoints and old immutable revisions retain their original authority.
func (r *Recording) ReplaceRecoveredRanges(ranges []capture.CommitRange) (bool, int, error) {
	snapshot := r.State().Snapshot
	retained := []capture.CommitRange{}
	priority := map[string]bool{}
	for _, prior := range snapshot.CommitRanges {
		if prior.Basis != "recovered_git_output" {
			retained = append(retained, prior)
			priority[prior.CommitSHA] = true
		}
	}
	count := 0
	for _, interval := range ranges {
		if interval.Basis != "recovered_git_output" {
			return false, 0, ErrInvalid
		}
		if !priority[interval.CommitSHA] {
			retained = append(retained, interval)
			count++
		}
	}
	snapshot.CommitRanges = retained
	preserveCursor := r.state.CommitBoundary != nil

	for _, interval := range ranges {
		if !slices.Contains(snapshot.Commits, interval.CommitSHA) {
			snapshot.Commits = append(snapshot.Commits, interval.CommitSHA)
		}
	}
	session, err := capture.NewSession(snapshot)
	if err != nil {
		return false, 0, err
	}
	current, _ := capture.NewSession(r.state.Snapshot)
	changed := session.Digest() != current.Digest()
	if changed {
		r.state.Snapshot = session.Snapshot()
		r.state.Dirty = true
	}
	boundary := 0
	for _, interval := range snapshot.CommitRanges {
		boundary = max(boundary, interval.End)
	}
	if preserveCursor && r.state.CommitBoundary != nil {
		boundary = max(boundary, *r.state.CommitBoundary)
	}
	r.state.CommitBoundary = &boundary
	covered := 0
	for _, interval := range snapshot.CommitRanges {
		covered = max(covered, interval.End)
	}
	if covered == len(snapshot.Messages) {
		r.state.PendingFiles = []string{}
	}
	return changed, count, nil
}
