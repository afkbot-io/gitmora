// Package application bridges complete compatibility projections to durable collector observations.
package application

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	capture "workgraph.local/platform/internal/capture/domain"
	collector "workgraph.local/platform/internal/collector/domain"
	"workgraph.local/platform/internal/compatibility/domain"
)

// Prepare normalizes every window before returning any; callers must not persist a partial plan.
func Prepare(projection *domain.Projection) ([]collector.Observation, error) {
	windows, err := projection.Windows()
	if err != nil {
		return nil, err
	}
	result := make([]collector.Observation, 0, len(windows))
	for _, window := range windows {
		base := *window.Origin
		base.Part = 0
		base.LineStart = 0
		base.LineEnd = 0
		base.Claims = nil
		identity, _ := json.Marshal(struct {
			Origin   capture.ImportOrigin
			Importer string
		}{base, window.Agent.Version})
		source := digest(identity)
		page := digest([]byte(fmt.Sprintf("%s:%d", source, window.Origin.Part)))
		messageIDs := make([]string, len(window.Messages))
		for i := range messageIDs {
			messageIDs[i] = digest([]byte(fmt.Sprintf("%s:%d", page, i)))
		}
		started := window.StartedAt
		input := collector.Observation{SchemaVersion: 1, SourceSessionID: "compat:" + source, EventID: page, Agent: window.Agent, StartedAt: &started, Messages: window.Messages, ChangedFiles: window.Files, Origin: window.Origin, History: &collector.NativeHistory{PageID: page, Epoch: source, MessageIDs: messageIDs, Reason: "window"}}
		normalized, err := collector.Normalize(input)
		if err != nil {
			return nil, err
		}
		result = append(result, normalized.Value())
	}
	return result, nil
}
func digest(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
