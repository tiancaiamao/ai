package main

import (
	"testing"

	"github.com/tiancaiamao/ai/pkg/agent"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/rpc"
	"github.com/tiancaiamao/ai/pkg/session"
	"github.com/tiancaiamao/ai/pkg/traceevent"
)

// newTestAppForSlash creates an rpcApp suitable for testing slash commands
// that don't require an LLM connection.
func newTestAppForSlash(t *testing.T) *rpcApp {
	t.Helper()
	model := llm.Model{ID: "test-model", ContextWindow: 4096}
	agentCtx := agentctx.NewAgentContext("test system prompt")
	ag := agent.NewAgentFromConfigWithContext(model, "test-key", agentCtx, agent.DefaultLoopConfig())

	t.Cleanup(func() {
		traceevent.SetActiveTraceBuf(nil)
	})

	return &rpcApp{
		ag:                   ag,
		steeringMode:         "all",
		followUpMode:         "all",
		showThinking:         true,
		showTools:            true,
		showPrefix:           true,
		busyMode:             "steer",
		currentThinkingLevel: "off",
		server:               rpc.NewServer(),
	}
}

// --- /toggle ---

func TestHandleToggle_Thinking(t *testing.T) {
	app := newTestAppForSlash(t)
	result, err := app.handleToggle("thinking")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := result.(map[string]any)
	if m["setting"] != "thinking" {
		t.Errorf("setting = %v, want thinking", m["setting"])
	}
	if m["value"] != false {
		t.Errorf("value = %v, want false", m["value"])
	}
}

func TestHandleToggle_Prefix(t *testing.T) {
	app := newTestAppForSlash(t)
	app.showPrefix = false
	_, err := app.handleToggle("prefix")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if app.showPrefix != true {
		t.Error("showPrefix should be true after toggle from false")
	}
}

func TestHandleToggle_Tools(t *testing.T) {
	app := newTestAppForSlash(t)
	_, err := app.handleToggle("tools")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if app.showTools != false {
		t.Error("showTools should be false after toggle")
	}
}

func TestHandleToggle_Invalid(t *testing.T) {
	app := newTestAppForSlash(t)
	_, err := app.handleToggle("unknown")
	if err == nil {
		t.Fatal("expected error for unknown toggle target")
	}
}

// --- /show settings ---

func TestHandleShow_DefaultSettings(t *testing.T) {
	app := newTestAppForSlash(t)
	result, err := app.handleShow("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := result.(map[string]any)
	if m["type"] != "settings" {
		t.Errorf("type = %v, want settings", m["type"])
	}
	data := m["data"].(map[string]any)
	if data["auto-compaction"] != "off" {
		t.Errorf("auto-compaction = %v, want off", data["auto-compaction"])
	}
	if data["show-thinking"] != "on" {
		t.Errorf("show-thinking = %v, want on", data["show-thinking"])
	}
}

func TestHandleShow_ExplicitSettings(t *testing.T) {
	app := newTestAppForSlash(t)
	result, err := app.handleShow("settings")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := result.(map[string]any)
	if m["type"] != "settings" {
		t.Errorf("type = %v, want settings", m["type"])
	}
}

func TestHandleShow_InvalidSubcmd(t *testing.T) {
	app := newTestAppForSlash(t)
	_, err := app.handleShow("invalid")
	if err == nil {
		t.Fatal("expected error for invalid show subcommand")
	}
}

// --- /set busy-mode ---

func TestHandleSet_BusyMode(t *testing.T) {
	app := newTestAppForSlash(t)
	result, err := app.handleSet("busy-mode reject", nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := result.(map[string]any)
	if m["value"] != "reject" {
		t.Errorf("value = %v, want reject", m["value"])
	}
	if app.busyMode != "reject" {
		t.Errorf("busyMode = %s, want reject", app.busyMode)
	}
}

func TestHandleSet_BusyMode_Invalid(t *testing.T) {
	app := newTestAppForSlash(t)
	_, err := app.handleSet("busy-mode invalid", nil, nil, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for invalid busy-mode")
	}
}

func TestHandleSet_PrefixDisplay(t *testing.T) {
	app := newTestAppForSlash(t)
	_, err := app.handleSet("prefix-display off", nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if app.showPrefix != false {
		t.Error("showPrefix should be false after 'off'")
	}
	_, err = app.handleSet("prefix-display on", nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if app.showPrefix != true {
		t.Error("showPrefix should be true after 'on'")
	}
	_, err = app.handleSet("prefix-display toggle", nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if app.showPrefix != false {
		t.Error("showPrefix should be false after 'toggle' from true")
	}
}

func TestHandleSet_PrefixDisplay_Invalid(t *testing.T) {
	app := newTestAppForSlash(t)
	_, err := app.handleSet("prefix-display invalid", nil, nil, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for invalid value")
	}
}

func TestHandleSet_ThinkingDisplay(t *testing.T) {
	app := newTestAppForSlash(t)
	_, err := app.handleSet("thinking-display off", nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if app.showThinking != false {
		t.Error("showThinking should be false")
	}
}

func TestHandleSet_ToolsDisplay(t *testing.T) {
	app := newTestAppForSlash(t)
	_, err := app.handleSet("tools-display toggle", nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if app.showTools != false {
		t.Error("showTools should be false after toggle from true")
	}
}

func TestHandleSet_Help(t *testing.T) {
	app := newTestAppForSlash(t)
	result, err := app.handleSet("help", nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := result.(map[string]any)
	if m["usage"] == nil {
		t.Error("expected usage key in help result")
	}
}

func TestHandleSet_UnknownKey(t *testing.T) {
	app := newTestAppForSlash(t)
	_, err := app.handleSet("unknown-key value", nil, nil, nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for unknown set key")
	}
}

func TestHandleSet_EmptyArgs(t *testing.T) {
	app := newTestAppForSlash(t)
	result, err := app.handleSet("", nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := result.(map[string]any)
	if m["usage"] == nil {
		t.Error("expected usage key for empty args")
	}
}

// --- /set steering-mode ---

func TestHandleSet_SteeringMode(t *testing.T) {
	validModes := map[string]bool{"all": true, "immediate": true, "one-at-a-time": true}
	app := newTestAppForSlash(t)
	_, err := app.handleSet("steering-mode one-at-a-time", nil, nil, validModes, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if app.steeringMode != "one-at-a-time" {
		t.Errorf("steeringMode = %s, want one-at-a-time", app.steeringMode)
	}
}

func TestHandleSet_SteeringMode_Invalid(t *testing.T) {
	validModes := map[string]bool{"all": true, "immediate": true, "one-at-a-time": true}
	app := newTestAppForSlash(t)
	_, err := app.handleSet("steering-mode invalid", nil, nil, validModes, nil, nil)
	if err == nil {
		t.Fatal("expected error for invalid steering mode")
	}
}

// --- /set follow-up-mode ---

func TestHandleSet_FollowUpMode(t *testing.T) {
	validModes := map[string]bool{"all": true, "immediate": true, "one-at-a-time": true}
	app := newTestAppForSlash(t)
	_, err := app.handleSet("follow-up-mode one-at-a-time", nil, nil, nil, validModes, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if app.followUpMode != "one-at-a-time" {
		t.Errorf("followUpMode = %s, want one-at-a-time", app.followUpMode)
	}
}

func TestHandleSet_FollowUpMode_Invalid(t *testing.T) {
	validModes := map[string]bool{"all": true, "immediate": true, "one-at-a-time": true}
	app := newTestAppForSlash(t)
	_, err := app.handleSet("follow-up-mode invalid", nil, nil, nil, validModes, nil)
	if err == nil {
		t.Fatal("expected error for invalid follow-up mode")
	}
}

// --- /export_html ---

func TestHandleExportHTML(t *testing.T) {
	app := newTestAppForSlash(t)
	_, err := app.handleExportHTML("/tmp/test.html")
	if err == nil {
		t.Fatal("expected error for export_html (not supported)")
	}
}

// --- /messages handler ---

func TestHandleGetMessages_DefaultCount(t *testing.T) {
	app := newTestAppForSlash(t)
	result, err := app.handleGetMessages("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := result.(rpc.MessagesResult)
	if m.Total != 0 {
		t.Errorf("Total = %d, want 0", m.Total)
	}
}

func TestHandleGetMessages_WithCount(t *testing.T) {
	app := newTestAppForSlash(t)
	result, err := app.handleGetMessages("5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	m := result.(rpc.MessagesResult)
	if m.Total != 0 {
		t.Errorf("Total = %d, want 0", m.Total)
	}
}

// --- /get_last_assistant_text ---

func TestHandleGetLastAssistantText_Empty(t *testing.T) {
	app := newTestAppForSlash(t)
	result, err := app.handleGetLastAssistantText("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "" {
		t.Errorf("text = %v, want empty string", result)
	}
}

// --- helper functions ---

func TestTruncateText(t *testing.T) {
	if truncateText("hello", 10) != "hello" {
		t.Error("should not truncate short text")
	}
	if truncateText("hello world", 8) != "hello..." {
		t.Errorf("got %q", truncateText("hello world", 8))
	}
	if truncateText("hi", 0) != "" {
		t.Error("limit 0 should return empty")
	}
	if truncateText("abc", 2) != "ab" {
		t.Error("limit <= 3 should return raw slice")
	}
}

func TestFormatIntOrUnknown(t *testing.T) {
	if formatIntOrUnknown(0) != "unknown" {
		t.Error("0 should be unknown")
	}
	if formatIntOrUnknown(-1) != "unknown" {
		t.Error("-1 should be unknown")
	}
	if formatIntOrUnknown(42) != "42" {
		t.Error("42 should be 42")
	}
}

func TestFormatLimit(t *testing.T) {
	if formatLimit(0) != "disabled" {
		t.Error("0 should be disabled")
	}
	if formatLimit(-1) != "disabled" {
		t.Error("-1 should be disabled")
	}
	if formatLimit(100) != "100" {
		t.Error("100 should be 100")
	}
}

func TestFormatTokenLimit(t *testing.T) {
	if formatTokenLimit(nil) != "unknown" {
		t.Error("nil should be unknown")
	}
	state := &rpc.CompactionState{TokenLimit: 0}
	if formatTokenLimit(state) != "unknown" {
		t.Error("TokenLimit 0 should be unknown")
	}
	state.TokenLimit = 8000
	state.TokenLimitSource = "context_window"
	result := formatTokenLimit(state)
	if result != "8000 (context-window)" {
		t.Errorf("got %q", result)
	}
}

func TestFormatTokenLimitSource(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"context_window", "context-window"},
		{"max_tokens", "max-tokens"},
		{"none", ""},
		{"custom", "custom"},
		{"  spaced  ", "spaced"},
	}
	for _, tt := range tests {
		got := formatTokenLimitSource(tt.input)
		if got != tt.want {
			t.Errorf("formatTokenLimitSource(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestCollectSessionUsage_Empty(t *testing.T) {
	userCount, assistantCount, toolCalls, toolResults, tokens, cost := collectSessionUsage(nil)
	if userCount != 0 || assistantCount != 0 || toolCalls != 0 || toolResults != 0 {
		t.Errorf("counts should be 0")
	}
	_ = tokens
	_ = cost
}

func TestCollectSessionUsage_Basic(t *testing.T) {
	messages := []agentctx.AgentMessage{
		{Role: "user"},
		{Role: "assistant"},
		{Role: "toolResult"},
	}
	userCount, assistantCount, _, toolResults, _, _ := collectSessionUsage(messages)
	if userCount != 1 {
		t.Errorf("userCount = %d, want 1", userCount)
	}
	if assistantCount != 1 {
		t.Errorf("assistantCount = %d, want 1", assistantCount)
	}
	if toolResults != 1 {
		t.Errorf("toolResults = %d, want 1", toolResults)
	}
}

// --- treeEntryLabel ---

func TestTreeEntryLabel_Message(t *testing.T) {
	msg := &agentctx.AgentMessage{Role: "user", Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "hello"}}}
	entry := session.SessionEntry{Type: session.EntryTypeMessage, Message: msg}
	role, text := treeEntryLabel(entry)
	if role != "user" || text != "hello" {
		t.Errorf("role=%q text=%q", role, text)
	}
}

func TestTreeEntryLabel_Compaction(t *testing.T) {
	entry := session.SessionEntry{Type: session.EntryTypeCompaction, Summary: "summarized"}
	role, text := treeEntryLabel(entry)
	if role != "compaction" || text != "summarized" {
		t.Errorf("role=%q text=%q", role, text)
	}
}

func TestTreeEntryLabel_EmptyMessage(t *testing.T) {
	entry := session.SessionEntry{Type: session.EntryTypeMessage, Message: nil}
	role, text := treeEntryLabel(entry)
	if role != "message" || text != "" {
		t.Errorf("role=%q text=%q", role, text)
	}
}

func TestTreeEntryLabel_ToolResultNoText(t *testing.T) {
	msg := &agentctx.AgentMessage{Role: "toolResult", ToolName: "bash"}
	entry := session.SessionEntry{Type: session.EntryTypeMessage, Message: msg}
	_, text := treeEntryLabel(entry)
	if text != "bash result" {
		t.Errorf("text=%q, want 'bash result'", text)
	}
}

func TestTreeEntryLabel_AssistantToolCall(t *testing.T) {
	msg := &agentctx.AgentMessage{
		Role: "assistant",
		Content: []agentctx.ContentBlock{
			agentctx.ToolCallContent{Type: "toolCall", Name: "read"},
		},
	}
	entry := session.SessionEntry{Type: session.EntryTypeMessage, Message: msg}
	_, text := treeEntryLabel(entry)
	if text != "tool call" {
		t.Errorf("text=%q, want 'tool call'", text)
	}
}

func TestTreeEntryLabel_SessionInfo(t *testing.T) {
	entry := session.SessionEntry{Type: session.EntryTypeSessionInfo, Name: "my-session"}
	role, text := treeEntryLabel(entry)
	if role != "session info" || text != "my-session" {
		t.Errorf("role=%q text=%q", role, text)
	}
}

func TestTreeEntryLabel_BranchSummary(t *testing.T) {
	entry := session.SessionEntry{Type: session.EntryTypeBranchSummary, Summary: "branch summary"}
	role, text := treeEntryLabel(entry)
	if role != "branch summary" || text != "branch summary" {
		t.Errorf("role=%q text=%q", role, text)
	}
}

// --- buildTreeEntries ---

func TestBuildTreeEntries_Empty(t *testing.T) {
	result := buildTreeEntries(nil, nil)
	if result != nil {
		t.Error("expected nil for empty entries")
	}
}

func TestBuildTreeEntries_SingleRoot(t *testing.T) {
	msg := &agentctx.AgentMessage{Role: "user", Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "hi"}}}
	entries := []session.SessionEntry{
		{ID: "root", Type: session.EntryTypeMessage, Message: msg},
	}
	result := buildTreeEntries(entries, nil)
	if len(result) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(result))
	}
	if result[0].Role != "user" {
		t.Errorf("role=%q, want user", result[0].Role)
	}
	if result[0].Depth != 0 {
		t.Errorf("depth=%d, want 0", result[0].Depth)
	}
}

func TestBuildTreeEntries_ParentChild(t *testing.T) {
	parentID := "p1"
	msg := &agentctx.AgentMessage{Role: "user", Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "parent"}}}
	childMsg := &agentctx.AgentMessage{Role: "assistant", Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "child"}}}
	entries := []session.SessionEntry{
		{ID: "p1", Type: session.EntryTypeMessage, Message: msg},
		{ID: "c1", Type: session.EntryTypeMessage, Message: childMsg, ParentID: &parentID},
	}
	leafID := "c1"
	result := buildTreeEntries(entries, &leafID)
	if len(result) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(result))
	}
	if result[0].Depth != 0 {
		t.Errorf("parent depth=%d, want 0", result[0].Depth)
	}
	if result[1].Depth != 1 {
		t.Errorf("child depth=%d, want 1", result[1].Depth)
	}
	if !result[1].Leaf {
		t.Error("child should be leaf")
	}
}
