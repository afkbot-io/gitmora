package domain

import "workgraph.local/platform/internal/redact"

// Sanitize validates raw bounds before removing recognized secrets and taking a canonical copy.
// Both collector and API use this policy so acknowledged content has the same digest.
func Sanitize(input Snapshot) (Session, error) {
	validated, err := NewSession(input)
	if err != nil {
		return Session{}, err
	}
	snapshot := validated.Snapshot()
	for i := range snapshot.Messages {
		snapshot.Messages[i].Text = redact.Text(snapshot.Messages[i].Text)
		snapshot.Messages[i].ToolName = redact.Text(snapshot.Messages[i].ToolName)
	}
	snapshot.Agent.Name = redact.Text(snapshot.Agent.Name)
	snapshot.Agent.Version = redact.Text(snapshot.Agent.Version)
	snapshot.Agent.Model = redact.Text(snapshot.Agent.Model)
	sanitizeOrigin(snapshot.Origin)
	return NewSession(snapshot)
}
