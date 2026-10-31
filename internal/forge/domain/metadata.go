package domain

import (
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	capture "workgraph.local/platform/internal/capture/domain"
	"workgraph.local/platform/internal/redact"
)

// StreamKind names an independently resumable provider collection.
type StreamKind string

const (
	// MergeRequests discovers project merge requests, followed by authoritative detail reads.
	MergeRequests StreamKind = "merge_requests"
	// Pipelines includes regular project pipelines.
	Pipelines StreamKind = "pipelines"
	// ChildPipelines explicitly includes pipelines omitted by GitLab's default pipeline list.
	ChildPipelines StreamKind = "child_pipelines"
	// Deployments records observed environment deployment status and exact deployed SHA.
	Deployments StreamKind = "deployments"
)

// StreamKinds returns a detached deterministic scheduling order.
func StreamKinds() []StreamKind {
	return []StreamKind{MergeRequests, Pipelines, ChildPipelines, Deployments}
}

// Cursor fixes a collection window and fences persisted page advancement with a version.
type Cursor struct {
	Kind          StreamKind
	Version       int64
	After, Before time.Time
	Page          int
	Full          bool
}

// MRHint is discovery only; detailed state is obtained from the MR detail endpoint.
type MRHint struct {
	IID       int64
	UpdatedAt time.Time
}

// ChangePage is one bounded provider page, never a whole repository snapshot.
type ChangePage struct {
	Unavailable   bool
	MergeRequests []MRHint
	Pipelines     []Pipeline
	Deployments   []Deployment
	NextPage      int
}

// MergeRequest is a sanitized observation, with source project scope distinct from its target.
type MergeRequest struct {
	ID              int64     `json:"id"`
	IID             int64     `json:"iid"`
	ProjectID       int64     `json:"project_id"`
	SourceProjectID int64     `json:"source_project_id"`
	State           string    `json:"state"`
	Title           string    `json:"title"`
	Description     string    `json:"description"`
	URL             string    `json:"url"`
	Head            string    `json:"head"`
	Base            string    `json:"base"`
	Start           string    `json:"start"`
	Merge           string    `json:"merge"`
	Squash          string    `json:"squash"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// NormalizeMR validates detached provider state and applies the shared known-secret redaction.
func NormalizeMR(value MergeRequest) (MergeRequest, error) {
	if value.ID < 1 || value.IID < 1 || value.ProjectID < 1 || value.SourceProjectID < 0 || value.UpdatedAt.IsZero() || !metadataText(value.State, 64, true) || !metadataText(value.Title, 2048, false) || !metadataText(value.Description, 1024*1024, false) || !metadataText(value.URL, 2048, false) {
		return MergeRequest{}, ErrInvalid
	}
	for _, sha := range []string{value.Head, value.Base, value.Start, value.Merge, value.Squash} {
		if sha != "" && !capture.ValidCommit(sha) {
			return MergeRequest{}, ErrInvalid
		}
	}
	value.Title = redact.Text(value.Title)
	value.Description = redact.Text(value.Description)
	if len(value.Title) > 2048 || len(value.Description) > 1024*1024 {
		return MergeRequest{}, ErrInvalid
	}
	value.UpdatedAt = value.UpdatedAt.UTC()
	return value, nil
}

// Digest identifies one immutable sanitized metadata observation.
func (m MergeRequest) Digest() string { raw, _ := json.Marshal(m); return hash(raw) }

// CodeKey identifies the diff and merge-result scope to which a complete commit set belongs.
func (m MergeRequest) CodeKey() string {
	raw, _ := json.Marshal(struct {
		Project, Source, IID                    int64
		State, Head, Base, Start, Merge, Squash string
	}{m.ProjectID, m.SourceProjectID, m.IID, m.State, m.Head, m.Base, m.Start, m.Merge, m.Squash})
	return hash(raw)
}

// CodeReady avoids declaring a preparing MR's incomplete diff references authoritative.
func (m MergeRequest) CodeReady() bool { return m.Head != "" && m.Base != "" && m.Start != "" }

// Pipeline always retains its tested SHA; status alone cannot apply to another MR head.
type Pipeline struct {
	ID        int64     `json:"id"`
	ProjectID int64     `json:"project_id"`
	SHA       string    `json:"sha"`
	Status    string    `json:"status"`
	Ref       string    `json:"ref"`
	Source    string    `json:"source"`
	URL       string    `json:"url"`
	UpdatedAt time.Time `json:"updated_at"`
}

// AppliesTo requires both source-project scope and exact current head; other CI stays historical.
func (p Pipeline) AppliesTo(m MergeRequest) bool {
	return m.Head != "" && m.SourceProjectID > 0 && p.ProjectID == m.SourceProjectID && p.SHA == m.Head
}

// NormalizePipeline validates bounded CI metadata without inferring success for unknown statuses.
func NormalizePipeline(p Pipeline) (Pipeline, error) {
	if p.ID < 1 || p.ProjectID < 1 || !capture.ValidCommit(p.SHA) || p.UpdatedAt.IsZero() || !metadataText(p.Status, 64, true) || !metadataText(p.Ref, 1024, false) || !metadataText(p.Source, 128, false) || !metadataText(p.URL, 2048, false) {
		return Pipeline{}, ErrInvalid
	}
	p.Ref = redact.Text(p.Ref)
	if len(p.Ref) > 1024 {
		return Pipeline{}, ErrInvalid
	}
	p.UpdatedAt = p.UpdatedAt.UTC()
	return p, nil
}

// Deployment records only scoped deployment metadata; CI variables and external URLs are excluded.
type Deployment struct {
	ID            int64     `json:"id"`
	SHA           string    `json:"sha"`
	Status        string    `json:"status"`
	Ref           string    `json:"ref"`
	EnvironmentID int64     `json:"environment_id"`
	Environment   string    `json:"environment"`
	PipelineID    int64     `json:"pipeline_id,omitempty"`
	JobID         int64     `json:"job_id,omitempty"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// NormalizeDeployment validates observed deployment identity and exact code association.
func NormalizeDeployment(d Deployment) (Deployment, error) {
	if d.ID < 1 || !capture.ValidCommit(d.SHA) || d.UpdatedAt.IsZero() || d.EnvironmentID < 1 || d.PipelineID < 0 || d.JobID < 0 || !metadataText(d.Status, 64, true) || !metadataText(d.Ref, 1024, false) || !metadataText(d.Environment, 1024, true) {
		return Deployment{}, ErrInvalid
	}
	d.Ref = redact.Text(d.Ref)
	d.Environment = redact.Text(d.Environment)
	if len(d.Ref) > 1024 || len(d.Environment) > 1024 {
		return Deployment{}, ErrInvalid
	}
	d.UpdatedAt = d.UpdatedAt.UTC()
	return d, nil
}

// MRCommitPage is a guarded source-commit page or a replacement for a concurrently changed diff.
type MRCommitPage struct {
	Commits     []string
	NextPage    int
	Replacement *MergeRequest
	Missing     bool
}

func metadataText(value string, limit int, required bool) bool {
	return len(value) <= limit && utf8.ValidString(value) && !strings.ContainsRune(value, 0) && (!required || strings.TrimSpace(value) != "")
}
