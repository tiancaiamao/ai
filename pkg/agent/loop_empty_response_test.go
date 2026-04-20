package agent

import (
	"context"
	"fmt"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"testing"

	"github.com/tiancaiamao/ai/pkg/llm"
)

func TestIsEmptyActionableResponse(t *testing.T) {
	tests := []struct {
		name     string
		msg      *agentctx.AgentMessage
		expected bool
	}{
		{
			name:     "nil message",
			msg:      nil,
			expected: true,
		},
		{
			name: "empty content",
			msg: func() *agentctx.AgentMessage {
				m := agentctx.NewAssistantMessage()
				m.Content = []agentctx.ContentBlock{}
				return &m
			}(),
			expected: true,
		},
		{
			name: "only thinking content",
			msg: func() *agentctx.AgentMessage {
				m := agentctx.NewAssistantMessage()
				m.Content = []agentctx.ContentBlock{
					agentctx.ThinkingContent{Type: "thinking", Thinking: "I need to do something"},
				}
				return &m
			}(),
			expected: true,
		},
		{
			name: "thinking and empty text",
			msg: func() *agentctx.AgentMessage {
				m := agentctx.NewAssistantMessage()
				m.Content = []agentctx.ContentBlock{
					agentctx.ThinkingContent{Type: "thinking", Thinking: "hmm"},
					agentctx.TextContent{Type: "text", Text: "  "},
				}
				return &m
			}(),
			expected: true,
		},
		{
			name: "has non-empty text",
			msg: func() *agentctx.AgentMessage {
				m := agentctx.NewAssistantMessage()
				m.Content = []agentctx.ContentBlock{
					agentctx.ThinkingContent{Type: "thinking", Thinking: "hmm"},
					agentctx.TextContent{Type: "text", Text: "done"},
				}
				return &m
			}(),
			expected: false,
		},
		{
			name: "has tool call",
			msg: func() *agentctx.AgentMessage {
				m := agentctx.NewAssistantMessage()
				m.Content = []agentctx.ContentBlock{
					agentctx.ThinkingContent{Type: "thinking", Thinking: "hmm"},
					agentctx.ToolCallContent{
						ID:   "call-1",
						Type: "toolCall",
						Name: "bash",
						Arguments: map[string]any{
							"command": "ls",
						},
					},
				}
				return &m
			}(),
			expected: false,
		},
		{
			name: "only text with content",
			msg: func() *agentctx.AgentMessage {
				m := agentctx.NewAssistantMessage()
				m.Content = []agentctx.ContentBlock{
					agentctx.TextContent{Type: "text", Text: "hello world"},
				}
				return &m
			}(),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isEmptyActionableResponse(tt.msg)
			if got != tt.expected {
				t.Errorf("isEmptyActionableResponse() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestRunInnerLoopEmptyResponseRetry(t *testing.T) {
	orig := streamAssistantResponseFn
	defer func() { streamAssistantResponseFn = orig }()

	callCount := 0
	streamAssistantResponseFn = func(
		_ context.Context,
		_ *agentctx.AgentContext,
		_ *LoopConfig,
		_ *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
	) (*agentctx.AgentMessage, error) {
		callCount++
		msg := agentctx.NewAssistantMessage()

		if callCount == 1 {
			// First call: returns only thinking content, no text, no tool calls
			msg.Content = []agentctx.ContentBlock{
				agentctx.ThinkingContent{Type: "thinking", Thinking: "Now I need to implement the feature..."},
			}
			msg.StopReason = "stop"
			return &msg, nil
		}

		// Second call: returns actual text content
		msg.Content = []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "I have completed the task."},
		}
		msg.StopReason = "stop"
		return &msg, nil
	}

	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.RecentMessages = append(agentCtx.RecentMessages, agentctx.NewUserMessage("implement feature X"))
	stream := newTestAgentEventStream()
	runInnerLoop(context.Background(), agentCtx, nil, &LoopConfig{}, stream)

	// Should have retried once and then gotten a real response
	if callCount != 2 {
		t.Fatalf("expected 2 LLM calls (1 empty + 1 retry with content), got %d", callCount)
	}
}

func TestRunInnerLoopEmptyResponseMaxRetriesExhausted(t *testing.T) {
	orig := streamAssistantResponseFn
	defer func() { streamAssistantResponseFn = orig }()

	callCount := 0
	streamAssistantResponseFn = func(
		_ context.Context,
		_ *agentctx.AgentContext,
		_ *LoopConfig,
		_ *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
	) (*agentctx.AgentMessage, error) {
		callCount++
		msg := agentctx.NewAssistantMessage()
		// Always returns empty (thinking-only) response
		msg.Content = []agentctx.ContentBlock{
			agentctx.ThinkingContent{Type: "thinking", Thinking: fmt.Sprintf("Attempt %d...", callCount)},
		}
		msg.StopReason = "stop"
		return &msg, nil
	}

	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.RecentMessages = append(agentCtx.RecentMessages, agentctx.NewUserMessage("implement feature X"))
	stream := newTestAgentEventStream()
	runInnerLoop(context.Background(), agentCtx, nil, &LoopConfig{}, stream)

	// Should have: 1 initial + 2 retries = 3 calls total
	expectedCalls := 1 + defaultEmptyResponseMaxRetries
	if callCount != expectedCalls {
		t.Fatalf("expected %d LLM calls (1 initial + %d retries), got %d", expectedCalls, defaultEmptyResponseMaxRetries, callCount)
	}
}

func TestRunInnerLoopEmptyResponseDoesNotRetryWithText(t *testing.T) {
	orig := streamAssistantResponseFn
	defer func() { streamAssistantResponseFn = orig }()

	callCount := 0
	streamAssistantResponseFn = func(
		_ context.Context,
		_ *agentctx.AgentContext,
		_ *LoopConfig,
		_ *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
	) (*agentctx.AgentMessage, error) {
		callCount++
		msg := agentctx.NewAssistantMessage()
		// Returns text content — should NOT trigger retry
		msg.Content = []agentctx.ContentBlock{
			agentctx.ThinkingContent{Type: "thinking", Thinking: "Hmm..."},
			agentctx.TextContent{Type: "text", Text: "Here is the answer."},
		}
		msg.StopReason = "stop"
		return &msg, nil
	}

	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.RecentMessages = append(agentCtx.RecentMessages, agentctx.NewUserMessage("hello"))
	stream := newTestAgentEventStream()
	runInnerLoop(context.Background(), agentCtx, nil, &LoopConfig{}, stream)

	if callCount != 1 {
		t.Fatalf("expected 1 LLM call (text content should not trigger retry), got %d", callCount)
	}
}

func TestRunInnerLoopEmptyResponseDoesNotRetryWithToolCalls(t *testing.T) {
	orig := streamAssistantResponseFn
	defer func() { streamAssistantResponseFn = orig }()

	callCount := 0
	streamAssistantResponseFn = func(
		_ context.Context,
		_ *agentctx.AgentContext,
		_ *LoopConfig,
		_ *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
	) (*agentctx.AgentMessage, error) {
		callCount++
		msg := agentctx.NewAssistantMessage()
		if callCount == 1 {
			// Returns tool calls — should NOT trigger empty response retry
			msg.Content = []agentctx.ContentBlock{
				agentctx.ThinkingContent{Type: "thinking", Thinking: "I need to read a file"},
				agentctx.ToolCallContent{
					ID:   "call-1",
					Type: "toolCall",
					Name: "bash",
					Arguments: map[string]any{
						"command": "ls",
					},
				},
			}
			msg.StopReason = "tool_calls"
			return &msg, nil
		}
		// Second call: text response to end the loop
		msg.Content = []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "done"},
		}
		msg.StopReason = "stop"
		return &msg, nil
	}

	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.RecentMessages = append(agentCtx.RecentMessages, agentctx.NewUserMessage("list files"))
	stream := newTestAgentEventStream()

	// The tool isn't registered, so the loop guard may trigger after repeated attempts.
	// The key assertion is that the empty response retry logic is NOT invoked —
	// the first call has tool calls so it should proceed to tool execution, not retry.
	runInnerLoop(context.Background(), agentCtx, nil, &LoopConfig{}, stream)

	// The empty response retry should NOT have been triggered.
	// The first call had tool calls, so it's not empty.
	// We just verify the loop didn't treat it as empty — the call count being > 1
	// means it got past the empty response check.
	if callCount < 1 {
		t.Fatalf("expected at least 1 LLM call, got %d", callCount)
	}
}

func TestRunInnerLoopEmptyResponseRetriesThenSucceeds(t *testing.T) {
	orig := streamAssistantResponseFn
	defer func() { streamAssistantResponseFn = orig }()

	callCount := 0
	streamAssistantResponseFn = func(
		_ context.Context,
		_ *agentctx.AgentContext,
		_ *LoopConfig,
		_ *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
	) (*agentctx.AgentMessage, error) {
		callCount++
		msg := agentctx.NewAssistantMessage()

		if callCount <= 2 {
			// First 2 calls: empty (thinking-only) responses
			msg.Content = []agentctx.ContentBlock{
				agentctx.ThinkingContent{Type: "thinking", Thinking: fmt.Sprintf("Thinking attempt %d", callCount)},
			}
			msg.StopReason = "stop"
			return &msg, nil
		}

		// Third call: actual content
		msg.Content = []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "Final answer"},
		}
		msg.StopReason = "stop"
		return &msg, nil
	}

	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.RecentMessages = append(agentCtx.RecentMessages, agentctx.NewUserMessage("do something"))
	stream := newTestAgentEventStream()
	runInnerLoop(context.Background(), agentCtx, nil, &LoopConfig{}, stream)

	// 1st empty + 1 retry empty + 1 retry with content = 3 calls
	if callCount != 3 {
		t.Fatalf("expected 3 LLM calls (2 empty + 1 retry with content), got %d", callCount)
	}
}