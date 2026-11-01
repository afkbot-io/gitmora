package postgres

import "workgraph.local/platform/internal/work/domain"

type linkMatch struct {
	TaskID   string
	Link     domain.MRLink
	Evidence domain.LinkEvidence
}
type rowScanner interface{ Scan(...any) error }

func readLinkMatch(row rowScanner) (linkMatch, error) {
	var m linkMatch
	err := row.Scan(&m.TaskID, &m.Link.RepositoryID, &m.Link.BaseURL, &m.Link.ProjectID, &m.Link.IID, &m.Link.URL, &m.Evidence.RepositoryID, &m.Evidence.SessionID, &m.Evidence.Revision, &m.Evidence.CaptureDigest, &m.Evidence.CommitSHA, &m.Evidence.MRObservation, &m.Evidence.MRCodeKey, &m.Link.Provider)
	if err != nil {
		return m, err
	}
	// Discovery blocks invalid rows individually and clears their URL before persistence.
	m.Link.Title = domain.ProviderMRLinkTitle(m.Link.Provider, m.Link.IID)
	return m, nil
}
