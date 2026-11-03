package domain

import (
	"encoding/json"
	"math"
	"slices"
	"strings"
	"time"
	"workgraph.local/platform/internal/identifier"
)

// Metadata records producer-reported context. Only message metadata names an executing model;
// a session configuration and an imported attribution report are distinct, weaker observations.
type Metadata struct {
	Agent             string `json:"agent,omitempty"`
	Version           string `json:"version,omitempty"`
	Model             string `json:"model,omitempty"`
	Provider          string `json:"provider,omitempty"`
	ModelBasis        string `json:"model_basis,omitempty"`
	Profile           string `json:"profile,omitempty"`
	Role              string `json:"role,omitempty"`
	Tool              string `json:"tool,omitempty"`
	NativeID          string `json:"native_id,omitempty"`
	TurnID            string `json:"turn_id,omitempty"`
	Relation          string `json:"relation,omitempty"`
	ReportedLineStart int    `json:"reported_line_start,omitempty"`
	ReportedLineEnd   int    `json:"reported_line_end,omitempty"`
	AttributionKind   string `json:"attribution_kind,omitempty"`
	ReportedCommit    string `json:"reported_commit,omitempty"`
}

// Validate bounds derived metadata independently of its untrusted producer.
func (m Metadata) Validate() error {
	if m.ReportedCommit != "" && !identifier.ValidCommit(m.ReportedCommit) {
		return ErrInvalid
	}
	for _, v := range []struct {
		s string
		n int
	}{{m.Agent, 120}, {m.Version, 120}, {m.Model, 240}, {m.Provider, 120}, {m.Profile, 120}, {m.Role, 40}, {m.Tool, 160}, {m.NativeID, 200}, {m.TurnID, 200}} {
		if !validText(v.s, v.n, false) {
			return ErrInvalid
		}
	}
	if !slices.Contains([]string{"", "ai", "known_human"}, m.AttributionKind) || !slices.Contains([]string{"", "message_metadata", "session_configuration", "import_report"}, m.ModelBasis) || !slices.Contains([]string{"", "message", "tool_reported", "session_reported", "git_ai_reported", "retained_code"}, m.Relation) || m.ReportedLineStart < 0 || m.ReportedLineEnd < m.ReportedLineStart || m.ReportedLineEnd > 1_000_000_000 {
		return ErrInvalid
	}
	return nil
}

// SearchCursor pins the query and indexing cutoff, with a stable keyset instead of growing OFFSET.
// Current source permissions and availability are still rechecked on every page.
type SearchCursor struct {
	ResolvedCommitSHA string    `json:"resolved_commit_sha,omitempty"`
	Literal           bool      `json:"literal,omitempty"`
	QueryDigest       string    `json:"query_digest"`
	IndexedBefore     time.Time `json:"indexed_before"`
	Score             float64   `json:"score"`
	ObservedAt        time.Time `json:"observed_at"`
	ID                string    `json:"id"`
}

// Fingerprint binds continuation to normalized selectors, excluding page size and server read time.
func (q Query) Fingerprint() string {
	if q.CommitPrefix != "" {
		q.CommitSHA = ""
	}
	q.After = nil
	q.Limit = 0
	q.Terms = ""
	q.ReadAt = time.Time{}
	raw, _ := json.Marshal(q)
	return Hash(raw)
}

// Validate rejects cross-query cursors and incompatible prefix/literal modes before storage work.
func (c SearchCursor) Validate(q Query) error {
	if c.Literal && (q.PrefixText == "" || c.ResolvedCommitSHA != "") {
		return ErrInvalid
	}
	if c.ResolvedCommitSHA != "" && (q.CommitPrefix == "" || !identifier.ValidCommit(c.ResolvedCommitSHA) || !strings.HasPrefix(c.ResolvedCommitSHA, q.CommitPrefix)) {
		return ErrInvalid
	}
	if q.CommitPrefix != "" && !c.Literal && c.ResolvedCommitSHA == "" {
		return ErrInvalid
	}

	if !Digest(c.ID) || c.QueryDigest != q.Fingerprint() || c.IndexedBefore.IsZero() || c.ObservedAt.IsZero() || math.IsNaN(c.Score) || math.IsInf(c.Score, 0) || c.Score < 0 {
		return ErrInvalid
	}
	return nil
}

func cleanFilter(value string, bound int) (string, error) {
	value = strings.TrimSpace(value)
	if !validText(value, bound, false) {
		return "", ErrInvalid
	}
	return strings.ToLower(value), nil
}
