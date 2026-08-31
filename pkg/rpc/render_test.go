package rpc

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tiancaiamao/ai/pkg/command"
	"github.com/tiancaiamao/ai/pkg/config"
	"github.com/tiancaiamao/ai/pkg/session"
)

// formatData renders a result through the shared dispatcher, mirroring how
// ACP (named) and the TUI event stream (unnamed) call it.
func formatData(command string, data any) string {
	return FormatCommandResult(command, data)
}

func TestFormatCommandResultSession(t *testing.T) {
	out := formatData("session", &SessionState{
		Model:       &config.ModelInfo{ID: "glm-4.6", Provider: "zai", Name: "GLM-4.6"},
		SessionID:   "abcdefgh12345678",
		IsStreaming: true,
	})
	for _, want := range []string{"model: zai/glm-4.6", "id: abcdefgh12345678", "streaming: on"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q, got:\n%s", want, out)
		}
	}

	// Enriched fields
	enriched := &SessionState{
		Model:                 &config.ModelInfo{ID: "glm-4.6", Provider: "zai", Name: "GLM-4.6"},
		SessionID:             "abcdefgh12345678",
		SessionName:           "fix-bug",
		AIWorkingDir:          "/home/user/proj",
		ThinkingLevel:         "medium",
		MessageCount:          12,
		PendingMessageCount:   2,
		AutoCompactionEnabled: false,
	}
	out = formatData("session", enriched)
	for _, want := range []string{
		"name: fix-bug", "ai-cwd: /home/user/proj", "thinking-level: medium",
		"messages: 12", "pending: 2", "auto-compaction: off",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q, got:\n%s", want, out)
		}
	}

	// Shape mismatch → no render (caller falls back to JSON).
	if out := formatData("session", map[string]any{"bogus": true}); out != "" {
		t.Errorf("expected empty, got:\n%s", out)
	}
}

func TestFormatCommandResultContext(t *testing.T) {
	state := &SessionState{
		Model:     &config.ModelInfo{ID: "glm-4.6", Provider: "zai", Name: "GLM-4.6"},
		SessionID: "abcdefgh12345678",
	}
	stats := &SessionStats{
		UserMessages:      3,
		AssistantMessages: 2,
		ToolCalls:         1,
		Tokens:            SessionTokenStats{Input: 100, Output: 50, CacheRead: 10, CacheWrite: 20, Total: 180},
	}
	models := map[string]any{
		"models":       []config.ModelInfo{{ID: "glm-4.5-air", Provider: "zai"}, {ID: "glm-4.6", Provider: "zai"}},
		"currentIndex": 1,
	}
	out := formatData("context", map[string]any{"state": state, "stats": stats, "models": models})
	for _, want := range []string{"Context Usage", "zai/glm-4.6", "Session Stats", "Messages: 0 total (user 3, assistant 2)"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q, got:\n%s", want, out)
		}
	}
	// The model list belongs to /model, not /context.
	if strings.Contains(out, "glm-4.5-air") || strings.Contains(out, "marks current") {
		t.Errorf("/context must not render the model list, got:\n%s", out)
	}

	// Nil stats → no render.
	if out := formatData("context", map[string]any{"state": state}); out != "" {
		t.Errorf("expected empty, got:\n%s", out)
	}
}

func TestFormatCommandResultResume(t *testing.T) {
	list := map[string]any{
		"sessions": []session.SessionMeta{
			{ID: "1111111111111111", Name: "fix-login", MessageCount: 42, UpdatedAt: time.Date(2026, 8, 26, 15, 4, 0, 0, time.Local)},
			{ID: "2222222222222222", Name: "default", MessageCount: 3, UpdatedAt: time.Date(2026, 8, 25, 9, 0, 0, 0, time.Local)},
		},
	}
	out := formatData("resume", list)
	for _, want := range []string{
		"Available Sessions",
		"0: fix-login (id: 1111111111111111)",
		"updated: 2026-08-26T15...  messages: 42",
		"1: default (id: 2222222222222222)",
		"/resume <index|id|path>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q, got:\n%s", want, out)
		}
	}

	// Switch confirmation
	out = formatData("resume", map[string]any{"sessionId": "1111222233334444", "sessionName": "fix-bug"})
	if !strings.Contains(out, "Switched to session fix-bug (11112222)") {
		t.Errorf("unexpected switch confirmation:\n%s", out)
	}

	// Empty list via /sessions
	if out := formatData("sessions", map[string]any{"sessions": []session.SessionMeta{}}); out != "No sessions found" {
		t.Errorf("expected 'No sessions found', got %q", out)
	}
}

func TestFormatCommandResultShow(t *testing.T) {
	out := formatData("show", map[string]any{
		"type": "settings",
		"data": map[string]any{"model": "zai/glm-4.6", "prefix": "on"},
	})
	for _, want := range []string{
		"Display Settings:",
		"  model: zai/glm-4.6",
		"  prefix: on",
		"  thinking-level: unknown",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q, got:\n%s", want, out)
		}
	}
	// Non-settings payload → no render.
	if out := formatData("show", map[string]any{"type": "other"}); out != "" {
		t.Errorf("expected empty, got:\n%s", out)
	}
}

func TestFormatCommandResultHelpAndSkills(t *testing.T) {
	out := formatData("help", map[string]any{
		"commands": []command.CommandInfo{
			{Name: "help", Description: "Show available slash commands"},
			{Name: "session", Description: "Get the current agent state"},
		},
	})
	for _, want := range []string{
		"Commands:",
		"[slash] help - Show available slash commands",
		"[slash] session - Get the current agent state",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q, got:\n%s", want, out)
		}
	}

	out = formatData("skills", map[string]any{
		"commands": []SlashCommand{{Name: "/skill:review", Description: "Review code", Source: "skill"}},
	})
	for _, want := range []string{"Commands:", "[skill] /skill:review - Review code"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q, got:\n%s", want, out)
		}
	}
	if out := formatData("skills", map[string]any{"commands": []SlashCommand{}}); !strings.Contains(out, "no commands") {
		t.Errorf("expected 'no commands', got %q", out)
	}
}

func TestFormatCommandResultModel(t *testing.T) {
	models := map[string]any{
		"models":       []config.ModelInfo{{ID: "glm-4.5-air", Provider: "zai"}, {ID: "glm-4.6", Provider: "zai"}},
		"currentIndex": 1,
	}
	out := formatData("model", models)
	for _, want := range []string{"Available Models", "Usage: /model <index>", "[current]"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q, got:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "1: zai/glm-4.6") {
		t.Errorf("expected indexed current model line, got:\n%s", out)
	}

	// Switched-model confirmation.
	out = formatData("cycle_model", CycleModelResult{
		Model: config.ModelInfo{ID: "m2", Provider: "p", Name: "Two"},
	})
	if !strings.Contains(out, "Model: p/Two (m2)") {
		t.Errorf("unexpected switched-model output:\n%s", out)
	}

	// Wrong shape → no render.
	if out := formatData("model", map[string]any{"models": "nope"}); out != "" {
		t.Errorf("expected empty, got:\n%s", out)
	}
}

func TestFormatCommandResultUnnamedShapes(t *testing.T) {
	cases := []struct {
		name string
		data any
		want string
	}{
		{"thinking level", map[string]any{"level": "low"}, "Thinking level: low"},
		{"compact message", map[string]any{"message": "compacted"}, "compacted"},
		{"trace events", map[string]any{"events": []string{"e1", "e2"}}, "trace events set to: e1, e2"},
		{"tree", map[string]any{"entries": []TreeEntry{{EntryID: "e1", Depth: 0, Text: "root"}}}, "[e1] root"},
		{"new session skipped", map[string]any{"sessionId": "abc", "cancelled": false}, ""},
		{"resume switch via sniff", map[string]any{"sessionId": "1111222233334444", "sessionName": "fix-bug"}, "Switched to session fix-bug (11112222)"},
		{"session stats", SessionStats{SessionID: "s1", TotalMessages: 10, UserMessages: 4, AssistantMessages: 5}, "messages: 10 (user 4, assistant 5)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatData("", tc.data)
			if tc.want == "" {
				if got != "" {
					t.Errorf("expected empty, got %q", got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Errorf("output missing %q, got:\n%s", tc.want, got)
			}
		})
	}
}

func TestFormatCommandResultMessages(t *testing.T) {
	newFmt := map[string]any{"total": 5, "showing": 3, "messages": []map[string]any{
		{"index": 2, "role": "user", "preview": "Hello world"},
		{"index": 3, "role": "assistant", "preview": "Let me check", "toolCalls": []string{"bash", "read"}},
	}}
	out := formatData("messages", newFmt)
	for _, want := range []string{"Messages (last 3 of 5):", "Hello world", "tools: bash, read"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q, got:\n%s", want, out)
		}
	}

	// Legacy {messages: [...]}
	out = formatData("", map[string]any{"messages": []map[string]any{
		{"role": "user", "content": "Hello"}, {"role": "assistant", "content": "World"},
	}})
	if !strings.Contains(out, "Hello") || !strings.Contains(out, "World") {
		t.Errorf("unexpected legacy messages output:\n%s", out)
	}

	// Legacy top-level array (direct renderer call).
	raw, _ := json.Marshal([]map[string]any{{"role": "user", "content": "hi"}})
	if out := renderMessagesText(raw); !strings.Contains(out, "hi") {
		t.Errorf("unexpected array messages output: %q", out)
	}

	// Empty list.
	if out := formatData("messages", map[string]any{"total": 0, "showing": 0, "messages": []any{}}); !strings.Contains(out, "no messages") {
		t.Errorf("expected 'no messages', got %q", out)
	}
}

func TestFormatCommandResultFallbacks(t *testing.T) {
	if got := FormatCommandResult("", nil); got != "" {
		t.Errorf("expected empty for nil data, got %q", got)
	}
	if got := formatData("set", map[string]any{"foo": 1}); got != "" {
		t.Errorf("unrendered command should yield empty, got:\n%s", got)
	}
	if got := formatData("prompt", []string{"not", "a", "map"}); got != "" {
		t.Errorf("non-map payload should yield empty, got %q", got)
	}
}

func TestFormatCommandResultSet(t *testing.T) {
	// /set <key> <value> confirmation via named dispatch.
	if got := formatData("set", map[string]any{"setting": "thinking-level", "value": "low"}); got != "thinking-level: low" {
		t.Errorf("set confirmation mismatch, got %q", got)
	}
	// /toggle shares the same shape and renderer.
	if got := formatData("toggle", map[string]any{"setting": "tools", "value": true}); got != "tools: true" {
		t.Errorf("toggle confirmation mismatch, got %q", got)
	}
	// Unnamed /set answers (command arrives as "prompt") via shape sniffing.
	if got := formatData("prompt", map[string]any{"setting": "session-name", "value": "bugfix"}); got != "session-name: bugfix" {
		t.Errorf("shape-sniffed set confirmation mismatch, got %q", got)
	}
	// /set help usage listing.
	got := formatData("set", map[string]any{
		"usage":    "/set <key> [value]",
		"settings": []string{"auto-retry <on|off>", "thinking-level <off|low|medium|high>"},
	})
	want := "usage: /set <key> [value]\nsettings:\n  auto-retry <on|off>\n  thinking-level <off|low|medium|high>"
	if got != want {
		t.Errorf("set usage mismatch:\ngot:\n%s\nwant:\n%s", got, want)
	}
}
