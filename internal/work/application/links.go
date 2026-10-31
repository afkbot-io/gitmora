package application

import (
	"context"
	"errors"
	"time"

	"workgraph.local/platform/internal/work/domain"
)

// LinkPublication is the durable delivery outcome of one operator-authorized MR link.
type LinkPublication struct {
	ID          string               `json:"id"`
	TaskID      string               `json:"task_id"`
	State       string               `json:"state"`
	ErrorCode   string               `json:"error_code,omitempty"`
	Link        domain.MRLink        `json:"link"`
	Evidence    *domain.LinkEvidence `json:"evidence,omitempty"`
	CompletedAt *time.Time           `json:"completed_at,omitempty"`
}

// LinkPage bounds task link delivery history without exposing session conversation text.
type LinkPage struct {
	Items   []LinkPublication `json:"items"`
	HasMore bool              `json:"has_more"`
}

// Links returns the latest bounded delivery records under current project membership.
func (s *Service) Links(ctx context.Context, user, project, task string) (LinkPage, error) {
	return s.store.Links(ctx, user, project, task)
}

func (s *Service) ensureLink(ctx context.Context, config domain.Config, lease Lease, task domain.Task) (domain.Task, error) {
	if lease.Link.Validate() != nil {
		return task, &ProviderError{Code: "link_invalid"}
	}
	if task.HasLink(lease.Link.URL) {
		return task, nil
	}
	err := s.provider.AddLink(ctx, config, lease.TaskID, lease.ID, lease.Link)
	var failure *ProviderError
	// Another writer may add the same URL between task.get and link_add. Reconcile that conflict.
	if err != nil && !(errors.As(err, &failure) && failure.Code == "publication_conflict") {
		return task, err
	}
	current, readErr := s.provider.Task(ctx, config, lease.TaskID)
	if readErr != nil {
		return task, readErr
	}
	if current.HasLink(lease.Link.URL) {
		return current, nil
	}
	if err != nil {
		return task, &ProviderError{Code: "link_conflict"}
	}
	return task, &ProviderError{Code: "link_readback_mismatch"}
}
