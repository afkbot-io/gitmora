package domain

import (
	"slices"
	"time"
	"workgraph.local/platform/internal/identifier"
	"workgraph.local/platform/internal/redact"
)

// ChecklistItem records Tasktopia's checklist state without asserting verification of the item.
type ChecklistItem struct {
	ID       string `json:"id"`
	TaskID   string `json:"taskId,omitempty"`
	Title    string `json:"title"`
	Done     bool   `json:"done"`
	Position int    `json:"position"`
}

// Comment retains source text and time; it supplies no trusted instruction or approval.
type Comment struct {
	ID        string    `json:"id"`
	TaskID    string    `json:"taskId,omitempty"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
}

// Defect is an observed Tasktopia defect, independent of GitLab/test conclusions.
type Defect struct {
	ID                string    `json:"id"`
	TaskID            string    `json:"taskId,omitempty"`
	Title             string    `json:"title"`
	Description       string    `json:"description"`
	ReproductionSteps string    `json:"reproductionSteps"`
	ActualResult      string    `json:"actualResult"`
	ExpectedResult    string    `json:"expectedResult"`
	Status            string    `json:"status"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

// Dependency contains a linked task header reported by the same country-scoped source.
type Dependency struct {
	ID     string `json:"id"`
	Number int64  `json:"taskNumber"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

func child(id, task, expected string) bool {
	return identifier.Valid(id) && (task == "" || task == expected)
}
func taskStatus(s string) bool {
	return slices.Contains([]string{"PLANNING", "STARTED", "IN_PROGRESS", "TESTING", "COMPLETED"}, s)
}

func normalizeContext(t *Task) error {
	if len(t.Checklist) > 512 || len(t.Comments) > 512 || len(t.Defects) > 128 || len(t.Dependencies) > 256 {
		return ErrInvalid
	}
	t.Checklist = slices.Clone(t.Checklist)
	t.Comments = slices.Clone(t.Comments)
	t.Defects = slices.Clone(t.Defects)
	t.Dependencies = slices.Clone(t.Dependencies)
	seen := map[string]bool{}
	for i, v := range t.Checklist {
		if !child(v.ID, v.TaskID, t.ID) || seen["list:"+v.ID] || !text(v.Title, 2048, true) || v.Position < 0 || v.Position > 100000 {
			return ErrInvalid
		}
		seen["list:"+v.ID] = true
		v.Title = redact.Text(v.Title)
		if !text(v.Title, 2048, true) {
			return ErrInvalid
		}
		t.Checklist[i] = v
	}
	for i, v := range t.Comments {
		if !child(v.ID, v.TaskID, t.ID) || seen["comment:"+v.ID] || !text(v.Body, 64000, true) || v.CreatedAt.IsZero() {
			return ErrInvalid
		}
		seen["comment:"+v.ID] = true
		v.Body = redact.Text(v.Body)
		v.CreatedAt = v.CreatedAt.UTC()
		if !text(v.Body, 64000, true) {
			return ErrInvalid
		}
		t.Comments[i] = v
	}
	for i, v := range t.Defects {
		if !child(v.ID, v.TaskID, t.ID) || seen["defect:"+v.ID] || !text(v.Title, 2048, true) || v.UpdatedAt.IsZero() || !slices.Contains([]string{"OPEN", "IN_PROGRESS", "VERIFYING", "FIXED"}, v.Status) {
			return ErrInvalid
		}
		seen["defect:"+v.ID] = true
		v.Title = redact.Text(v.Title)
		if !text(v.Title, 2048, true) {
			return ErrInvalid
		}
		for _, field := range []*string{&v.Description, &v.ReproductionSteps, &v.ActualResult, &v.ExpectedResult} {
			if !text(*field, 64000, false) {
				return ErrInvalid
			}
			*field = redact.Text(*field)
			if !text(*field, 64000, false) {
				return ErrInvalid
			}
		}
		v.UpdatedAt = v.UpdatedAt.UTC()
		t.Defects[i] = v
	}
	for i, v := range t.Dependencies {
		if !identifier.Valid(v.ID) || v.ID == t.ID || seen["dependency:"+v.ID] || v.Number < 1 || !text(v.Title, 512, true) || !taskStatus(v.Status) {
			return ErrInvalid
		}
		seen["dependency:"+v.ID] = true
		v.Title = redact.Text(v.Title)
		if !text(v.Title, 512, true) {
			return ErrInvalid
		}
		t.Dependencies[i] = v
	}
	return nil
}
