package sqlite

import (
	"context"
	"encoding/json"
	"slices"

	"workgraph.local/platform/internal/collector/application"
	"workgraph.local/platform/internal/collector/domain"
)

// SetCollection changes durable local consent without changing destination, observations or queue contents.
// Empty agent controls the whole repository; named controls remain independent when the repository is re-enabled.
func (s *Store) SetCollection(ctx context.Context, enabled bool, agent string) error {
	if agent != "" && !domain.ValidAgentName(agent) {
		return domain.ErrInvalid
	}

	return s.Within(ctx, func(tx application.Transaction) error {
		current, e := tx.Config()
		if e != nil {
			return e
		}
		if agent == "" {
			current.Disabled = !enabled
		} else {
			current.DisabledAgents = slices.DeleteFunc(current.DisabledAgents, func(v string) bool { return v == agent })
			if !enabled {
				current.DisabledAgents = append(current.DisabledAgents, agent)
			}
		}
		current, e = domain.NormalizeConfig(current)
		if e != nil {
			return e
		}
		raw, e := json.Marshal(current)
		if e != nil {
			return e
		}
		_, e = tx.(*transaction).tx.ExecContext(ctx, `UPDATE config SET data=? WHERE id=1`, raw)
		return e
	})
}
