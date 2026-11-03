// Package codex adapts exposed Codex app-server history without recording reasoning or reading rollout files.
package codex

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	capture "workgraph.local/platform/internal/capture/domain"
	"workgraph.local/platform/internal/collector/domain"
	"workgraph.local/platform/internal/jsonwire"
	"workgraph.local/platform/internal/redact"
)

// Item count and protocol/text byte budgets independently bound long conversation recovery.
const maxHistoryItems = 50000

var ErrProtocol = errors.New("Codex history is invalid, unsupported or exceeds the capture bound")
var ErrUnavailable = errors.New("Codex app-server unavailable; source remains local")

type Thread struct {
	ID            string `json:"id"`
	CWD           string `json:"cwd"`
	CreatedAt     int64  `json:"createdAt"`
	UpdatedAt     int64  `json:"updatedAt"`
	Model         string `json:"model"`
	ModelProvider string `json:"modelProvider"`
	CLIVersion    string `json:"cliVersion"`
}
type Entry struct {
	TurnID string          `json:"turnId"`
	Item   json.RawMessage `json:"item"`
}
type Window struct {
	Observation domain.Observation
	ItemIDs     []string
}
type History struct {
	Windows      []Window
	SkippedTypes []string
}

func digest(values ...string) string {
	raw, _ := json.Marshal(values)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func locator(value string) bool {
	return len(value) > 0 && len(value) <= 180 && !strings.ContainsAny(value, "\x00\r\n") && utf8.ValidString(value)
}

type nativeItem struct {
	ID       string            `json:"id"`
	Type     string            `json:"type"`
	Text     string            `json:"text"`
	Content  []json.RawMessage `json:"content"`
	Command  string            `json:"command"`
	CWD      string            `json:"cwd"`
	Status   string            `json:"status"`
	Output   *string           `json:"aggregatedOutput"`
	ExitCode *int              `json:"exitCode"`
	Changes  []struct {
		Path string          `json:"path"`
		Diff string          `json:"diff"`
		Kind json.RawMessage `json:"kind"`
	} `json:"changes"`
	Server         string            `json:"server"`
	Tool           string            `json:"tool"`
	Name           string            `json:"name"`
	Namespace      *string           `json:"namespace"`
	Arguments      json.RawMessage   `json:"arguments"`
	Result         json.RawMessage   `json:"result"`
	Error          json.RawMessage   `json:"error"`
	FunctionOutput json.RawMessage   `json:"output"`
	ContentItems   []json.RawMessage `json:"contentItems"`
}

func textFragments(parts []json.RawMessage) []string {
	result := []string{}
	for _, part := range parts {
		var value struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if jsonwire.DecodeExternalObject(part, &value) == nil && (value.Type == "text" || value.Type == "inputText" || value.Type == "outputText") {
			result = append(result, value.Text)
		}
	}
	return result
}
func textParts(parts []json.RawMessage) string { return strings.Join(textFragments(parts), "\n") }
func messages(i nativeItem) []capture.Message {
	if i.Type == "userMessage" {
		result := []capture.Message{}
		for _, text := range textFragments(i.Content) {
			if text != "" {
				result = append(result, capture.Message{Role: "user", Text: text})
			}
		}
		return result
	}
	message, ok := exposed(i)
	if !ok || message.Text == "" {
		return nil
	}
	return []capture.Message{message}
}
func toolText(value any) string { raw, _ := json.Marshal(value); return string(raw) }
func exposed(i nativeItem) (capture.Message, bool) {
	switch i.Type {
	case "userMessage":
		return capture.Message{Role: "user", Text: textParts(i.Content)}, true
	case "agentMessage", "plan":
		return capture.Message{Role: "assistant", Text: i.Text}, true
	case "commandExecution":
		if i.Status == "inProgress" {
			return capture.Message{}, false
		}
		return capture.Message{Role: "tool", ToolName: "Bash", Text: toolText(struct {
			Command  string  `json:"command"`
			CWD      string  `json:"cwd"`
			Status   string  `json:"status"`
			ExitCode *int    `json:"exit_code"`
			Output   *string `json:"output"`
		}{i.Command, i.CWD, i.Status, i.ExitCode, i.Output})}, true
	case "fileChange":
		if i.Status == "inProgress" {
			return capture.Message{}, false
		}
		return capture.Message{Role: "tool", ToolName: "apply_patch", Text: toolText(struct {
			Status  string `json:"status"`
			Changes any    `json:"changes"`
		}{i.Status, i.Changes})}, true
	case "mcpToolCall":
		if i.Status == "inProgress" {
			return capture.Message{}, false
		}
		var result struct {
			Content []json.RawMessage `json:"content"`
		}
		if len(i.Result) > 0 && string(i.Result) != "null" {
			if jsonwire.DecodeExternalObject(i.Result, &result) != nil {
				return capture.Message{}, false
			}
		}
		return capture.Message{Role: "tool", ToolName: "mcp", Text: toolText(struct {
			Server    string          `json:"server"`
			Tool      string          `json:"tool"`
			Status    string          `json:"status"`
			Arguments json.RawMessage `json:"arguments"`
			Output    string          `json:"output"`
			Error     json.RawMessage `json:"error,omitempty"`
		}{i.Server, i.Tool, i.Status, i.Arguments, textParts(result.Content), i.Error})}, true
	case "dynamicToolCall":
		if i.Status == "inProgress" {
			return capture.Message{}, false
		}
		return capture.Message{Role: "tool", ToolName: "function", Text: toolText(struct {
			Tool      string          `json:"tool"`
			Status    string          `json:"status"`
			Arguments json.RawMessage `json:"arguments"`
			Output    string          `json:"output"`
		}{i.Tool, i.Status, i.Arguments, textParts(i.ContentItems)})}, true
	case "functionCallOutput":
		var output string
		if json.Unmarshal(i.FunctionOutput, &output) != nil {
			var parts []json.RawMessage
			if json.Unmarshal(i.FunctionOutput, &parts) != nil {
				return capture.Message{}, false
			}
			output = textParts(parts)
		}
		return capture.Message{Role: "tool", ToolName: "function", Text: toolText(struct {
			Name   string `json:"name"`
			Output string `json:"output"`
		}{i.Name, output})}, true
	default:
		return capture.Message{}, false
	}
}

// Normalize keeps visible text exact (apart from shared credential redaction), preserving order and deterministic windows.
// Unsupported/media-only item kinds are reported, never represented as invented conversation.
func Normalize(thread Thread, entries []Entry, model, version, root string) (History, error) {
	if !locator(thread.ID) || !filepath.IsAbs(root) || !filepath.IsAbs(thread.CWD) || filepath.Clean(root) != filepath.Clean(thread.CWD) || thread.CreatedAt < 1 || len(entries) > maxHistoryItems {
		return History{}, ErrProtocol
	}
	basis := "hook_configuration"
	if thread.Model != "" {
		model = thread.Model
		basis = "thread_configuration"
	}
	if thread.CLIVersion != "" {
		version = thread.CLIVersion
	}
	if model == "" {
		model = "unknown"
		basis = ""
	}
	if version == "" {
		version = "unknown"
	}
	started := time.Unix(thread.CreatedAt, 0).UTC()
	history := History{Windows: []Window{}, SkippedTypes: []string{}}
	current := Window{}
	size, total := 0, 0
	flush := func() error {
		if len(current.Observation.Messages) == 0 {
			return nil
		}
		o := current.Observation
		o.SchemaVersion = 1
		o.SourceSessionID = "codex:" + thread.ID
		o.Agent = capture.Agent{Name: "codex", Model: model, Version: version, Provider: thread.ModelProvider, ModelBasis: basis}
		o.StartedAt = &started
		o.History.PageID = digest("codex-page-v1", o.History.MessageIDs[0])
		o.History.Epoch = digest("codex-items-v1", thread.ID)
		o.History.Reason = "window"
		raw, _ := json.Marshal(o)
		o.EventID = digest("codex-observation-v1", string(raw))
		normalized, e := domain.Normalize(o)
		if e != nil {
			return ErrProtocol
		}
		current.Observation = normalized.Value()
		history.Windows = append(history.Windows, current)
		current = Window{}
		size = 0
		return nil
	}
	seen := map[string]string{}
	for _, entry := range entries {
		if !locator(entry.TurnID) || len(entry.Item) > 16*1024*1024 {
			return history, ErrProtocol
		}
		var i nativeItem
		if jsonwire.DecodeExternalObject(entry.Item, &i) != nil || !locator(i.ID) || i.Type == "" {
			return history, ErrProtocol
		}
		fingerprint := digest(string(entry.Item))
		if prior, exists := seen[i.ID]; exists {
			if prior != fingerprint {
				return history, ErrProtocol
			}
			continue
		}
		seen[i.ID] = fingerprint
		visible := messages(i)
		if len(visible) == 0 {
			kind := i.Type
			if !domain.ValidAgentName(kind) {
				kind = "unknown"
			}
			if !slices.Contains(history.SkippedTypes, kind) {
				history.SkippedTypes = append(history.SkippedTypes, kind)
			}
			continue
		}
		events := []domain.FileEvent{}
		if i.Type == "fileChange" && i.Status == "completed" {
			for _, change := range i.Changes {
				path := change.Path
				if filepath.IsAbs(path) {
					relative, e := filepath.Rel(root, filepath.Clean(path))
					if e != nil {
						continue
					}
					path = relative
				}
				path = filepath.ToSlash(filepath.Clean(path))
				if path == "." || path == ".." || strings.HasPrefix(path, "../") || strings.HasPrefix(path, ".git/") || path == ".git" || strings.ContainsAny(path, "\x00\r\n") {
					continue
				}
				events = append(events, domain.FileEvent{ID: "codex:" + i.ID, Path: path})
			}
		}
		part := 0
		for _, message := range visible {
			message.TurnID = entry.TurnID
			message.NativeID = i.ID
			if message.Role == "tool" {
				seenPaths := map[string]bool{}
				for _, event := range events {
					if capture.ValidFilePath(event.Path) && !seenPaths[event.Path] && len(message.Changes) < 256 {
						message.Changes = append(message.Changes, capture.FileChange{Path: event.Path})
						seenPaths[event.Path] = true
					}
				}
			}
			message.Text = redact.Conversation(message.Text)
			total += len(message.Text)
			if total > 64*1024*1024 {
				return history, ErrProtocol
			}
			text := message.Text
			for ; len(text) > 0; part++ {
				n := min(len(text), 64*1024)
				for n < len(text) && !utf8.RuneStart(text[n]) {
					n--
				}
				piece := message
				piece.Text = text[:n]
				text = text[n:]
				legacy := piece
				legacy.Model = ""
				legacy.Provider = ""
				legacy.AgentProfile = ""
				legacy.NativeID = ""
				legacy.TurnID = ""
				legacy.Changes = nil
				raw, _ := json.Marshal(legacy)
				eventRaw, _ := json.Marshal(events)
				if len(current.Observation.Messages) > 0 && (size+len(raw)+len(eventRaw) > 1024*1024 || len(current.Observation.Messages) >= 512) {
					if e := flush(); e != nil {
						return history, e
					}
				}
				if current.Observation.History == nil {
					current.Observation.History = &domain.NativeHistory{}
				}
				current.Observation.Messages = append(current.Observation.Messages, piece)
				current.Observation.History.MessageIDs = append(current.Observation.History.MessageIDs, digest("codex-item-v1", thread.ID, i.ID, strconv.Itoa(part)))
				current.ItemIDs = append(current.ItemIDs, i.ID)
				current.Observation.FileEvents = append(current.Observation.FileEvents, events...)
				size += len(raw) + len(eventRaw)
				events = nil
			}
		}
	}
	if e := flush(); e != nil {
		return history, e
	}
	slices.Sort(history.SkippedTypes)
	return history, nil
}
