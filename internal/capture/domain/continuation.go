package domain

import (
	"encoding/hex"
	"slices"
	"strings"
)

// Continuation identifies an exact prior observation without replacing or trusting its text.
// Reasons describe collector segmentation, not semantic equivalence of agent summaries.
type Continuation struct {
	RootSessionID     string `json:"root_session_id"`
	PreviousSessionID string `json:"previous_session_id"`
	PreviousRevision  int64  `json:"previous_revision"`
	PreviousDigest    string `json:"previous_digest"`
	Segment           int64  `json:"segment"`
	Reason            string `json:"reason"`
}

// Validate bounds a child reference; source ownership and matching parent bytes are checked separately.
func (c Continuation) Validate(child string) error {
	_, err := hex.DecodeString(c.PreviousDigest)
	if !ValidID(c.RootSessionID) || !ValidID(c.PreviousSessionID) || c.RootSessionID == child || c.PreviousSessionID == child || c.PreviousRevision < 1 || c.PreviousRevision > 1_000_000_000 || c.Segment < 2 || c.Segment > 1_000_000_000 || len(c.PreviousDigest) != 64 || err != nil || c.PreviousDigest != strings.ToLower(c.PreviousDigest) || !slices.Contains([]string{"window", "compaction", "history_replacement", "protocol_upgrade"}, c.Reason) {
		return ErrInvalid
	}
	return nil
}

// MatchesParent checks this edge's exact bytes, sequence and immutable source/task scope.
// It does not attest the agent's summary, native chronological order or the entire ancestor chain.
func (s Session) MatchesParent(parent Session) bool {
	c := s.snapshot.Continuation
	p := parent.snapshot
	if c == nil || c.PreviousSessionID != p.SessionID || c.PreviousRevision != p.Revision || c.PreviousDigest != parent.Digest() || s.snapshot.RepositoryID != p.RepositoryID || s.snapshot.Agent.Name != p.Agent.Name || !s.snapshot.StartedAt.Equal(p.StartedAt) {
		return false
	}
	if (s.snapshot.Task == nil) != (p.Task == nil) || s.snapshot.Task != nil && *s.snapshot.Task != *p.Task {
		return false
	}
	if !SameImportSource(s.snapshot.Origin, p.Origin) {
		return false
	}
	if s.snapshot.Origin != nil && s.snapshot.Origin.Part != p.Origin.Part+1 {
		return false
	}
	root, index := p.SessionID, int64(1)
	if p.Continuation != nil {
		root, index = p.Continuation.RootSessionID, p.Continuation.Segment
	}
	return c.RootSessionID == root && c.Segment == index+1
}
