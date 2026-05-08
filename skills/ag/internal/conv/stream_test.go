package conv

import (
	"strings"
	"testing"
)

func TestStreamEvents(t *testing.T) {
	input := `{"type":"agent_start"}
{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"hello"}}
{"type":"tool_execution_start","toolName":"read","args":{"path":"main.go"}}
{"type":"agent_end","messages":[]}
`
	var texts []string
	var tools []string
	var metas []string

	classifyHook := func(evt *FormattedEvent) bool {
		switch evt.Kind {
		case KindText:
			texts = append(texts, evt.Text)
		case KindTool:
			tools = append(tools, evt.Tool)
		case KindMeta:
			metas = append(metas, evt.Text)
		}
		return true
	}

		count, _ := StreamEvents(strings.NewReader(input), classifyHook)

	if count != 4 {
		t.Fatalf("expected 4 events, got %d", count)
	}
	if len(texts) != 1 || texts[0] != "hello" {
		t.Fatalf("expected ['hello'], got %v", texts)
	}
	if len(tools) != 1 || tools[0] != "read" {
		t.Fatalf("expected ['read'], got %v", tools)
	}
	if len(metas) != 2 {
		t.Fatalf("expected 2 meta events, got %d: %v", len(metas), metas)
	}
}

func TestStreamEvents_EarlyStop(t *testing.T) {
	input := `{"type":"agent_start"}
{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"first"}}
{"type":"agent_end","messages":[]}
{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"after end"}}
`
	var texts []string
	stopHook := func(evt *FormattedEvent) bool {
		if IsAgentDone(evt) {
			return false // stop streaming
		}
		return true
	}
	textHook := func(evt *FormattedEvent) bool {
		if evt.Kind == KindText {
			texts = append(texts, evt.Text)
		}
		return true
	}

	count, _ := StreamEvents(strings.NewReader(input), textHook, stopHook)

	if count != 3 {
		t.Fatalf("expected 3 events before stop, got %d", count)
	}
	// "after end" should not be collected
	if len(texts) != 1 || texts[0] != "first" {
		t.Fatalf("expected ['first'], got %v", texts)
	}
}

func TestStreamEventsFromString(t *testing.T) {
	input := `{"type":"agent_start"}
{"type":"agent_end","messages":[]}
`
	count, _ := StreamEventsFromString(input, func(evt *FormattedEvent) bool {
		return true
	})
	if count != 2 {
		t.Fatalf("expected 2, got %d", count)
	}
}

func TestIsAgentDone(t *testing.T) {
	tests := []struct {
		name string
		evt  *FormattedEvent
		want bool
	}{
		{"done", &FormattedEvent{Kind: KindMeta, Text: "--- agent done ---"}, true},
		{"failed", &FormattedEvent{Kind: KindMeta, Text: "--- agent failed ---"}, true},
		{"failed_with_msg", &FormattedEvent{Kind: KindMeta, Text: "--- agent failed: timeout ---"}, true},
		{"started", &FormattedEvent{Kind: KindMeta, Text: "--- agent started ---"}, false},
		{"text", &FormattedEvent{Kind: KindText, Text: "--- agent done ---"}, false},
		{"tool", &FormattedEvent{Kind: KindTool, Text: "🔧 read"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsAgentDone(tt.evt); got != tt.want {
				t.Fatalf("IsAgentDone() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsAgentSuccess(t *testing.T) {
	success := &FormattedEvent{Kind: KindMeta, Text: "--- agent done ---"}
	failed := &FormattedEvent{Kind: KindMeta, Text: "--- agent failed ---"}

	if !IsAgentSuccess(success) {
		t.Fatal("expected success for 'agent done'")
	}
	if IsAgentSuccess(failed) {
		t.Fatal("expected false for 'agent failed'")
	}
}

func TestCollectLastN(t *testing.T) {
	input := `{"type":"tool_execution_start","toolName":"read","args":{"path":"a.go"}}
{"type":"tool_execution_start","toolName":"write","args":{"path":"b.go"}}
{"type":"tool_execution_start","toolName":"bash","args":{"command":"ls"}}
{"type":"tool_execution_start","toolName":"read","args":{"path":"c.go"}}
{"type":"agent_end","messages":[]}
`

	hook, result := CollectLastN(2, KindTool)

		_, _ = StreamEventsFromString(input, hook)

	if len(*result) != 2 {
		t.Fatalf("expected 2 lines, got %d: %v", len(*result), *result)
	}
	// Should be the last 2 tool lines
	if !strings.Contains((*result)[0], "bash") {
		t.Fatalf("expected first line to contain 'bash', got %q", (*result)[0])
	}
	if !strings.Contains((*result)[1], "read") {
		t.Fatalf("expected second line to contain 'read', got %q", (*result)[1])
	}
}