package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/tiancaiamao/ai/pkg/agent"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

func TestParseSystemPrompt(t *testing.T) {
	// Test 1: Empty string
	result := parseSystemPrompt("")
	if result != "" {
		t.Errorf("Expected empty string, got %q", result)
	}

	// Test 2: Plain text (no @ prefix)
	plainText := "You are a helpful assistant"
	result = parseSystemPrompt(plainText)
	if result != plainText {
		t.Errorf("Expected %q, got %q", plainText, result)
	}

	// Test 3: @ prefix with empty path
	result = parseSystemPrompt("@")
	if result != "" {
		t.Errorf("Expected empty string for @ with no path, got %q", result)
	}

	// Test 4: @ prefix with whitespace only
	result = parseSystemPrompt("@   ")
	if result != "" {
		t.Errorf("Expected empty string for @ with whitespace, got %q", result)
	}

	// Test 5: @ prefix with valid file
	tmpFile := t.TempDir() + "/test_prompt.md"
	content := "You are a test assistant"
	if err := os.WriteFile(tmpFile, []byte(content), 0644); err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	result = parseSystemPrompt("@" + tmpFile)
	if result != content {
		t.Errorf("Expected %q, got %q", content, result)
	}

	// Test 6: @ prefix with non-existent file
	result = parseSystemPrompt("@/nonexistent/path/to/file.md")
	if result != "" {
		t.Errorf("Expected empty string for non-existent file, got %q", result)
	}

	// Test 7: Large file (truncate to 64KB)
	largeFile := t.TempDir() + "/large_prompt.md"
	largeContent := make([]byte, 70*1024) // 70KB
	for i := range largeContent {
		largeContent[i] = 'a'
	}
	if err := os.WriteFile(largeFile, largeContent, 0644); err != nil {
		t.Fatalf("Failed to create large temp file: %v", err)
	}
	result = parseSystemPrompt("@" + largeFile)
	if len(result) != 64*1024 {
		t.Errorf("Expected 64KB, got %d bytes", len(result))
	}

	// Test 8: @ with leading whitespace in path
	tmpFile2 := t.TempDir() + "/test_prompt2.md"
	if err := os.WriteFile(tmpFile2, []byte("Content 2"), 0644); err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	result = parseSystemPrompt("@   " + tmpFile2)
	if result != "Content 2" {
		t.Errorf("Expected 'Content 2', got %q", result)
	}
}

func TestBuildWinReplConfig_Defaults(t *testing.T) {
	cfg := buildWinReplConfig("", "/tmp/ai.log")

	if cfg.WindowName != "+ai" {
		t.Fatalf("expected default window name +ai, got %q", cfg.WindowName)
	}

	if !cfg.EnableKeyboardExecute {
		t.Fatal("expected keyboard execution to be enabled")
	}

	if !cfg.EnableExecute {
		t.Fatal("expected execute events to be enabled")
	}

	if cfg.SendPrefix != sendPrefix {
		t.Fatalf("expected send prefix %q, got %q", sendPrefix, cfg.SendPrefix)
	}

	if cfg.LogPath != "/tmp/ai.log" {
		t.Fatalf("expected log path /tmp/ai.log, got %q", cfg.LogPath)
	}

	if !strings.Contains(cfg.WelcomeMessage, "press Enter") {
		t.Fatalf("welcome message should mention Enter execution, got %q", cfg.WelcomeMessage)
	}
}

func TestBuildWinReplConfig_CustomWindowName(t *testing.T) {
	cfg := buildWinReplConfig("my-win", "")
	if cfg.WindowName != "my-win" {
		t.Fatalf("expected custom window name to be preserved, got %q", cfg.WindowName)
	}
}

type capturedEventEmitter struct {
	events []any
}

func (c *capturedEventEmitter) EmitEvent(event any) {
	c.events = append(c.events, event)
}

func TestRPCEventEmitterAdapter_ForwardFullAgentEvent(t *testing.T) {
	sink := &capturedEventEmitter{}
	adapter := &rpcEventEmitterAdapter{server: sink}

	input := agent.AgentEvent{
		Type: agent.EventMessageUpdate,
		AssistantMessageEvent: agent.AssistantMessageEvent{
			Type:  "text_delta",
			Delta: "hello",
		},
		ToolName:   "read",
		ToolCallID: "call-1",
	}

	adapter.Emit(input)

	if len(sink.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(sink.events))
	}

	payload, err := json.Marshal(sink.events[0])
	if err != nil {
		t.Fatalf("marshal emitted event: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		t.Fatalf("unmarshal emitted event: %v", err)
	}

	if raw["type"] != agent.EventMessageUpdate {
		t.Fatalf("expected type %q, got %#v", agent.EventMessageUpdate, raw["type"])
	}

	assistantRaw, ok := raw["assistantMessageEvent"].(map[string]any)
	if !ok {
		t.Fatalf("expected assistantMessageEvent object, got %#v", raw["assistantMessageEvent"])
	}

	if assistantRaw["type"] != "text_delta" {
		t.Fatalf("expected assistant event type text_delta, got %#v", assistantRaw["type"])
	}
	if assistantRaw["delta"] != "hello" {
		t.Fatalf("expected assistant delta hello, got %#v", assistantRaw["delta"])
	}
}

func TestExtractLastAssistantText(t *testing.T) {
	data := []byte(`{
		"messages": [
			{"role":"user","content":[{"type":"text","text":"u"}],"timestamp":1},
			{"role":"assistant","content":[{"type":"text","text":"first"}],"timestamp":2},
			{"role":"assistant","content":[{"type":"text","text":"second"}],"timestamp":3}
		]
	}`)

	got := extractLastAssistantText(data)
	if got != "second" {
		t.Fatalf("expected second, got %q", got)
	}
}

func TestUpdateHeadlessStreamStateFromEvent(t *testing.T) {
	state := &headlessStreamState{}

	msg := agentctx.AgentMessage{
		Role:      "assistant",
		Content:   []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "from message"}},
		Timestamp: 1,
	}
	line1, err := json.Marshal(map[string]any{
		"type":    "message_end",
		"message": msg,
	})
	if err != nil {
		t.Fatalf("marshal message event: %v", err)
	}

	updateHeadlessStreamStateFromEvent(line1, state)
	if state.lastAssistantText != "from message" {
		t.Fatalf("expected message text, got %q", state.lastAssistantText)
	}

	line2, err := json.Marshal(map[string]any{
		"type": "message_update",
		"assistantMessageEvent": map[string]any{
			"type":  "text_delta",
			"delta": "from delta",
		},
	})
	if err != nil {
		t.Fatalf("marshal delta event: %v", err)
	}

	updateHeadlessStreamStateFromEvent(line2, state)
	if state.lastAssistantText != "from delta" {
		t.Fatalf("expected delta text, got %q", state.lastAssistantText)
	}
}
