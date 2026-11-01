package domain

import (
	"encoding/hex"
	"slices"
	"strings"
	"time"

	capture "workgraph.local/platform/internal/capture/domain"
)

// NativeHistory describes one bounded stable window of exposed native parts. IDs are hashes.
type NativeHistory struct {
	PageID     string   `json:"page_id"`
	Epoch      string   `json:"epoch"`
	MessageIDs []string `json:"message_ids"`
	Pruned     []int    `json:"pruned,omitempty"`
	Reason     string   `json:"reason"`
}

func nativeDigest(s string) bool {
	_, err := hex.DecodeString(s)
	return len(s) == 64 && err == nil && s == strings.ToLower(s)
}
func (h NativeHistory) validate(messages []capture.Message) error {
	if !nativeDigest(h.PageID) || !nativeDigest(h.Epoch) || len(h.MessageIDs) != len(messages) || len(messages) > 1000 || !slices.Contains([]string{"window", "compaction"}, h.Reason) || len(h.Pruned) > len(messages) {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, id := range h.MessageIDs {
		if !nativeDigest(id) || seen[id] {
			return ErrInvalid
		}
		seen[id] = true
	}
	for _, i := range h.Pruned {
		if i < 0 || i >= len(messages) || messages[i].Role != "tool" {
			return ErrInvalid
		}
	}
	return nil
}

// ObserveNative preserves previously captured tool bytes when the native source only prunes output.
func (r *Recording) ObserveNative(o NormalizedObservation) error {
	value := o.Value()
	if value.History == nil {
		return ErrInvalid
	}
	prior := map[string]capture.Message{}
	for i, id := range r.state.NativeMessageIDs {
		prior[id] = r.state.Snapshot.Messages[i]
	}
	for _, i := range value.History.Pruned {
		if message, ok := prior[value.History.MessageIDs[i]]; ok {
			value.Messages[i] = message
		}
	}
	if len(value.Messages) < len(r.state.Snapshot.Messages) && slices.Equal(value.Messages, r.state.Snapshot.Messages[:len(value.Messages)]) {
		value.Messages = slices.Clone(r.state.Snapshot.Messages)
		value.History.MessageIDs = slices.Clone(r.state.NativeMessageIDs)
		value.Agent = r.state.Snapshot.Agent
	}
	effective, err := Normalize(value)
	if err != nil {
		return err
	}
	if err = r.Observe(effective); err != nil {
		return err
	}
	r.state.NativeMessageIDs = slices.Clone(value.History.MessageIDs)
	return nil
}

// ContinueRecording creates a bounded child of an already sealed exact prior observation.
func ContinueRecording(parent *Recording, rootID, id string, o NormalizedObservation, reason string, now time.Time) (*Recording, error) {
	p := parent.State()
	if p.LastRevision < 1 {
		return nil, ErrInvalid
	}
	value := o.Value()
	started := p.Snapshot.StartedAt
	value.StartedAt = &started
	input, err := Normalize(value)
	if err != nil {
		return nil, err
	}
	r, err := NewRecording(p.Snapshot.RepositoryID, id, p.Worktree, p.Snapshot.Task, input, now)
	if err != nil {
		return nil, err
	}
	previous, err := capture.NewSession(p.Snapshot)
	if err != nil {
		return nil, err
	}
	index := int64(2)
	if p.Snapshot.Continuation != nil {
		index = p.Snapshot.Continuation.Segment + 1
	}
	r.state.Snapshot.Continuation = &capture.Continuation{RootSessionID: rootID, PreviousSessionID: p.Snapshot.SessionID, PreviousRevision: p.LastRevision, PreviousDigest: previous.Digest(), Segment: index, Reason: reason}
	// A new storage window does not inherit earlier commit associations.
	if _, err = capture.NewSession(r.state.Snapshot); err != nil {
		return nil, ErrInvalid
	}
	return r, nil
}
