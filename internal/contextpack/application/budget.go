package application

import (
	"encoding/json"
	"workgraph.local/platform/internal/contextpack/domain"
)

// bound reduces optional detail in bounded passes, preserving authority fields and explicit gap/navigation flags.
func bound(p *domain.Pack) error {
	stages := []func(){
		func() {
			for i := range p.MergeRequests.Items {
				m := &p.MergeRequests.Items[i]
				if len(m.Pipelines) > 2 {
					m.Pipelines = m.Pipelines[:2]
					m.PipelinesMore = true
				}
				if len(m.Deployments) > 2 {
					m.Deployments = m.Deployments[:2]
					m.DeploymentsMore = true
				}
			}
		},
		func() {
			if len(p.Evidence) > 3 {
				p.Evidence = p.Evidence[:3]
				p.EvidenceMore = true
			}
		},
		func() {
			if t := p.Task; t != nil {
				t.Links = t.Links[:min(2, len(t.Links))]
				t.Checklist = t.Checklist[:min(4, len(t.Checklist))]
				t.Comments = t.Comments[:min(1, len(t.Comments))]
				t.Defects = t.Defects[:min(2, len(t.Defects))]
				t.Dependencies = t.Dependencies[:min(2, len(t.Dependencies))]
				t.Documents = t.Documents[:min(2, len(t.Documents))]
				t.ChildrenClipped = true
			}
		},
		func() {
			if len(p.MergeRequests.Items) > 1 {
				p.MergeRequests.Items = p.MergeRequests.Items[:1]
				p.MergeRequests.HasMore = true
				last := p.MergeRequests.Items[0]
				p.MergeRequests.Next = &domain.MRCursor{RepositoryID: last.RepositoryID, IID: last.IID}
			}
		},
		func() {
			if len(p.Captures.Items) > 2 {
				p.Captures.Items = p.Captures.Items[:2]
				last := p.Captures.Items[1]
				p.Captures.Next = &domain.Cursor{RepositoryID: last.Source.RepositoryID, Segment: last.Segment, SessionID: last.Source.SessionID}
			}
		},
		func() {
			for i := range p.MergeRequests.Items {
				m := &p.MergeRequests.Items[i]
				m.PipelinesMore = m.PipelinesMore || len(m.Pipelines) > 0
				m.Pipelines = m.Pipelines[:0]
				m.DeploymentsMore = m.DeploymentsMore || len(m.Deployments) > 0
				m.Deployments = m.Deployments[:0]
			}
		},
		func() { p.EvidenceMore = p.EvidenceMore || len(p.Evidence) > 0; p.Evidence = p.Evidence[:0] },
		func() {
			if t := p.Task; t != nil {
				t.Links = t.Links[:0]
				t.Checklist = t.Checklist[:0]
				t.Comments = t.Comments[:0]
				t.Defects = t.Defects[:0]
				t.Dependencies = t.Dependencies[:0]
				t.Documents = t.Documents[:0]
				t.ChildrenClipped = true
				t.Description = domain.Excerpt{Truncated: t.Description.Truncated || t.Description.Text != ""}
				t.AcceptanceCriteria = domain.Excerpt{Truncated: t.AcceptanceCriteria.Truncated || t.AcceptanceCriteria.Text != ""}
			}
		},
	}
	for i := 0; ; i++ {
		raw, err := json.Marshal(p)
		if err != nil {
			return domain.ErrInvalid
		}
		if len(raw) <= 64*1024 {
			return nil
		}
		if i == len(stages) {
			return domain.ErrInvalid
		}
		p.BudgetLimited = true
		stages[i]()
	}
}
