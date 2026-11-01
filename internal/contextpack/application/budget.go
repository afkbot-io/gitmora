package application

import (
	"encoding/json"
	"workgraph.local/platform/internal/contextpack/domain"
)

// bound reduces optional detail in bounded passes, preserving authority fields and explicit gap/navigation flags.
func bound(p *domain.Pack) error {
	stages := []func(){
		func() {
			for len(p.Transcript.Items) > 1 {
				raw, _ := json.Marshal(p)
				if len(raw) <= 64*1024 {
					break
				}
				last := p.Transcript.Items[len(p.Transcript.Items)-1]
				p.Transcript.Items = p.Transcript.Items[:len(p.Transcript.Items)-1]
				p.Transcript.HasMore = true
				p.Transcript.Next = &domain.MessageCursor{RepositoryID: last.Source.RepositoryID, SessionID: last.Source.SessionID, Revision: last.Source.Revision, Digest: last.Source.Digest, Segment: last.Segment, Offset: last.Index, RuneOffset: last.MessageOffset}
				if len(p.MergeRequests.Items) > 0 {
					p.Transcript.Next.Head = p.MergeRequests.Items[0].Head
					p.Transcript.Next.ScopeKey = p.MergeRequests.Items[0].MessageScopeKey()
				}
			}
		},
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
