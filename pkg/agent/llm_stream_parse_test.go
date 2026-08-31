package agent

import (
	"errors"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
)

func TestProcessStreamChunk_StartEvent(t *testing.T) {
	state := NewStreamChunkState()
	result := processStreamChunk(state, llm.LLMStartEvent{}, "")

	if result.EventType != ChunkStart {
		t.Fatalf("expected ChunkStart, got %v", result.EventType)
	}
	if !state.Started {
		t.Fatal("expected state.Started = true")
	}
	if state.FirstTokenSeen {
		t.Fatal("expected FirstTokenSeen = false after start")
	}
}

func TestProcessStreamChunk_TextDelta(t *testing.T) {
	state := NewStreamChunkState()
	// Must process start first
	processStreamChunk(state, llm.LLMStartEvent{}, "")

	result := processStreamChunk(state, llm.LLMTextDeltaEvent{
		Delta: "Hello",
		Index: 0,
	}, "")

	if result.EventType != ChunkTextDelta {
		t.Fatalf("expected ChunkTextDelta, got %v", result.EventType)
	}
	if result.Delta != "Hello" {
		t.Fatalf("expected delta 'Hello', got %q", result.Delta)
	}
	if result.ContentIndex != 0 {
		t.Fatalf("expected content index 0, got %d", result.ContentIndex)
	}
	if !state.FirstTokenSeen {
		t.Fatal("expected FirstTokenSeen = true")
	}

	// Check accumulated text
	if state.TextBuilder.String() != "Hello" {
		t.Fatalf("expected text builder 'Hello', got %q", state.TextBuilder.String())
	}

	// Check content blocks
	assertTextContent(t, result.Content, "Hello")
}

func TestProcessStreamChunk_TextDeltaAccumulates(t *testing.T) {
	state := NewStreamChunkState()
	processStreamChunk(state, llm.LLMStartEvent{}, "")

	processStreamChunk(state, llm.LLMTextDeltaEvent{Delta: "Hello ", Index: 0}, "")
	result := processStreamChunk(state, llm.LLMTextDeltaEvent{Delta: "world", Index: 0}, "")

	if state.TextBuilder.String() != "Hello world" {
		t.Fatalf("expected 'Hello world', got %q", state.TextBuilder.String())
	}
	assertTextContent(t, result.Content, "Hello world")
}

func TestProcessStreamChunk_ThinkingDelta(t *testing.T) {
	state := NewStreamChunkState()
	processStreamChunk(state, llm.LLMStartEvent{}, "")

	result := processStreamChunk(state, llm.LLMThinkingDeltaEvent{
		Delta: "Let me think...",
		Index: 1,
	}, "")

	if result.EventType != ChunkThinkingDelta {
		t.Fatalf("expected ChunkThinkingDelta, got %v", result.EventType)
	}
	if result.Delta != "Let me think..." {
		t.Fatalf("expected delta 'Let me think...', got %q", result.Delta)
	}
	if !state.FirstTokenSeen {
		t.Fatal("expected FirstTokenSeen = true")
	}

	assertThinkingContent(t, result.Content, "Let me think...")
}

func TestProcessStreamChunk_ThinkingDeltaSuppressedWhenOff(t *testing.T) {
	state := NewStreamChunkState()
	processStreamChunk(state, llm.LLMStartEvent{}, "")

	result := processStreamChunk(state, llm.LLMThinkingDeltaEvent{
		Delta: "should be ignored",
		Index: 0,
	}, "off")

	if result.EventType != ChunkIgnored {
		t.Fatalf("expected ChunkIgnored when thinking is off, got %v", result.EventType)
	}
	if state.ThinkingBuilder.String() != "" {
		t.Fatalf("expected empty thinking builder, got %q", state.ThinkingBuilder.String())
	}
}

// --- processThinkingDelta standalone tests ---

func TestProcessThinkingDelta_Basic(t *testing.T) {
	state := NewStreamChunkState()
	state.Started = true

	result := processThinkingDelta(state, "I need to", 0, "")

	if result.EventType != ChunkThinkingDelta {
		t.Fatalf("expected ChunkThinkingDelta, got %v", result.EventType)
	}
	if result.Delta != "I need to" {
		t.Fatalf("expected delta 'I need to', got %q", result.Delta)
	}
	if result.ContentIndex != 0 {
		t.Fatalf("expected content index 0, got %d", result.ContentIndex)
	}
	if !state.FirstTokenSeen {
		t.Fatal("expected FirstTokenSeen = true")
	}
	if state.ThinkingBuilder.String() != "I need to" {
		t.Fatalf("expected thinking builder 'I need to', got %q", state.ThinkingBuilder.String())
	}
	assertThinkingContent(t, result.Content, "I need to")
}

func TestProcessThinkingDelta_Accumulates(t *testing.T) {
	state := NewStreamChunkState()
	state.Started = true

	processThinkingDelta(state, "Step 1: ", 0, "")
	result := processThinkingDelta(state, "analyze code", 0, "")

	if state.ThinkingBuilder.String() != "Step 1: analyze code" {
		t.Fatalf("expected 'Step 1: analyze code', got %q", state.ThinkingBuilder.String())
	}
	assertThinkingContent(t, result.Content, "Step 1: analyze code")
}

func TestProcessThinkingDelta_NotStarted(t *testing.T) {
	state := NewStreamChunkState()
	// state.Started is false

	result := processThinkingDelta(state, "thinking", 0, "")

	if result.EventType != ChunkIgnored {
		t.Fatalf("expected ChunkIgnored before start, got %v", result.EventType)
	}
	if state.ThinkingBuilder.String() != "" {
		t.Fatalf("expected empty thinking builder, got %q", state.ThinkingBuilder.String())
	}
}

func TestProcessThinkingDelta_SuppressedWhenOff(t *testing.T) {
	state := NewStreamChunkState()
	state.Started = true

	result := processThinkingDelta(state, "should be ignored", 0, "off")

	if result.EventType != ChunkIgnored {
		t.Fatalf("expected ChunkIgnored when thinking is off, got %v", result.EventType)
	}
	if state.ThinkingBuilder.String() != "" {
		t.Fatalf("expected empty thinking builder, got %q", state.ThinkingBuilder.String())
	}
	if state.FirstTokenSeen {
		t.Fatal("expected FirstTokenSeen = false when suppressed")
	}
}

func TestProcessThinkingDelta_ContentBlocksWithExistingText(t *testing.T) {
	state := NewStreamChunkState()
	state.Started = true
	state.TextBuilder.WriteString("existing text")

	result := processThinkingDelta(state, "reasoning", 1, "")

	assertThinkingContent(t, result.Content, "reasoning")
	assertTextContent(t, result.Content, "existing text")
}

func TestProcessThinkingDelta_SetsFirstTokenSeen(t *testing.T) {
	state := NewStreamChunkState()
	state.Started = true

	if state.FirstTokenSeen {
		t.Fatal("expected FirstTokenSeen = false initially")
	}

	processThinkingDelta(state, "first thought", 0, "")

	if !state.FirstTokenSeen {
		t.Fatal("expected FirstTokenSeen = true after processing thinking delta")
	}
}

func TestProcessThinkingDelta_WithToolCalls(t *testing.T) {
	state := NewStreamChunkState()
	state.Started = true
	state.ToolCalls[0] = &toolCallState{
		id: "call_1", name: "bash", callType: "function", arguments: `{"command":"ls"}`,
	}

	result := processThinkingDelta(state, "let me check files", 0, "")

	assertThinkingContent(t, result.Content, "let me check files")
	assertToolCallContent(t, result.Content, 0, "call_1", "bash", map[string]any{
		"command": "ls",
	})
}

func TestProcessThinkingDelta_ThinkingLevels(t *testing.T) {
	// All non-"off" levels should allow thinking
	for _, level := range []string{"", "minimal", "low", "medium", "high", "xhigh"} {
		t.Run("level_"+level, func(t *testing.T) {
			state := NewStreamChunkState()
			state.Started = true

			result := processThinkingDelta(state, "thinking", 0, level)
			if result.EventType != ChunkThinkingDelta {
				t.Fatalf("level %q: expected ChunkThinkingDelta, got %v", level, result.EventType)
			}
		})
	}
}

func TestProcessThinkingDelta_ContentIndexPreserved(t *testing.T) {
	state := NewStreamChunkState()
	state.Started = true

	result := processThinkingDelta(state, "thought", 3, "")
	if result.ContentIndex != 3 {
		t.Fatalf("expected content index 3, got %d", result.ContentIndex)
	}
}

func TestProcessStreamChunk_ToolCallDelta(t *testing.T) {
	state := NewStreamChunkState()
	processStreamChunk(state, llm.LLMStartEvent{}, "")

	// First tool call delta: name + id
	result := processStreamChunk(state, llm.LLMToolCallDeltaEvent{
		Index: 0,
		ToolCall: &llm.ToolCall{
			ID:   "call_123",
			Type: "function",
			Function: llm.FunctionCall{
				Name: "bash",
			},
		},
	}, "")

	if result.EventType != ChunkToolCallDelta {
		t.Fatalf("expected ChunkToolCallDelta, got %v", result.EventType)
	}
	if result.ContentIndex != 0 {
		t.Fatalf("expected content index 0, got %d", result.ContentIndex)
	}

	// Second delta: arguments
	result = processStreamChunk(state, llm.LLMToolCallDeltaEvent{
		Index: 0,
		ToolCall: &llm.ToolCall{
			Function: llm.FunctionCall{
				Arguments: `{"command":"ls"}`,
			},
		},
	}, "")

	assertToolCallContent(t, result.Content, 0, "call_123", "bash", map[string]any{
		"command": "ls",
	})
}

func TestProcessStreamChunk_ToolCallDeltaAccumulatesArgs(t *testing.T) {
	state := NewStreamChunkState()
	processStreamChunk(state, llm.LLMStartEvent{}, "")

	// Simulate Anthropic-style incremental arguments
	processStreamChunk(state, llm.LLMToolCallDeltaEvent{
		Index: 0,
		ToolCall: &llm.ToolCall{
			ID:   "call_1",
			Type: "function",
			Function: llm.FunctionCall{
				Name:      "read",
				Arguments: `{"pa`,
			},
		},
	}, "")

	result := processStreamChunk(state, llm.LLMToolCallDeltaEvent{
		Index: 0,
		ToolCall: &llm.ToolCall{
			Function: llm.FunctionCall{
				Arguments: `th":"/tmp"}`,
			},
		},
	}, "")

	assertToolCallContent(t, result.Content, 0, "call_1", "read", map[string]any{
		"path": "/tmp",
	})
}

func TestProcessStreamChunk_MultipleToolCalls(t *testing.T) {
	state := NewStreamChunkState()
	processStreamChunk(state, llm.LLMStartEvent{}, "")

	processStreamChunk(state, llm.LLMToolCallDeltaEvent{
		Index: 0,
		ToolCall: &llm.ToolCall{
			ID:   "call_1",
			Type: "function",
			Function: llm.FunctionCall{
				Name:      "bash",
				Arguments: `{"command":"ls"}`,
			},
		},
	}, "")

	result := processStreamChunk(state, llm.LLMToolCallDeltaEvent{
		Index: 1,
		ToolCall: &llm.ToolCall{
			ID:   "call_2",
			Type: "function",
			Function: llm.FunctionCall{
				Name:      "read",
				Arguments: `{"path":"test.go"}`,
			},
		},
	}, "")

	if result.EventType != ChunkToolCallDelta {
		t.Fatalf("expected ChunkToolCallDelta, got %v", result.EventType)
	}

	// Should have 2 tool calls in content
	toolCalls := 0
	for _, block := range result.Content {
		if _, ok := block.(agentctx.ToolCallContent); ok {
			toolCalls++
		}
	}
	if toolCalls != 2 {
		t.Fatalf("expected 2 tool calls, got %d", toolCalls)
	}
}

func TestProcessStreamChunk_DoneEvent(t *testing.T) {
	state := NewStreamChunkState()
	processStreamChunk(state, llm.LLMStartEvent{}, "")
	processStreamChunk(state, llm.LLMTextDeltaEvent{Delta: "done text", Index: 0}, "")

	result := processStreamChunk(state, llm.LLMDoneEvent{
		StopReason: "stop",
		Usage:      llm.Usage{InputTokens: 100, OutputTokens: 50, TotalTokens: 150},
	}, "")

	if result.EventType != ChunkDone {
		t.Fatalf("expected ChunkDone, got %v", result.EventType)
	}
	if result.StopReason != "stop" {
		t.Fatalf("expected stop reason 'stop', got %q", result.StopReason)
	}
	if result.Usage.InputTokens != 100 {
		t.Fatalf("expected 100 input tokens, got %d", result.Usage.InputTokens)
	}
	if result.Usage.OutputTokens != 50 {
		t.Fatalf("expected 50 output tokens, got %d", result.Usage.OutputTokens)
	}
	assertTextContent(t, result.Content, "done text")
}

func TestProcessStreamChunk_DoneEventWithToolCalls(t *testing.T) {
	state := NewStreamChunkState()
	processStreamChunk(state, llm.LLMStartEvent{}, "")

	processStreamChunk(state, llm.LLMToolCallDeltaEvent{
		Index: 0,
		ToolCall: &llm.ToolCall{
			ID:   "tc_1",
			Type: "function",
			Function: llm.FunctionCall{
				Name:      "bash",
				Arguments: `{"command":"echo hi"}`,
			},
		},
	}, "")

	result := processStreamChunk(state, llm.LLMDoneEvent{
		StopReason: "tool_calls",
		Usage:      llm.Usage{InputTokens: 200, OutputTokens: 30, TotalTokens: 230},
	}, "")

	if result.EventType != ChunkDone {
		t.Fatalf("expected ChunkDone, got %v", result.EventType)
	}
	if result.StopReason != "tool_calls" {
		t.Fatalf("expected 'tool_calls', got %q", result.StopReason)
	}
	assertToolCallContent(t, result.Content, 0, "tc_1", "bash", map[string]any{
		"command": "echo hi",
	})
}

func TestProcessStreamChunk_ErrorEvent(t *testing.T) {
	state := NewStreamChunkState()
	processStreamChunk(state, llm.LLMStartEvent{}, "")

	testErr := errors.New("connection timeout")
	result := processStreamChunk(state, llm.LLMErrorEvent{Error: testErr}, "")

	if result.EventType != ChunkError {
		t.Fatalf("expected ChunkError, got %v", result.EventType)
	}
	if result.Error == nil {
		t.Fatal("expected non-nil error")
	}
	if result.Error.Error() != "connection timeout" {
		t.Fatalf("expected 'connection timeout', got %q", result.Error.Error())
	}
}

func TestProcessStreamChunk_DeltaBeforeStartIgnored(t *testing.T) {
	state := NewStreamChunkState()

	result := processStreamChunk(state, llm.LLMTextDeltaEvent{Delta: "hello", Index: 0}, "")
	if result.EventType != ChunkIgnored {
		t.Fatalf("expected ChunkIgnored for delta before start, got %v", result.EventType)
	}

	result = processStreamChunk(state, llm.LLMThinkingDeltaEvent{Delta: "think", Index: 0}, "")
	if result.EventType != ChunkIgnored {
		t.Fatalf("expected ChunkIgnored for thinking delta before start, got %v", result.EventType)
	}

	result = processStreamChunk(state, llm.LLMToolCallDeltaEvent{
		Index:    0,
		ToolCall: &llm.ToolCall{ID: "x"},
	}, "")
	if result.EventType != ChunkIgnored {
		t.Fatalf("expected ChunkIgnored for tool call delta before start, got %v", result.EventType)
	}
}

func TestProcessStreamChunk_StartResetsState(t *testing.T) {
	state := NewStreamChunkState()

	// First stream
	processStreamChunk(state, llm.LLMStartEvent{}, "")
	processStreamChunk(state, llm.LLMTextDeltaEvent{Delta: "first", Index: 0}, "")
	processStreamChunk(state, llm.LLMThinkingDeltaEvent{Delta: "thinking", Index: 0}, "")
	processStreamChunk(state, llm.LLMToolCallDeltaEvent{
		Index: 0,
		ToolCall: &llm.ToolCall{
			ID: "tc1", Type: "function",
			Function: llm.FunctionCall{Name: "bash", Arguments: `{"cmd":"x"}`},
		},
	}, "")

	// Second stream start should reset everything
	processStreamChunk(state, llm.LLMStartEvent{}, "")

	if state.TextBuilder.String() != "" {
		t.Fatalf("expected empty text after reset, got %q", state.TextBuilder.String())
	}
	if state.ThinkingBuilder.String() != "" {
		t.Fatalf("expected empty thinking after reset, got %q", state.ThinkingBuilder.String())
	}
	if len(state.ToolCalls) != 0 {
		t.Fatalf("expected empty tool calls after reset, got %d", len(state.ToolCalls))
	}
	if state.FirstTokenSeen {
		t.Fatal("expected FirstTokenSeen = false after reset")
	}

	// Can accumulate fresh content
	result := processStreamChunk(state, llm.LLMTextDeltaEvent{Delta: "fresh", Index: 0}, "")
	assertTextContent(t, result.Content, "fresh")
}

func TestProcessStreamChunk_FullStream(t *testing.T) {
	state := NewStreamChunkState()

	// Start
	processStreamChunk(state, llm.LLMStartEvent{}, "")

	// Thinking
	thinkResult := processStreamChunk(state, llm.LLMThinkingDeltaEvent{Delta: "I should", Index: 0}, "")
	assertThinkingContent(t, thinkResult.Content, "I should")

	thinkResult = processStreamChunk(state, llm.LLMThinkingDeltaEvent{Delta: " use a tool", Index: 0}, "")
	assertThinkingContent(t, thinkResult.Content, "I should use a tool")

	// Text
	textResult := processStreamChunk(state, llm.LLMTextDeltaEvent{Delta: "Let me", Index: 0}, "")
	assertTextContent(t, textResult.Content, "Let me")
	// Thinking still present in content
	assertThinkingContent(t, textResult.Content, "I should use a tool")

	textResult = processStreamChunk(state, llm.LLMTextDeltaEvent{Delta: " check.", Index: 0}, "")
	assertTextContent(t, textResult.Content, "Let me check.")

	// Done
	doneResult := processStreamChunk(state, llm.LLMDoneEvent{
		StopReason: "end_turn",
		Usage:      llm.Usage{InputTokens: 500, OutputTokens: 100, TotalTokens: 600},
	}, "")

	if doneResult.EventType != ChunkDone {
		t.Fatalf("expected ChunkDone, got %v", doneResult.EventType)
	}
	assertTextContent(t, doneResult.Content, "Let me check.")
	assertThinkingContent(t, doneResult.Content, "I should use a tool")
}

func TestProcessStreamChunk_UnknownEventIgnored(t *testing.T) {
	state := NewStreamChunkState()

	// A nil interface (no event value) should be handled gracefully.
	// Since processStreamChunk accepts llm.LLMEvent interface, we can't
	// easily pass a truly unknown type. Instead, verify that the default
	// case is reached by passing nil (which won't match any case).
	// However, the interface requires non-nil. So we test with all known
	// events that the ChunkIgnored path works for unhandled cases.
	// The thinking-off path already returns ChunkIgnored.
	result := processStreamChunk(state, llm.LLMThinkingDeltaEvent{Delta: "x", Index: 0}, "off")
	if result.EventType != ChunkIgnored {
		t.Fatalf("expected ChunkIgnored, got %v", result.EventType)
	}
}

func TestBuildContentBlocks_Empty(t *testing.T) {
	content := buildContentBlocks("", "", nil)
	if len(content) != 0 {
		t.Fatalf("expected empty content, got %d blocks", len(content))
	}
}

func TestBuildContentBlocks_TextOnly(t *testing.T) {
	content := buildContentBlocks("hello", "", nil)
	if len(content) != 1 {
		t.Fatalf("expected 1 block, got %d", len(content))
	}
	assertTextContent(t, content, "hello")
}

func TestBuildContentBlocks_ThinkingAndText(t *testing.T) {
	calls := map[int]*toolCallState{}
	content := buildContentBlocks("text", "thoughts", calls)

	if len(content) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(content))
	}
	// Thinking comes first
	if tc, ok := content[0].(agentctx.ThinkingContent); !ok {
		t.Fatalf("expected ThinkingContent first, got %T", content[0])
	} else if tc.Thinking != "thoughts" {
		t.Fatalf("expected 'thoughts', got %q", tc.Thinking)
	}
	if tc, ok := content[1].(agentctx.TextContent); !ok {
		t.Fatalf("expected TextContent second, got %T", content[1])
	} else if tc.Text != "text" {
		t.Fatalf("expected 'text', got %q", tc.Text)
	}
}

func TestBuildContentBlocks_InvalidJSONArgs(t *testing.T) {
	calls := map[int]*toolCallState{
		0: {id: "tc_1", name: "bash", callType: "function", arguments: "{invalid json"},
	}
	content := buildContentBlocks("", "", calls)

	if len(content) != 1 {
		t.Fatalf("expected 1 block, got %d", len(content))
	}
	tc, ok := content[0].(agentctx.ToolCallContent)
	if !ok {
		t.Fatalf("expected ToolCallContent, got %T", content[0])
	}
	// Invalid JSON should result in empty map, not crash
	if len(tc.Arguments) != 0 {
		t.Fatalf("expected empty args map for invalid JSON, got %v", tc.Arguments)
	}
}

// --- helpers ---

func assertTextContent(t *testing.T, blocks []agentctx.ContentBlock, expected string) {
	t.Helper()
	for _, b := range blocks {
		if tc, ok := b.(agentctx.TextContent); ok {
			if tc.Text != expected {
				t.Fatalf("expected text %q, got %q", expected, tc.Text)
			}
			return
		}
	}
	t.Fatalf("no TextContent block found with text %q in %d blocks", expected, len(blocks))
}

func assertThinkingContent(t *testing.T, blocks []agentctx.ContentBlock, expected string) {
	t.Helper()
	for _, b := range blocks {
		if tc, ok := b.(agentctx.ThinkingContent); ok {
			if tc.Thinking != expected {
				t.Fatalf("expected thinking %q, got %q", expected, tc.Thinking)
			}
			return
		}
	}
	t.Fatalf("no ThinkingContent block found with thinking %q in %d blocks", expected, len(blocks))
}

func assertToolCallContent(t *testing.T, blocks []agentctx.ContentBlock, index int, id string, name string, args map[string]any) {
	t.Helper()
	i := 0
	for _, b := range blocks {
		if tc, ok := b.(agentctx.ToolCallContent); ok {
			if i == index {
				if tc.ID != id {
					t.Fatalf("expected tool call id %q, got %q", id, tc.ID)
				}
				if tc.Name != name {
					t.Fatalf("expected tool call name %q, got %q", name, tc.Name)
				}
				// Compare args
				for k, v := range args {
					if tc.Arguments[k] != v {
						t.Fatalf("expected arg %q=%v, got %v", k, v, tc.Arguments[k])
					}
				}
				return
			}
			i++
		}
	}
	t.Fatalf("no ToolCallContent at index %d found in %d blocks", index, len(blocks))
}
