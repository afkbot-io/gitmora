// Package domain defines compact cross-source context with explicit authority, gaps and immutable references.
package domain

import (
	"errors"
	"strings"
	"time"
	capture "workgraph.local/platform/internal/capture/domain"

	forge "workgraph.local/platform/internal/forge/domain"
	"workgraph.local/platform/internal/identifier"
	retrieval "workgraph.local/platform/internal/retrieval/domain"
)

var ErrInvalid = errors.New("invalid context pack query")

// Cursor continues a stable capture ordering; it grants no authority and is reapplied inside the full scope filter.
type Cursor struct {
	RepositoryID string `json:"repository_id"`
	Segment      int64  `json:"segment"`
	SessionID    string `json:"session_id"`
}

// MRCursor navigates discovered provider associations independently of capture pagination.
type MRCursor struct {
	RepositoryID string `json:"repository_id"`
	IID          int64  `json:"iid"`
}

// Query selects an exact capture stream, confirmed commit or explicit MR in one local project.
type Query struct {
	MRReadKey          string         `json:"-"`
	MRTargetRepository string         `json:"-"`
	MRCodeKey          string         `json:"-"`
	MRSourceProject    int64          `json:"-"`
	ProjectID          string         `json:"project_id"`
	DialogueID         string         `json:"dialogue_id,omitempty"`
	MessageAfter       *MessageCursor `json:"message_after,omitempty"`
	RepositoryID       string         `json:"repository_id,omitempty"`
	SessionID          string         `json:"session_id,omitempty"`
	CommitSHA          string         `json:"commit_sha,omitempty"`
	MR                 int64          `json:"merge_request_iid,omitempty"`
	ExpectedHead       string         `json:"expected_head_sha,omitempty"`
	Limit              int            `json:"limit,omitempty"`
	After              *Cursor        `json:"capture_after,omitempty"`
	MRAfter            *MRCursor      `json:"merge_requests_after,omitempty"`
}

// Normalize owns cursor values and rejects ambiguous selectors before any source query.
func Normalize(q Query) (Query, error) {
	if (q.MessageAfter != nil && q.SessionID == "" && q.DialogueID == "") || (q.MessageAfter != nil && q.MR > 0 && (q.MessageAfter.Head == "" || q.MessageAfter.ScopeKey == "")) || (q.DialogueID != "" && (!identifier.Valid(q.DialogueID) || q.SessionID != "")) || (q.MessageAfter != nil && q.MessageAfter.Validate() != nil) || q.MRReadKey != "" || q.MRTargetRepository != "" || q.MRCodeKey != "" || q.MRSourceProject != 0 || !identifier.Valid(q.ProjectID) || q.RepositoryID != "" && !identifier.Valid(q.RepositoryID) || q.SessionID != "" && (!identifier.Valid(q.SessionID) || q.RepositoryID == "") || q.CommitSHA != "" && (!identifier.ValidCommit(q.CommitSHA) || q.RepositoryID == "") || q.MR < 0 || q.MR > 0 && q.RepositoryID == "" || q.ExpectedHead != "" && (!identifier.ValidCommit(q.ExpectedHead) || q.MR == 0) || q.Limit < 0 || q.Limit > 12 {
		return Query{}, ErrInvalid
	}
	selectors := 0
	for _, selected := range []bool{q.SessionID != "", q.CommitSHA != "", q.MR > 0} {
		if selected {
			selectors++
		}
	}
	if selectors != 1 {
		return Query{}, ErrInvalid
	}
	if q.After != nil && (!identifier.Valid(q.After.RepositoryID) || !identifier.Valid(q.After.SessionID) || q.After.Segment < 1 || q.After.Segment > 1_000_000_000) {
		return Query{}, ErrInvalid
	}
	if q.MRAfter != nil && (q.MR > 0 || !identifier.Valid(q.MRAfter.RepositoryID) || q.MRAfter.IID < 1) {
		return Query{}, ErrInvalid
	}
	if q.Limit == 0 {
		q.Limit = 6
	}
	if q.After != nil {
		owned := *q.After
		q.After = &owned
	}
	if q.MessageAfter != nil {
		owned := *q.MessageAfter
		q.MessageAfter = &owned
	}
	if q.MRAfter != nil {
		owned := *q.MRAfter
		q.MRAfter = &owned
	}
	return q, nil
}

// Capture identifies one latest segment revision without claiming that it is the newest native conversation window.
type Capture struct {
	RangesMore              bool                  `json:"ranges_more,omitempty"`
	MessageRanges           []capture.CommitRange `json:"message_ranges,omitempty"`
	RangeStatus             string                `json:"range_status,omitempty"`
	Source                  retrieval.Source      `json:"source"`
	RootSessionID           string                `json:"root_session_id"`
	Segment                 int64                 `json:"segment"`
	Agent                   string                `json:"agent"`
	Title                   string                `json:"title,omitempty"`
	StartedAt               string                `json:"started_at,omitempty"`
	ParentReferenceVerified bool                  `json:"parent_reference_verified"`
	ChainVerified           bool                  `json:"chain_verified"`
	VerificationLimited     bool                  `json:"chain_verification_limited"`
	ConfirmedCommits        []string              `json:"confirmed_commits"`
	ConfirmedCount          int                   `json:"confirmed_count"`
	Continuation            *capture.Continuation `json:"continuation,omitempty"`
}

// CapturePage gives explicit navigation when the selected stream has more windows.
type CapturePage struct {
	Items   []Capture `json:"items"`
	Next    *Cursor   `json:"next,omitempty"`
	Omitted bool      `json:"omitted_for_permissions"`
}

// Excerpt is bounded metadata, independent of the exact chunk citations returned in Evidence.
type Excerpt struct {
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"`
}

// Clip counts Unicode code points and preserves whether source text was omitted.
func Clip(s string, n int) Excerpt {
	r := []rune(s)
	return Excerpt{string(r[:min(n, len(r))]), len(r) > n}
}

// Pipeline is scoped to an exact provider project and immutable tested SHA.
type Pipeline struct {
	forge.Pipeline
	MatchesObservedHead bool `json:"matches_observed_head"`
	MatchesExpectedHead bool `json:"matches_expected_head"`
}

// PipelineView compares observed and requested heads independently without changing tested identity.
func PipelineView(p forge.Pipeline, m forge.MergeRequest, expected string) Pipeline {
	return Pipeline{p, p.AppliesTo(m), expected != "" && p.AppliesTo(m) && p.SHA == expected}
}

// MR preserves independent provider observation and code identities; CI is never inherited across heads.
type MR struct {
	SourceCommits       []string           `json:"source_commits"`
	SourceCommitCount   int                `json:"source_commit_count"`
	SourceCommitsMore   bool               `json:"source_commits_more"`
	Association         string             `json:"association_basis"`
	RepositoryID        string             `json:"repository_id"`
	IID                 int64              `json:"iid"`
	Observation         string             `json:"observation"`
	CodeKey             string             `json:"code_key"`
	ObservedAt          time.Time          `json:"observed_at"`
	ProviderUpdatedAt   time.Time          `json:"provider_updated_at"`
	Title               string             `json:"title"`
	Description         Excerpt            `json:"description"`
	URL                 string             `json:"url"`
	State               string             `json:"state"`
	Head                string             `json:"observed_head_sha"`
	SourceProjectID     int64              `json:"source_project_id"`
	SourceRepositoryID  string             `json:"source_repository_id,omitempty"`
	CommitsComplete     bool               `json:"source_commits_complete"`
	ResultSHAs          []string           `json:"result_shas"`
	ExpectedHeadMatches *bool              `json:"expected_head_matches,omitempty"`
	Pipelines           []Pipeline         `json:"pipelines_for_observed_head"`
	PipelinesMore       bool               `json:"pipelines_have_more"`
	Deployments         []forge.Deployment `json:"deployments_for_result_or_head"`
	DeploymentsMore     bool               `json:"deployments_have_more"`
}

// MRPage navigates discovered associations independently of native-window pagination.
type MRPage struct {
	Items   []MR      `json:"items"`
	HasMore bool      `json:"has_more"`
	Omitted bool      `json:"omitted_for_permissions"`
	Next    *MRCursor `json:"next,omitempty"`
}

// Evidence adds a selection reason without inventing a segment's own commit relation.
type Evidence struct {
	retrieval.Hit
	Selection string `json:"selection"`
}

// Pack assembles cached observations. It is never approval, a coherent provider snapshot or a whole repository inventory.
type Pack struct {
	Transcript     MessagePage `json:"transcript"`
	Scope          Scope       `json:"scope"`
	SchemaVersion  int         `json:"schema_version"`
	Query          Query       `json:"query"`
	ReadAt         time.Time   `json:"read_at"`
	Captures       CapturePage `json:"captures"`
	MergeRequests  MRPage      `json:"merge_requests"`
	Evidence       []Evidence  `json:"evidence"`
	EvidenceMore   bool        `json:"evidence_has_more"`
	PendingSources int         `json:"pending_sources_in_project"`
	FailedSources  int         `json:"failed_sources_in_project"`
	BudgetLimited  bool        `json:"budget_limited"`
}

// MessageCursor pins immutable bytes and original positions across paged source reads.
type MessageCursor struct {
	ScopeKey     string `json:"scope_key,omitempty"`
	RuneOffset   int    `json:"rune_offset,omitempty"`
	RepositoryID string `json:"repository_id"`
	SessionID    string `json:"session_id"`
	Revision     int64  `json:"revision"`
	Digest       string `json:"digest"`
	Segment      int64  `json:"segment"`
	Offset       int    `json:"offset"`
	Head         string `json:"head,omitempty"`
}

func (c MessageCursor) Validate() error {
	if c.ScopeKey != "" {
		parts := strings.Split(c.ScopeKey, ":")
		if len(parts) != 2 || !retrieval.Digest(parts[0]) || parts[1] != "head" && parts[1] != "commits" {
			return ErrInvalid
		}
	}
	if !identifier.Valid(c.RepositoryID) || !identifier.Valid(c.SessionID) || c.Revision < 1 || c.Revision > 1_000_000_000 || !retrieval.Digest(c.Digest) || c.Segment < 1 || c.Segment > 1_000_000_000 || c.Offset < 0 || c.Offset > 5000 || c.RuneOffset < 0 || c.RuneOffset > 128*1024 || c.Head != "" && !identifier.ValidCommit(c.Head) {
		return ErrInvalid
	}
	return nil
}

type ScopedMessage struct {
	MessageOffset int              `json:"message_offset,omitempty"`
	TextTruncated bool             `json:"text_truncated,omitempty"`
	Source        retrieval.Source `json:"source"`
	Segment       int64            `json:"segment"`
	Index         int              `json:"index"`
	Message       capture.Message  `json:"message"`
	CommitSHAs    []string         `json:"commit_shas,omitempty"`
}
type MessagePage struct {
	Items   []ScopedMessage `json:"items"`
	Next    *MessageCursor  `json:"next,omitempty"`
	HasMore bool            `json:"has_more"`
}
type Scope struct {
	Kind                string `json:"kind"`
	MissingCommitRanges bool   `json:"missing_commit_ranges"`
}

func (m MR) MessageScopeKey() string {
	if m.CommitsComplete && m.SourceRepositoryID != "" {
		return m.CodeKey + ":commits"
	}
	return m.CodeKey + ":head"
}
