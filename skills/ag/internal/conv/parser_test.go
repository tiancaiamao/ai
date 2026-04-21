package conv

import (
	"testing"
)

func TestParseEvent_Empty(t *testing.T) {
	got := ParseEvent("")
	if got != nil {
		t.Fatalf("expected nil for empty string, got %v", got)
	}
}

func TestParseEvent_InvalidJSON(t *testing.T) {
	got := ParseEvent("not json")
	if got != nil {
		t.Fatalf("expected nil for invalid JSON, got %v", got)
	}
}

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

func TestParseEvent_MessageUpdate_EmptyDelta(t *testing.T) {
	evt := ParseEvent(`{"type":"message_update","data":{"text_delta":""}}`)
	if evt != nil {
		t.Fatalf("expected nil for empty text delta, got %v", evt)
	}
}

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
	if evt.Text == "" {
		t.Fatal("expected formatted text output")
	}
}

func TestParseEvent_ToolExecutionStart_Legacy(t *testing.T) {
	evt := ParseEvent(`{"type":"tool_execution_start","data":{"tool":"bash","args":{"command":"ls"}}}`)
	if evt == nil {
		t.Fatal("expected non-nil event")
	}
	if evt.Tool != "bash" {
		t.Fatalf("expected tool 'bash', got %q", evt.Tool)
	}
}

func TestParseEvent_AgentStart(t *testing.T) {
	evt := ParseEvent(`{"type":"agent_start"}`)
	if evt == nil {
		t.Fatal("expected non-nil event")
	}
	if evt.Kind != KindMeta {
		t.Fatalf("expected KindMeta, got %s", evt.Kind)
	}
}

func TestParseEvent_AgentEnd(t *testing.T) {
	evt := ParseEvent(`{"type":"agent_end","messages":[]}`)
	if evt == nil {
		t.Fatal("expected non-nil event")
	}
	if evt.Kind != KindMeta {
		t.Fatalf("expected KindMeta, got %s", evt.Kind)
	}
}

func TestParseEvent_AgentEnd_Failed(t *testing.T) {
	evt := ParseEvent(`{"type":"agent_end","success":false}`)
	if evt == nil {
		t.Fatal("expected non-nil event")
	}
	if evt.Kind != KindMeta {
		t.Fatalf("expected KindMeta, got %s", evt.Kind)
	}
}

func TestParseEvent_AgentEnd_WithErrMsg(t *testing.T) {
	evt := ParseEvent(`{"type":"agent_end","error":"API rate limit exceeded"}`)
	if evt == nil {
		t.Fatal("expected non-nil event")
	}
	if evt.Kind != KindMeta {
		t.Fatalf("expected KindMeta, got %s", evt.Kind)
	}
}

func TestParseEvent_Error(t *testing.T) {
	evt := ParseEvent(`{"type":"error","error":"something broke"}`)
	if evt == nil {
		t.Fatal("expected non-nil event")
	}
	if evt.Kind != KindMeta {
		t.Fatalf("expected KindMeta, got %s", evt.Kind)
	}
}

func TestParseEvent_TurnStart(t *testing.T) {
	evt := ParseEvent(`{"type":"turn_start"}`)
	if evt == nil {
		t.Fatal("expected non-nil event")
	}
	if evt.Kind != KindMeta {
		t.Fatalf("expected KindMeta, got %s", evt.Kind)
	}
}

func TestParseEvent_TurnEnd_Silent(t *testing.T) {
	evt := ParseEvent(`{"type":"turn_end","message":{}}`)
	if evt != nil {
		t.Fatalf("expected nil for turn_end, got %v", evt)
	}
}

func TestParseEvent_UnknownType(t *testing.T) {
	evt := ParseEvent(`{"type":"something_else"}`)
	if evt != nil {
		t.Fatalf("expected nil for unknown type, got %v", evt)
	}
}

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