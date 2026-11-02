package postgres

import (
	"strings"
	capture "workgraph.local/platform/internal/capture/domain"
	"workgraph.local/platform/internal/retrieval/domain"
)

func knownModel(value string) string {
	if value == "unknown" || value == "unreported" || value == "no-model-needed" || value == "none" || strings.Contains(value, "[REDACTED]") {
		return ""
	}
	return value
}

// captureMetadata never converts a thread's current configuration into per-message execution telemetry.
func captureMetadata(s capture.Snapshot, m capture.Message) domain.Metadata {
	result := domain.Metadata{Agent: s.Agent.Name, Version: s.Agent.Version, Role: m.Role, Tool: m.ToolName, Profile: m.AgentProfile, TurnID: m.TurnID, NativeID: m.NativeID, Relation: "message"}
	if m.Model != "" {
		result.Model = m.Model
		result.Provider = m.Provider
		result.ModelBasis = "message_metadata"
	} else if model := knownModel(s.Agent.Model); model != "" {
		result.Model = model
		result.Provider = s.Agent.Provider
		if result.Provider == "" && strings.EqualFold(s.Agent.Name, "opencode") {
			if provider, model, ok := strings.Cut(result.Model, "/"); ok && provider != "" && model != "" {
				result.Provider = provider
				result.Model = model
			}
		}
		result.ModelBasis = "session_configuration"
	}
	if len(m.Changes) > 0 {
		result.Relation = "tool_reported"
	}
	if s.Origin != nil && s.Origin.Agent != nil {
		result.Agent = s.Origin.Agent.Tool
		result.Model = knownModel(s.Origin.Agent.Model)
		result.ModelBasis = "import_report"
	}
	return result
}
