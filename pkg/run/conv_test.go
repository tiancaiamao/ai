package run

import (
	"strings"
	"testing"
)

func TestParseEvent_Empty(t *testing.T) {
	got := ParseEvent("")
	if got != nil {
		t.Fatalf("expected nil for empty string, got %+v", got)
	}
}

func TestParseEvent_Whitespace(t *testing.T) {
	got := ParseEvent("   ")
	if got != nil {
		t.Fatalf("expected nil for whitespace, got %+v", got)
	}
}

func TestParseEvent_InvalidJSON(t *testing.T) {
	got := ParseEvent("not json")
	if got != nil {
		t.Fatalf("expected nil for invalid JSON, got %+v", got)
	}
}

func TestParseEvent_UnknownType(t *testing.T) {
	got := ParseEvent(`{"type":"something_else"}`)
	if got != nil {
		t.Fatalf("expected nil for unknown type, got %+v", got)
	}
}

// --- Text delta extraction ---

func TestParseEvent_MessageUpdate_DataFormat(t *testing.T) {
	evt := ParseEvent(`{"type":"message_update","data":{"text_delta":"Hello"}}`)
	if evt == nil {
		t.Fatal("expected non-nil event")
	}
	if evt.Kind != KindText {
		t.Fatalf("expected KindText, got %s", evt.Kind)
	}
	if evt.Text != "Hello" {
		t.Fatalf("expected 'Hello', got %q", evt.Text)
	}
	if evt.Raw != "Hello" {
		t.Fatalf("expected Raw 'Hello', got %q", evt.Raw)
	}
}

func TestParseEvent_MessageUpdate_AssistantMessageEvent(t *testing.T) {
	evt := ParseEvent(`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"World"}}`)
	if evt == nil {
		t.Fatal("expected non-nil event")
	}
	if evt.Kind != KindText {
		t.Fatalf("expected KindText, got %s", evt.Kind)
	}
	if evt.Text != "World" {
		t.Fatalf("expected 'World', got %q", evt.Text)
	}
}

func TestParseEvent_MessageUpdate_TextStart(t *testing.T) {
	evt := ParseEvent(`{"type":"message_update","assistantMessageEvent":{"type":"text_start","delta":"starting"}}`)
	// text_start is not a recognized sub-type (only text_delta/thinking_delta handled)
	if evt != nil {
		t.Fatalf("expected nil for unrecognized message type 'text_start', got %+v", evt)
	}
}

func TestParseEvent_MessageUpdate_EmptyDelta(t *testing.T) {
	evt := ParseEvent(`{"type":"message_update","data":{"text_delta":""}}`)
	if evt != nil {
		t.Fatalf("expected nil for empty text delta, got %+v", evt)
	}
}

func TestParseEvent_MessageUpdate_NoDeltaField(t *testing.T) {
	evt := ParseEvent(`{"type":"message_update","assistantMessageEvent":{"type":"content_block"}}`)
	if evt != nil {
		t.Fatalf("expected nil for non-text event type, got %+v", evt)
	}
}

// --- Tool execution start ---

func TestParseEvent_ToolExecutionStart(t *testing.T) {
	evt := ParseEvent(`{"type":"tool_execution_start","toolName":"read","args":{"path":"main.go"}}`)
	if evt == nil {
		t.Fatal("expected non-nil event")
	}
	if evt.Kind != KindTool {
		t.Fatalf("expected KindTool, got %s", evt.Kind)
	}
	if evt.Tool != "read" {
		t.Fatalf("expected tool 'read', got %q", evt.Tool)
	}
	if !strings.Contains(evt.Detail, "path=main.go") {
		t.Fatalf("expected detail to contain 'path=main.go', got %q", evt.Detail)
	}
		if !strings.Contains(evt.Text, "tool read") {
		t.Fatalf("expected text to contain 'tool read', got %q", evt.Text)
	}
}

func TestParseEvent_ToolExecutionStart_Legacy(t *testing.T) {
	evt := ParseEvent(`{"type":"tool_execution_start","data":{"tool":"bash","args":{"command":"ls -la"}}}`)
	if evt == nil {
		t.Fatal("expected non-nil event")
	}
	if evt.Tool != "bash" {
		t.Fatalf("expected tool 'bash', got %q", evt.Tool)
	}
	if !strings.Contains(evt.Detail, "command=ls -la") {
		t.Fatalf("expected detail to contain command, got %q", evt.Detail)
	}
}

func TestParseEvent_ToolExecutionStart_NoToolName(t *testing.T) {
	evt := ParseEvent(`{"type":"tool_execution_start"}`)
	if evt != nil {
		t.Fatalf("expected nil for tool_execution_start without tool name, got %+v", evt)
	}
}

func TestParseEvent_ToolExecutionStart_NoArgs(t *testing.T) {
	evt := ParseEvent(`{"type":"tool_execution_start","toolName":"think"}`)
	if evt == nil {
		t.Fatal("expected non-nil event")
	}
	if evt.Tool != "think" {
		t.Fatalf("expected tool 'think', got %q", evt.Tool)
	}
	if evt.Detail != "" {
		t.Fatalf("expected empty detail, got %q", evt.Detail)
	}
}

// --- Meta events ---

func TestParseEvent_AgentStart(t *testing.T) {
	evt := ParseEvent(`{"type":"agent_start"}`)
	if evt == nil {
		t.Fatal("expected non-nil event")
	}
	if evt.Kind != KindMeta {
		t.Fatalf("expected KindMeta, got %s", evt.Kind)
	}
		if evt.Text != "ai: agent started" {
		t.Fatalf("unexpected text: %s", evt.Text)
	}
}

// --- Response events ---

func TestParseEvent_Response_Success_NoData(t *testing.T) {
	evt := ParseEvent(`{"type":"response","command":"prompt","success":true}`)
	if evt != nil {
		t.Fatalf("expected nil for success response with no data, got %+v", evt)
	}
}

func TestParseEvent_Response_Error(t *testing.T) {
	evt := ParseEvent(`{"type":"response","command":"prompt","success":false,"error":"session id is required"}`)
	if evt == nil {
		t.Fatal("expected non-nil event")
	}
	if evt.Kind != KindResponse {
		t.Fatalf("expected KindResponse, got %s", evt.Kind)
	}
	if !strings.Contains(evt.Text, "session id is required") {
		t.Fatalf("expected error message in text, got: %s", evt.Text)
	}
}

func TestParseEvent_Response_Commands(t *testing.T) {
	input := `{"type":"response","command":"prompt","success":true,"data":{"commands":[{"name":"compact","description":"Compact context"},{"name":"get_state","description":"Get agent state"}]}}`
	evt := ParseEvent(input)
	if evt == nil {
		t.Fatal("expected non-nil event")
	}
	if evt.Kind != KindMeta {
		t.Fatalf("expected KindMeta, got %s", evt.Kind)
	}
	if !strings.Contains(evt.Text, "compact") || !strings.Contains(evt.Text, "get_state") {
		t.Fatalf("expected commands in text, got: %s", evt.Text)
	}
}

func TestParseEvent_AgentEnd_Success(t *testing.T) {
	evt := ParseEvent(`{"type":"agent_end","messages":[]}`)
	if evt == nil {
		t.Fatal("expected non-nil event")
	}
	if evt.Kind != KindMeta {
		t.Fatalf("expected KindMeta, got %s", evt.Kind)
	}
	if evt.Text != "ai: agent done" {
		t.Fatalf("unexpected text: %q", evt.Text)
	}
}

func TestParseEvent_AgentEnd_Failed(t *testing.T) {
	evt := ParseEvent(`{"type":"agent_end","success":false}`)
	if evt == nil {
		t.Fatal("expected non-nil event")
	}
	if evt.Text != "ai: agent failed" {
		t.Fatalf("unexpected text: %q", evt.Text)
	}
}

func TestParseEvent_AgentEnd_WithErrMsg(t *testing.T) {
	evt := ParseEvent(`{"type":"agent_end","error":"API rate limit exceeded"}`)
	if evt == nil {
		t.Fatal("expected non-nil event")
	}
	if !strings.Contains(evt.Text, "ai: agent failed: API rate limit exceeded") {
		t.Fatalf("unexpected text: %q", evt.Text)
	}
}

func TestParseEvent_TurnStart(t *testing.T) {
	evt := ParseEvent(`{"type":"turn_start"}`)
	// turn_start is silent in ai-win mode
	if evt != nil {
		t.Fatalf("expected nil for turn_start (silent), got %+v", evt)
	}
}

// --- Skip events ---

func TestParseEvent_TurnEnd_Silent(t *testing.T) {
	evt := ParseEvent(`{"type":"turn_end","message":{}}`)
	if evt != nil {
		t.Fatalf("expected nil for turn_end, got %+v", evt)
	}
}

func TestParseEvent_ToolExecutionEnd_Silent(t *testing.T) {
	evt := ParseEvent(`{"type":"tool_execution_end"}`)
	if evt == nil {
		t.Fatal("expected non-nil event for tool_execution_end")
	}
	if evt.Kind != KindTool {
		t.Fatalf("expected KindTool, got %s", evt.Kind)
	}
	if !strings.Contains(evt.Text, "done") {
		t.Fatalf("expected 'done' in text, got: %q", evt.Text)
	}
}

// --- Error events ---

func TestParseEvent_Error(t *testing.T) {
	evt := ParseEvent(`{"type":"error","error":"something broke"}`)
	if evt == nil {
		t.Fatal("expected non-nil event")
	}
	if evt.Kind != KindMeta {
		t.Fatalf("expected KindMeta, got %s", evt.Kind)
	}
	if !strings.Contains(evt.Text, "something broke") {
		t.Fatalf("expected text to contain error message, got %q", evt.Text)
	}
}

func TestParseEvent_Error_NoMessage(t *testing.T) {
	evt := ParseEvent(`{"type":"error"}`)
	if evt == nil {
		t.Fatal("expected non-nil event")
	}
	if !strings.Contains(evt.Text, "unknown error") {
		t.Fatalf("expected 'unknown error', got %q", evt.Text)
	}
}

// --- Session switch ---

func TestParseEvent_SessionSwitch_WithName(t *testing.T) {
	evt := ParseEvent(`{"type":"session_switch","session":"abc123","sessionName":"my session"}`)
	if evt == nil {
		t.Fatal("expected non-nil event")
	}
	if evt.Kind != KindSessionSwitch {
		t.Fatalf("expected KindSessionSwitch, got %s", evt.Kind)
	}
	if !strings.Contains(evt.Text, "my session") {
		t.Fatalf("expected text to contain session name, got %q", evt.Text)
	}
	if !strings.Contains(evt.Text, "abc123") {
		t.Fatalf("expected text to contain session ID, got %q", evt.Text)
	}
}

func TestParseEvent_SessionSwitch_WithoutName(t *testing.T) {
	evt := ParseEvent(`{"type":"session_switch","session":"abc123"}`)
	if evt == nil {
		t.Fatal("expected non-nil event")
	}
	if evt.Kind != KindSessionSwitch {
		t.Fatalf("expected KindSessionSwitch, got %s", evt.Kind)
	}
	if !strings.Contains(evt.Text, "abc123") {
		t.Fatalf("expected text to contain session ID, got %q", evt.Text)
	}
}

func TestParseEvent_SessionSwitch_Empty(t *testing.T) {
	evt := ParseEvent(`{"type":"session_switch"}`)
	if evt != nil {
		t.Fatalf("expected nil for session_switch with no fields, got %+v", evt)
	}
}

// --- ExtractTextDelta unit tests ---

func TestExtractTextDelta_DataFormat(t *testing.T) {
	delta := ExtractTextDelta(map[string]any{
		"data": map[string]any{"text_delta": "hello"},
	})
	if delta != "hello" {
		t.Fatalf("expected 'hello', got %q", delta)
	}
}

func TestExtractTextDelta_AssistantMessageEvent(t *testing.T) {
	delta := ExtractTextDelta(map[string]any{
		"assistantMessageEvent": map[string]any{"type": "text_delta", "delta": "world"},
	})
	if delta != "world" {
		t.Fatalf("expected 'world', got %q", delta)
	}
}

func TestExtractTextDelta_Empty(t *testing.T) {
	delta := ExtractTextDelta(map[string]any{})
	if delta != "" {
		t.Fatalf("expected empty string, got %q", delta)
	}
}

// --- ExtractToolName unit tests ---

func TestExtractToolName(t *testing.T) {
	tests := []struct {
		name string
		evt  map[string]any
		want string
	}{
		{"top-level", map[string]any{"toolName": "read"}, "read"},
		{"legacy data", map[string]any{"data": map[string]any{"tool": "bash"}}, "bash"},
		{"missing", map[string]any{}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractToolName(tt.evt)
			if got != tt.want {
				t.Fatalf("expected %q, got %q", tt.want, got)
			}
		})
	}
}

// --- Multiple args in tool detail ---

func TestParseEvent_ToolExecutionStart_MultipleArgs(t *testing.T) {
	evt := ParseEvent(`{"type":"tool_execution_start","toolName":"search","args":{"pattern":"TODO","path":"/src"}}`)
	if evt == nil {
		t.Fatal("expected non-nil event")
	}
	if !strings.Contains(evt.Detail, "path=/src") {
		t.Fatalf("expected detail to contain path=/src, got %q", evt.Detail)
	}
	if !strings.Contains(evt.Detail, "pattern=TODO") {
		t.Fatalf("expected detail to contain pattern=TODO, got %q", evt.Detail)
	}
}

// --- Leading/trailing whitespace on input line ---

func TestParseEvent_WhitespaceAroundJSON(t *testing.T) {
	evt := ParseEvent(`  {"type":"agent_start"}  `)
	if evt == nil {
		t.Fatal("expected non-nil event")
	}
	if evt.Kind != KindMeta {
		t.Fatalf("expected KindMeta, got %s", evt.Kind)
	}
}