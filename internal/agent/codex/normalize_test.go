package codex_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"workgraph.local/platform/internal/agent/codex"
)

func item(id, kind string, fields map[string]any) codex.Entry {
	fields["id"] = id
	fields["type"] = kind
	raw, _ := json.Marshal(fields)
	return codex.Entry{TurnID: "turn", Item: raw}
}
func TestCodexNormalizationPreservesOnlyExposedTextAndExactFileClaims(t *testing.T) {
	thread := codex.Thread{ID: "thread", CWD: "/workspace/repo", CreatedAt: time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC).Unix()}
	entries := []codex.Entry{
		item("u", "userMessage", map[string]any{"content": []any{map[string]any{"type": "text", "text": "User intent"}}}),
		item("hidden", "reasoning", map[string]any{"content": []string{"private-reasoning-must-not-be-recorded"}}),
		item("edit", "fileChange", map[string]any{"status": "completed", "changes": []any{map[string]any{"path": "/workspace/repo/src/main.go", "diff": "+ exact change", "kind": map[string]any{"type": "update"}}}}),
		item("outside", "fileChange", map[string]any{"status": "completed", "changes": []any{map[string]any{"path": "/another/secret.go", "diff": "+ unrelated"}}}),
		item("a", "agentMessage", map[string]any{"text": "Exact final answer"}),
	}
	result, e := codex.Normalize(thread, entries, "fixture-model", "fixture-version", thread.CWD)
	if e != nil || len(result.Windows) != 1 {
		t.Fatal(result, e)
	}
	o := result.Windows[0].Observation
	raw, _ := json.Marshal(o)
	if strings.Contains(string(raw), "private-reasoning-must-not-be-recorded") || !strings.Contains(string(raw), "Exact final answer") {
		t.Fatal("hidden reasoning captured or visible tail lost")
	}
	if o.Agent.Name != "codex" || o.SourceSessionID != "codex:thread" || len(o.FileEvents) != 1 || o.FileEvents[0].Path != "src/main.go" {
		t.Fatal("source/file scope lost", o.FileEvents)
	}
	if _, e = codex.Normalize(thread, entries, "fixture-model", "fixture-version", "/other/repo"); e == nil {
		t.Fatal("thread from another repository captured")
	}
}
func TestCodexLongUnicodeTextUsesDeterministicBoundedWindowsWithoutLosingTail(t *testing.T) {
	thread := codex.Thread{ID: "long", CWD: "/repo", CreatedAt: time.Date(2026, 10, 30, 0, 0, 0, 0, time.UTC).Unix()}
	text := strings.Repeat("Точный контекст 🧪.\n", 160000) + "VisibleTailSentinel"
	entries := []codex.Entry{item("a", "agentMessage", map[string]any{"text": text})}
	first, e := codex.Normalize(thread, entries, "fixture-model", "1", "/repo")
	if e != nil || len(first.Windows) < 2 {
		t.Fatal("long source not windowed", e)
	}
	second, e := codex.Normalize(thread, entries, "fixture-model", "1", "/repo")
	if e != nil {
		t.Fatal(e)
	}
	var restored strings.Builder
	for i, window := range first.Windows {
		if window.Observation.EventID != second.Windows[i].Observation.EventID {
			t.Fatal("replay changed event identity")
		}
		if len(window.Observation.Messages) > 1000 {
			t.Fatal("unbounded source page")
		}
		for _, message := range window.Observation.Messages {
			if len(message.Text) > 128*1024 {
				t.Fatal("oversized message")
			}
			restored.WriteString(message.Text)
		}
	}
	if restored.String() != text {
		t.Fatal("Unicode source bytes lost or invented")
	}
}

func TestUserFragmentsRemainDistinctWithoutInventedSeparators(t *testing.T) {
	thread := codex.Thread{ID: "fragments", CWD: "/repo", CreatedAt: 1793318400}
	entries := []codex.Entry{item("u", "userMessage", map[string]any{"content": []any{
		map[string]any{"type": "text", "text": "First fragment"},
		map[string]any{"type": "image", "url": "media-not-recorded"},
		map[string]any{"type": "text", "text": "Second fragment"},
	}})}
	history, err := codex.Normalize(thread, entries, "", "", "/repo")
	if err != nil || len(history.Windows) != 1 {
		t.Fatal(err)
	}
	messages := history.Windows[0].Observation.Messages
	if len(messages) != 2 || messages[0].Text != "First fragment" || messages[1].Text != "Second fragment" {
		t.Fatal("native fragment boundaries changed", messages)
	}
}

func TestConversationPasswordIsMaskedBeforeNativeFragmentation(t *testing.T) {
	thread := codex.Thread{ID: "thread", CWD: "/workspace/repo", CreatedAt: 1793318400}
	text := strings.Repeat("x", 65520) + " пароль пока сделай unitSecret71!, потом поменяю"
	h, e := codex.Normalize(thread, []codex.Entry{item("user", "userMessage", map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}})}, "unknown", "unknown", thread.CWD)
	if e != nil {
		t.Fatal(e)
	}
	var joined strings.Builder
	count := 0
	for _, w := range h.Windows {
		for _, m := range w.Observation.Messages {
			joined.WriteString(m.Text)
			count++
		}
	}
	if count < 2 || strings.Contains(joined.String(), "unitSecret71!") || !strings.Contains(joined.String(), "[REDACTED], потом поменяю") {
		t.Fatal("password split across exposed fragments")
	}
}

func TestThreadModelConfigurationIsNeverInventedAsPerTurnTelemetry(t *testing.T) {
	thread := codex.Thread{ID: "models", CWD: "/repo", CreatedAt: 1793318400, Model: "thread-model", ModelProvider: "provider", CLIVersion: "1.2.3"}
	entries := []codex.Entry{item("answer", "agentMessage", map[string]any{"text": "Visible answer"}), item("edit", "fileChange", map[string]any{"status": "completed", "changes": []any{map[string]any{"path": "/repo/a.go", "diff": "+line"}, map[string]any{"path": "/repo/.git/config", "diff": "blocked"}}})}
	h, err := codex.Normalize(thread, entries, "hook-current-model", "hook-version", "/repo")
	if err != nil {
		t.Fatal(err)
	}
	observation := h.Windows[0].Observation
	if observation.Agent.Model != "thread-model" || observation.Agent.Provider != "provider" || observation.Agent.ModelBasis != "thread_configuration" || observation.Agent.Version != "1.2.3" {
		t.Fatal(observation.Agent)
	}
	for _, m := range observation.Messages {
		if m.Model != "" || m.TurnID != "turn" || m.NativeID == "" {
			t.Fatal("configuration presented as execution", m)
		}
	}
	changes := observation.Messages[1].Changes
	if len(changes) != 1 || changes[0].Path != "a.go" {
		t.Fatal("successful in-repository file evidence missing", changes)
	}
}
