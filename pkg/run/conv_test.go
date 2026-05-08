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

func TestParseEvent_Response_ModelList(t *testing.T) {
	input := `{"type":"response","command":"prompt","success":true,"data":{"models":[{"provider":"zai","id":"glm-4.5","name":"GLM 4.5"},{"provider":"openai","id":"gpt-4.1","name":"GPT-4.1"}],"currentIndex":1}}`
	evt := ParseEvent(input)
	if evt == nil {
		t.Fatal("expected non-nil event")
	}
	if evt.Kind != KindMeta {
		t.Fatalf("expected KindMeta, got %s", evt.Kind)
	}
	if !strings.Contains(evt.Text, "Available Models") {
		t.Fatalf("expected model list header, got: %s", evt.Text)
	}
	if !strings.Contains(evt.Text, "1: openai/gpt-4.1 - GPT-4.1 [current]") {
		t.Fatalf("expected current marker on selected model, got: %s", evt.Text)
	}
	if !strings.Contains(evt.Text, "Usage: /model <index>") {
		t.Fatalf("expected usage hint, got: %s", evt.Text)
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
	// turn_start is silent
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

// --- Sessions response ---

func TestParseEvent_Response_Sessions(t *testing.T) {
	raw := `{"type":"response","success":true,"data":{"sessions":[{"id":"abc123","name":"my session","updatedAt":"2025-01-15T10:30:00Z","messageCount":5}]}}`
	evt := ParseEvent(raw)
	if evt == nil {
		t.Fatal("expected non-nil event")
	}
	if evt.Kind != KindResponse {
		t.Fatalf("expected KindResponse, got %s", evt.Kind)
	}
	if !strings.Contains(evt.Text, "Available Sessions") {
		t.Fatalf("expected formatted sessions output, got %q", evt.Text)
	}
	if !strings.Contains(evt.Text, "my session") {
		t.Fatalf("expected session name in output, got %q", evt.Text)
	}
	if !strings.Contains(evt.Text, "/resume") {
		t.Fatalf("expected /resume usage hint, got %q", evt.Text)
	}
}

func TestParseEvent_Response_Sessions_DisplayOrder(t *testing.T) {
	// Sessions arrive sorted by UpdatedAt ascending (oldest first) from ListSessions.
	// renderSessions must preserve this order so that display index matches /resume index.
	raw := `{"type":"response","success":true,"data":{"sessions":[` +
		`{"id":"oldest","name":"oldest session","updatedAt":"2025-01-10T10:00:00Z","messageCount":1},` +
		`{"id":"middle","name":"middle session","updatedAt":"2025-01-15T10:00:00Z","messageCount":3},` +
		`{"id":"newest","name":"newest session","updatedAt":"2025-01-20T10:00:00Z","messageCount":5}` +
		`]}}`
	evt := ParseEvent(raw)
	if evt == nil {
		t.Fatal("expected non-nil event")
	}

	// Verify sessions appear in data order: oldest at top (index 0), newest at bottom
	lines := strings.Split(evt.Text, "\n")
	var indices []string
	for _, line := range lines {
		if strings.HasPrefix(line, "0:") || strings.HasPrefix(line, "1:") || strings.HasPrefix(line, "2:") {
			indices = append(indices, line)
		}
	}

	if len(indices) != 3 {
		t.Fatalf("expected 3 indexed session lines, got %d: %v", len(indices), indices)
	}

	// Index 0 should be "oldest session" — matches data source order
	if !strings.Contains(indices[0], "oldest session") {
		t.Fatalf("expected index 0 to be 'oldest session', got %q", indices[0])
	}
	// Index 1 should be "middle session"
	if !strings.Contains(indices[1], "middle session") {
		t.Fatalf("expected index 1 to be 'middle session', got %q", indices[1])
	}
	// Index 2 should be "newest session"
	if !strings.Contains(indices[2], "newest session") {
		t.Fatalf("expected index 2 to be 'newest session', got %q", indices[2])
	}
}

func TestParseEvent_Response_SessionsEmpty(t *testing.T) {
	raw := `{"type":"response","success":true,"data":{"sessions":[]}}`
	evt := ParseEvent(raw)
	if evt == nil {
		t.Fatal("expected non-nil event")
	}
	if evt.Text != "No sessions found" {
		t.Fatalf("expected 'No sessions found', got %q", evt.Text)
		}
}

// --- message_start / message_end (user messages) ---

func TestParseEvent_MessageStart_Silent(t *testing.T) {
	raw := `{"type":"message_start","message":{"role":"user","content":[{"type":"text","text":"hello"}]}}`
	evt := ParseEvent(raw)
	if evt != nil {
		t.Fatalf("expected nil for message_start, got %+v", evt)
	}
}

func TestParseEvent_MessageEnd_UserMessage(t *testing.T) {
	raw := `{"type":"message_end","message":{"role":"user","content":[{"type":"text","text":"你好"}],"metadata":{"kind":"user"}}}`
	evt := ParseEvent(raw)
	if evt == nil {
		t.Fatal("expected non-nil event")
	}
	if evt.Kind != KindText {
		t.Fatalf("expected KindText, got %s", evt.Kind)
	}
	if evt.Role != "user" {
		t.Fatalf("expected role=user, got %s", evt.Role)
	}
	if evt.Text != "你好" {
		t.Fatalf("expected '你好', got %q", evt.Text)
	}
}

func TestParseEvent_MessageEnd_AssistantSilent(t *testing.T) {
	raw := `{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"hi"}]}}`
	evt := ParseEvent(raw)
	if evt != nil {
		t.Fatalf("expected nil for assistant message_end, got %+v", evt)
	}
}

func TestParseEvent_MessageEnd_UserEmptyContent(t *testing.T) {
	raw := `{"type":"message_end","message":{"role":"user","content":[]}}`
	evt := ParseEvent(raw)
	if evt != nil {
		t.Fatalf("expected nil for empty user message, got %+v", evt)
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
// --- Tests for thinking content filtering ---

func TestParseEvent_ThinkingDelta_EmptyString(t *testing.T) {
	evt := ParseEvent(`{"type":"thinking_delta","delta":""}`)
	if evt != nil {
		t.Fatalf("expected nil for empty thinking delta, got %+v", evt)
	}
}

func TestParseEvent_ThinkingDelta_WhitespaceOnly(t *testing.T) {
	testCases := []struct {
		name  string
		delta string
	}{
		{"spaces only", "   "},
		{"tabs only", "\t\t"},
		{"newlines only", "\n\n"},
		{"mixed whitespace", "  \t\n  \t\n "},
		{"empty string with newline", "\n"},
		{"space tab mix", " \t \t "},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			json := `{"type":"thinking_delta","delta":"` + tc.delta + `"}`
			evt := ParseEvent(json)
			if evt != nil {
				t.Fatalf("expected nil for whitespace-only thinking delta %q, got %+v", tc.delta, evt)
			}
		})
	}
}

func TestParseEvent_ThinkingDelta_ValidContent(t *testing.T) {
	testCases := []struct {
		name      string
		json      string
		expectStr string
	}{
		{"simple text", `{"type":"thinking_delta","delta":"I need to think"}`, "I need to think"},
		{"text with leading space", `{"type":"thinking_delta","delta":"  Hello"}`, "  Hello"},
		{"text with trailing space", `{"type":"thinking_delta","delta":"Hello  "}`, "Hello  "},
		{"text with internal whitespace", `{"type":"thinking_delta","delta":"Hello world"}`, "Hello world"},
		{"text with newlines", `{"type":"thinking_delta","delta":"First\nSecond"}`, "First\nSecond"},
		{"text with mixed whitespace", `{"type":"thinking_delta","delta":"  Hello  world\n  "}`, "  Hello  world\n  "},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			evt := ParseEvent(tc.json)
			if evt == nil {
				t.Fatalf("expected non-nil event for json %s", tc.json)
			}
			if evt.Kind != KindThinking {
				t.Fatalf("expected KindThinking, got %s", evt.Kind)
			}
			if evt.Text != tc.expectStr {
				t.Fatalf("expected text %q, got %q", tc.expectStr, evt.Text)
			}
			if evt.Raw != tc.expectStr {
				t.Fatalf("expected raw %q, got %q", tc.expectStr, evt.Raw)
			}
		})
	}
}

func TestParseEvent_Response_NewSession_SkipsSessionState(t *testing.T) {
	// /new returns {sessionId, cancelled} — should be skipped (session_switch event handles display)
	input := `{"type":"response","command":"new","success":true,"data":{"sessionId":"abc-123","cancelled":false}}`
	evt := ParseEvent(input)
	if evt != nil {
		t.Fatalf("expected nil for /new response (should be skipped), got %+v", evt)
	}
}

func TestParseEvent_Response_NewSession_Cancelled(t *testing.T) {
	// /new with cancelled=true should also be skipped
	input := `{"type":"response","command":"new","success":true,"data":{"sessionId":"abc-123","cancelled":true}}`
	evt := ParseEvent(input)
	if evt != nil {
		t.Fatalf("expected nil for cancelled /new response, got %+v", evt)
	}
}

func TestParseEvent_Response_ForkNotSuppressed(t *testing.T) {
	// /fork returns {cancelled, text} but no sessionId — must not be suppressed
	input := `{"type":"response","command":"fork","success":true,"data":{"cancelled":false,"text":"forked content here"}}`
	evt := ParseEvent(input)
	if evt == nil {
		t.Fatal("expected non-nil event for /fork response, should not be suppressed")
	}
	if !strings.Contains(evt.Text, "forked content here") {
		t.Fatalf("expected fork text content, got: %s", evt.Text)
	}
}

func TestParseEvent_MessageUpdate_ThinkingDelta(t *testing.T) {
	// Test the thinking_delta in message_update format (assistantMessageEvent)
	json := `{"type":"message_update","assistantMessageEvent":{"type":"thinking_delta","delta":"   "}}`
	evt := ParseEvent(json)
	if evt != nil {
		t.Fatalf("expected nil for whitespace-only thinking_delta in message_update, got %+v", evt)
	}

	json = `{"type":"message_update","assistantMessageEvent":{"type":"thinking_delta","delta":"Valid thinking"}}`
	evt = ParseEvent(json)
	if evt == nil {
		t.Fatal("expected non-nil event for valid thinking_delta in message_update")
	}
	if evt.Kind != KindThinking {
		t.Fatalf("expected KindThinking, got %s", evt.Kind)
	}
	if evt.Text != "Valid thinking" {
		t.Fatalf("expected text 'Valid thinking', got %q", evt.Text)
	}
}
