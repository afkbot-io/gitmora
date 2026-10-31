// Package domain defines compact cross-source context with explicit authority, gaps and immutable references.
package domain

import (
	"cmp"
	"errors"
	"slices"
	"strings"
	"time"
	capture "workgraph.local/platform/internal/capture/domain"

	forge "workgraph.local/platform/internal/forge/domain"
	"workgraph.local/platform/internal/identifier"
	retrieval "workgraph.local/platform/internal/retrieval/domain"
	work "workgraph.local/platform/internal/work/domain"
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

// Query selects a task, exact capture stream, confirmed commit or explicit MR in one local project.
type Query struct {
	ProjectID    string    `json:"project_id"`
	RepositoryID string    `json:"repository_id,omitempty"`
	CountryID    string    `json:"country_id,omitempty"`
	TaskID       string    `json:"task_id,omitempty"`
	SessionID    string    `json:"session_id,omitempty"`
	CommitSHA    string    `json:"commit_sha,omitempty"`
	MR           int64     `json:"merge_request_iid,omitempty"`
	ExpectedHead string    `json:"expected_head_sha,omitempty"`
	Limit        int       `json:"limit,omitempty"`
	After        *Cursor   `json:"capture_after,omitempty"`
	MRAfter      *MRCursor `json:"merge_requests_after,omitempty"`
}

// Normalize owns cursor values and rejects ambiguous selectors before any source query.
func Normalize(q Query) (Query, error) {
	if !identifier.Valid(q.ProjectID) || q.RepositoryID != "" && !identifier.Valid(q.RepositoryID) || (q.CountryID == "") != (q.TaskID == "") || q.CountryID != "" && (!identifier.Valid(q.CountryID) || !identifier.Valid(q.TaskID)) || q.SessionID != "" && (!identifier.Valid(q.SessionID) || q.RepositoryID == "") || q.CommitSHA != "" && (!identifier.ValidCommit(q.CommitSHA) || q.RepositoryID == "") || q.MR < 0 || q.MR > 0 && q.RepositoryID == "" || q.ExpectedHead != "" && (!identifier.ValidCommit(q.ExpectedHead) || q.MR == 0) || q.Limit < 0 || q.Limit > 12 {
		return Query{}, ErrInvalid
	}
	selectors := 0
	for _, selected := range []bool{q.TaskID != "", q.SessionID != "", q.CommitSHA != "", q.MR > 0} {
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
	if q.MRAfter != nil {
		owned := *q.MRAfter
		q.MRAfter = &owned
	}
	return q, nil
}

// Capture identifies one latest segment revision without claiming that it is the newest native conversation window.
type Capture struct {
	Source                  retrieval.Source      `json:"source"`
	RootSessionID           string                `json:"root_session_id"`
	Segment                 int64                 `json:"segment"`
	Agent                   string                `json:"agent"`
	ParentReferenceVerified bool                  `json:"parent_reference_verified"`
	ChainVerified           bool                  `json:"chain_verified"`
	VerificationLimited     bool                  `json:"chain_verification_limited"`
	ConfirmedCommits        []string              `json:"confirmed_commits"`
	ConfirmedCount          int                   `json:"confirmed_count"`
	Continuation            *capture.Continuation `json:"continuation,omitempty"`
}

// CapturePage gives explicit navigation when the selected stream/task has more windows.
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

// Task holds a bounded current Tasktopia projection; counts expose omitted source children.
type Task struct {
	Source             retrieval.Source     `json:"source"`
	ObservedAt         time.Time            `json:"observed_at"`
	Number             int64                `json:"number"`
	Title              string               `json:"title"`
	Status             string               `json:"status"`
	Progress           int                  `json:"progress"`
	URL                string               `json:"url"`
	Description        Excerpt              `json:"description"`
	AcceptanceCriteria Excerpt              `json:"acceptance_criteria"`
	Links              []work.Link          `json:"links"`
	LinkCount          int                  `json:"link_count"`
	Checklist          []work.ChecklistItem `json:"checklist"`
	ChecklistCount     int                  `json:"checklist_count"`
	Comments           []Comment            `json:"comments"`
	CommentCount       int                  `json:"comment_count"`
	Defects            []Defect             `json:"defects"`
	DefectCount        int                  `json:"defect_count"`
	OpenDefectCount    int                  `json:"open_defect_count"`
	Dependencies       []work.Dependency    `json:"dependencies"`
	DependencyCount    int                  `json:"dependency_count"`
	Documents          []Document           `json:"documents"`
	DocumentCount      int                  `json:"document_count"`
	ChildrenClipped    bool                 `json:"children_clipped"`
}

// Comment is a source-timed bounded body, without inferred approval or actor attribution.
type Comment struct {
	ID        string    `json:"id"`
	Body      Excerpt   `json:"body"`
	CreatedAt time.Time `json:"created_at"`
}

// Defect keeps observed status and compact detail with source identity.
type Defect struct {
	ID      string  `json:"id"`
	Title   string  `json:"title"`
	Status  string  `json:"status"`
	Details Excerpt `json:"details"`
}

// Document is a navigable header; full text remains in exact retrieval/task APIs.
type Document struct {
	ID       string `json:"id"`
	FileName string `json:"file_name"`
	Title    string `json:"title"`
}

// CompactTask retains source navigation while preventing full task documents/history from filling a model context.
func CompactTask(t work.Task, digest, project string, observed time.Time) Task {
	out := Task{Source: retrieval.Source{Kind: "task", ProjectID: project, CountryID: t.CountryID, TaskID: t.ID, Digest: digest}, ObservedAt: observed, Number: t.Number, Title: t.Title, Status: t.Status, Progress: t.Progress, URL: t.URL, Description: Clip(t.Description, 800), AcceptanceCriteria: Clip(t.AcceptanceCriteria, 1000), Links: []work.Link{}, Checklist: []work.ChecklistItem{}, Comments: []Comment{}, Defects: []Defect{}, Dependencies: []work.Dependency{}, Documents: []Document{}, LinkCount: len(t.Links), ChecklistCount: len(t.Checklist), CommentCount: len(t.Comments), DefectCount: len(t.Defects), DependencyCount: len(t.Dependencies), DocumentCount: len(t.Documents)}
	out.Links = append(out.Links, t.Links[:min(8, len(t.Links))]...)
	for _, v := range t.Checklist[:min(12, len(t.Checklist))] {
		s := Clip(v.Title, 160)
		out.ChildrenClipped = out.ChildrenClipped || s.Truncated
		v.Title = s.Text
		out.Checklist = append(out.Checklist, v)
	}
	comments := slices.Clone(t.Comments)
	slices.SortFunc(comments, func(a, b work.Comment) int {
		if c := b.CreatedAt.Compare(a.CreatedAt); c != 0 {
			return c
		}
		return cmp.Compare(a.ID, b.ID)
	})
	for _, v := range comments[:min(4, len(comments))] {
		out.Comments = append(out.Comments, Comment{v.ID, Clip(v.Body, 300), v.CreatedAt})
	}
	for _, v := range t.Defects[:min(8, len(t.Defects))] {
		out.Defects = append(out.Defects, Defect{v.ID, v.Title, v.Status, Clip(strings.Join([]string{v.Description, v.ReproductionSteps, v.ActualResult, v.ExpectedResult}, "\n"), 400)})
	}
	for _, v := range t.Defects {
		if v.Status != "FIXED" {
			out.OpenDefectCount++
		}
	}
	out.Dependencies = append(out.Dependencies, t.Dependencies[:min(8, len(t.Dependencies))]...)
	for _, v := range t.Documents[:min(8, len(t.Documents))] {
		out.Documents = append(out.Documents, Document{v.ID, v.FileName, v.Title})
	}
	return out
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
	SchemaVersion  int         `json:"schema_version"`
	Query          Query       `json:"query"`
	ReadAt         time.Time   `json:"read_at"`
	Task           *Task       `json:"task,omitempty"`
	TaskState      string      `json:"task_state"`
	Captures       CapturePage `json:"captures"`
	MergeRequests  MRPage      `json:"merge_requests"`
	Evidence       []Evidence  `json:"evidence"`
	EvidenceMore   bool        `json:"evidence_has_more"`
	PendingSources int         `json:"pending_sources_in_project"`
	FailedSources  int         `json:"failed_sources_in_project"`
	BudgetLimited  bool        `json:"budget_limited"`
}
