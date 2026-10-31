package application

import (
	"context"
	"time"
	"workgraph.local/platform/internal/forge/domain"
)

// MRWork is a version-fenced resumable detail/commit-set job.
type MRWork struct {
	IID, Version, Requested int64
	Stage                   string
	Page                    int
	Expected                domain.MergeRequest
}

// MetadataWork schedules one provider page or one MR detail operation per worker batch.
type MetadataWork struct {
	Lane   int
	Cursor domain.Cursor
	MR     *MRWork
}

// StreamStatus distinguishes completed, in-progress and temporarily unavailable collections.
type StreamStatus struct {
	Kind           domain.StreamKind `json:"kind"`
	NextPage       int               `json:"next_page"`
	Active         bool              `json:"active"`
	Watermark      time.Time         `json:"watermark"`
	LastCompleteAt *time.Time        `json:"last_complete_at,omitempty"`
	ErrorCode      string            `json:"error_code,omitempty"`
	RetryAt        time.Time         `json:"retry_at"`
}

// MRView expands one immutable MR observation with bounded, explicitly incomplete source membership.
type MRView struct {
	Model           domain.MergeRequest `json:"model"`
	Observation     string              `json:"observation"`
	CodeKey         string              `json:"code_key"`
	Available       bool                `json:"provider_available"`
	CommitsComplete bool                `json:"commits_complete"`
	SourceCommits   []string            `json:"source_commits"`
	NextSHA         string              `json:"next_sha,omitempty"`
	Results         []string            `json:"result_shas"`
}

// MetadataQuery bounds artifact expansion by immutable IDs or source SHAs.
type MetadataQuery struct {
	IID                   int64
	Observation, AfterSHA string
	AfterID               int64
	Limit                 int
}

// PipelinePage is a bounded tested-SHA CI projection.
type PipelinePage struct {
	Items  []domain.Pipeline `json:"items"`
	NextID int64             `json:"next_id,omitempty"`
}

// DeploymentPage is a bounded deployed-SHA projection.
type DeploymentPage struct {
	Items  []domain.Deployment `json:"items"`
	NextID int64               `json:"next_id,omitempty"`
}

// MetadataStore adds independently persisted provider cursors and immutable MR observations.
type MetadataStore interface {
	NextMetadata(context.Context, Lease) (MetadataWork, bool, error)
	ApplyChanges(context.Context, Lease, MetadataWork, domain.ChangePage) error
	ApplyMR(context.Context, Lease, MetadataWork, *domain.MergeRequest) error
	ApplyMRCommits(context.Context, Lease, MetadataWork, domain.MRCommitPage) error
	MetadataDue(context.Context, Lease) (bool, error)
	ReadMR(context.Context, string, string, MetadataQuery) (MRView, error)
	ReadPipelines(context.Context, string, string, MetadataQuery) (PipelinePage, error)
	ReadDeployments(context.Context, string, string, MetadataQuery) (DeploymentPage, error)
}

// MetadataProvider exposes paginated provider reads and a guarded MR commit-membership operation.
type MetadataProvider interface {
	Changes(context.Context, domain.Repository, string, domain.Cursor) (domain.ChangePage, error)
	MR(context.Context, domain.Repository, string, int64) (domain.MergeRequest, bool, error)
	MRCommits(context.Context, domain.Repository, string, domain.MergeRequest, int) (domain.MRCommitPage, error)
}

// SyncInterval and FullRepairInterval preserve overlap recovery and periodic offset-pagination repair.
const SyncInterval = 5 * time.Minute
const FullRepairInterval = 24 * time.Hour

func (s *Service) metadata(ctx context.Context, lease Lease, token string) (bool, error) {
	work, found, err := s.store.NextMetadata(ctx, lease)
	if err != nil || !found {
		return found, err
	}
	if work.MR == nil {
		page, err := s.provider.Changes(ctx, lease.Repository, token, work.Cursor)
		if err != nil {
			return false, err
		}
		if err := s.store.ApplyChanges(ctx, lease, work, page); err != nil {
			return false, err
		}
	} else if work.MR.Stage == "detail" {
		model, found, err := s.provider.MR(ctx, lease.Repository, token, work.MR.IID)
		if err != nil {
			return false, err
		}
		var current *domain.MergeRequest
		if found {
			current = &model
		}
		if err := s.store.ApplyMR(ctx, lease, work, current); err != nil {
			return false, err
		}
	} else {
		page, err := s.provider.MRCommits(ctx, lease.Repository, token, work.MR.Expected, work.MR.Page)
		if err != nil {
			return false, err
		}
		if err := s.store.ApplyMRCommits(ctx, lease, work, page); err != nil {
			return false, err
		}
	}
	return s.store.MetadataDue(ctx, lease)
}

// MergeRequest returns authorized exact observation/code evidence with bounded expansion.
func (s *Service) MergeRequest(ctx context.Context, subject, repo string, query MetadataQuery) (MRView, error) {
	return s.store.ReadMR(ctx, subject, repo, query)
}

// Pipelines returns authorized provider-observed CI metadata with immutable tested SHAs.
func (s *Service) Pipelines(ctx context.Context, subject, repo string, query MetadataQuery) (PipelinePage, error) {
	return s.store.ReadPipelines(ctx, subject, repo, query)
}

// Deployments returns authorized provider-observed deployment/code associations.
func (s *Service) Deployments(ctx context.Context, subject, repo string, query MetadataQuery) (DeploymentPage, error) {
	return s.store.ReadDeployments(ctx, subject, repo, query)
}
