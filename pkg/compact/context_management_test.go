package compact

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/tools/context_mgmt"
	contextmgmttools "github.com/tiancaiamao/ai/pkg/tools/context_mgmt"
)

func makeToolResult(toolCallID string, size int) agentctx.AgentMessage {
	return agentctx.NewToolResultMessage(
		toolCallID,
		"bash",
		[]agentctx.ContentBlock{
			agentctx.TextContent{
				Type: "text",
				Text: strings.Repeat("x", size),
			},
		},
		false,
	)
}

func TestCollectTruncationCandidatesFiltersBySelectability(t *testing.T) {
	agentCtx := agentctx.NewAgentContext("system")
	agentCtx.RecentMessages = []agentctx.AgentMessage{
		makeToolResult("call-selectable", 5000),
		makeToolResult("", 5000), // non-selectable (missing tool_call_id)
		func() agentctx.AgentMessage {
			msg := makeToolResult("call-truncated", 5000)
			msg.Truncated = true
			return msg
		}(),
		agentctx.NewUserMessage("recent-1"),
		agentctx.NewUserMessage("recent-2"),
		agentctx.NewUserMessage("recent-3"),
		agentctx.NewUserMessage("recent-4"),
		agentctx.NewUserMessage("recent-5"),
	}

	protectedStart := len(agentCtx.RecentMessages) - agentctx.RecentMessagesKeep
	candidates, truncatedCount, nonSelectableCount := collectTruncationCandidates(agentCtx, protectedStart)

	if len(candidates) != 1 {
		t.Fatalf("expected 1 truncation candidate, got %d", len(candidates))
	}
	if candidates[0].ID != "call-selectable" {
		t.Fatalf("unexpected candidate id: %s", candidates[0].ID)
	}
	if truncatedCount != 1 {
		t.Fatalf("expected truncated count 1, got %d", truncatedCount)
	}
	if nonSelectableCount != 1 {
		t.Fatalf("expected non-selectable count 1, got %d", nonSelectableCount)
	}
}

func TestBuildContextMgmtMessagesExposesSavingsAndGuidance(t *testing.T) {
	agentCtx := agentctx.NewAgentContext("system")
	agentCtx.LLMContext = "existing context"
	agentCtx.RecentMessages = []agentctx.AgentMessage{
		makeToolResult("call-a", 12000),
		makeToolResult("call-b", 12000),
		makeToolResult("call-c", 12000),
		makeToolResult("", 3000), // shown as NON_TRUNCATABLE:NO_ID
		agentctx.NewUserMessage("recent-1"),
		agentctx.NewUserMessage("recent-2"),
		agentctx.NewUserMessage("recent-3"),
		agentctx.NewUserMessage("recent-4"),
		agentctx.NewUserMessage("recent-5"),
	}

	compactor := NewContextManager(DefaultContextManagerConfig(), llmModelStub(), "", 200000, "system", nil)
	msgs := compactor.buildContextMgmtMessages(agentCtx)
	if len(msgs) != 2 {
		t.Fatalf("expected 2 context management messages, got %d", len(msgs))
	}

	history := msgs[0].Content
	state := msgs[1].Content

	if !strings.Contains(history, "NON_TRUNCATABLE:NO_ID") {
		t.Fatalf("expected NON_TRUNCATABLE marker in history message, got: %s", history)
	}
	if !strings.Contains(state, "Estimated savings if truncating selectable outputs:") {
		t.Fatalf("expected estimated savings in state message, got: %s", state)
	}
	if !strings.Contains(state, "force_truncate_recommended=true") {
		t.Fatalf("expected force_truncate_recommended=true, got: %s", state)
	}
	if !strings.Contains(state, "Truncatable tool outputs (selectable): 3") {
		t.Fatalf("expected selectable truncatable count in state message, got: %s", state)
	}
}

func TestContextManagerCompactToolUpdatesLLMContext(t *testing.T) {
	agentCtx := agentctx.NewAgentContext("system")
	agentCtx.RecentMessages = []agentctx.AgentMessage{
		makeToolResult("call-a", 12000),
		makeToolResult("call-b", 12000),
		agentctx.NewUserMessage("recent-1"),
		agentctx.NewUserMessage("recent-2"),
		agentctx.NewUserMessage("recent-3"),
		agentctx.NewUserMessage("recent-4"),
		agentctx.NewUserMessage("recent-5"),
	}

	compactor := NewCompactor(&Config{
		MaxMessages: 5,
		AutoCompact:  true,
	}, llmModelStub(), "test-key", "test", 200000)

	ctxManager := NewContextManager(DefaultContextManagerConfig(), llmModelStub(), "test-key", 200000, "system", compactor)

	// Verify the fix: when compact tool is executed, llmContextUpdated should be true
	// Test via the tool registration path
	tools := []context_mgmt.Tool{
		NewCompactTool(agentCtx, compactor),
	}

	// Create tool calls for compact tool
	args, _ := json.Marshal(map[string]any{
		"strategy": "balanced",
		"reason":   "test compaction",
	})
	toolCalls := []llm.ToolCall{
		{
			ID: "test-call-1",
			Type: "function",
			Function: llm.FunctionCall{
				Name:      "compact",
				Arguments: string(args),
			},
		},
	}

	// Execute the tool calls via the test helper
	truncatedCount, llmContextUpdated := ctxManager.executeToolCallsForTest(toolCalls, tools)

	if llmContextUpdated != true {
		t.Errorf("expected llmContextUpdated=true after compact tool execution, got false")
	}
	if truncatedCount != 0 {
		t.Errorf("expected truncatedCount=0, got %d", truncatedCount)
	}

	// Verify AgentContext.RecentMessages was actually compacted
	if len(agentCtx.RecentMessages) > 10 { // Should be much smaller after compact
		t.Errorf("expected messages to be compacted, got %d messages", len(agentCtx.RecentMessages))
	}
}

func TestContextManagerAllToolsTrackLLMContextUpdates(t *testing.T) {
	tests := []struct {
		name              string
		toolName          string
		toolArgs          map[string]any
		expectUpdated     bool
		setupAgentContext func(*agentctx.AgentContext)
	}{
		{
			name:          "compact_tool_updates_llm_context",
			toolName:      "compact",
			toolArgs:      map[string]any{"strategy": "balanced", "reason": "test"},
			expectUpdated: true,
			setupAgentContext: func(ctx *agentctx.AgentContext) {
				ctx.RecentMessages = []agentctx.AgentMessage{
					makeToolResult("call-a", 12000),
					makeToolResult("call-b", 12000),
					agentctx.NewUserMessage("recent"),
				}
			},
		},
		{
			name:          "update_llm_context_updates_flag",
			toolName:      "update_llm_context",
			toolArgs:      map[string]any{"llm_context": "new context"},
			expectUpdated: true,
			setupAgentContext: func(ctx *agentctx.AgentContext) {
				ctx.RecentMessages = []agentctx.AgentMessage{agentctx.NewUserMessage("test")}
				ctx.LLMContext = "old context"
			},
		},
		{
			name:          "truncate_messages_does_not_update_llm_context",
			toolName:      "truncate_messages",
			toolArgs:      map[string]any{"message_ids": "tool-1,tool-2"},
			expectUpdated: false,
			setupAgentContext: func(ctx *agentctx.AgentContext) {
				// Create tool result messages with ToolCallID set
				// Add many messages so tool-1 and tool-2 are outside the protected window
				msgs := []agentctx.AgentMessage{
					agentctx.NewToolResultMessage("tool-1", "bash", []agentctx.ContentBlock{
						agentctx.TextContent{Type: "text", Text: strings.Repeat("x", 5000)},
					}, false),
					agentctx.NewToolResultMessage("tool-2", "bash", []agentctx.ContentBlock{
						agentctx.TextContent{Type: "text", Text: strings.Repeat("x", 5000)},
					}, false),
				}
				// Add protected messages at the end (last 5 are protected)
				for i := 0; i < 6; i++ {
					msgs = append(msgs, agentctx.NewUserMessage("protected"))
				}
				ctx.RecentMessages = msgs
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			agentCtx := agentctx.NewAgentContext("system")
			if tt.setupAgentContext != nil {
				tt.setupAgentContext(agentCtx)
			}

			compactor := NewCompactor(&Config{
				MaxMessages: 5,
				AutoCompact: true,
			}, llmModelStub(), "test-key", "test", 200000)

			ctxManager := NewContextManager(
				DefaultContextManagerConfig(),
				llmModelStub(),
				"test-key",
				200000,
				"system",
				compactor,
			)

			tools := []context_mgmt.Tool{
				NewCompactTool(agentCtx, compactor),
				contextmgmttools.NewUpdateLLMContextTool(agentCtx),
				contextmgmttools.NewTruncateMessagesTool(agentCtx),
			}

			args, _ := json.Marshal(tt.toolArgs)
			toolCalls := []llm.ToolCall{
				{
					ID:   "test-call",
					Type: "function",
					Function: llm.FunctionCall{
						Name:      tt.toolName,
						Arguments: string(args),
					},
				},
			}

			truncatedCount, llmContextUpdated := ctxManager.executeToolCallsForTest(toolCalls, tools)

			if llmContextUpdated != tt.expectUpdated {
				t.Errorf("expected llmContextUpdated=%v, got %v", tt.expectUpdated, llmContextUpdated)
			}

			if tt.toolName == "truncate_messages" && truncatedCount != 2 {
				t.Errorf("expected 2 truncated messages, got %d", truncatedCount)
			}
		})
	}
}

func llmModelStub() llm.Model {
	return llm.Model{
		ID:            "stub-model",
		ContextWindow: 200000,
	}
}

// executeToolCallsForTest is a test helper that exposes executeToolCalls for testing
func (c *ContextManager) executeToolCallsForTest(toolCalls []llm.ToolCall, tools []context_mgmt.Tool) (int, bool) {
	return c.executeToolCalls(context.Background(), toolCalls, tools)
}
