package compact

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

// sseResponse builds a Server-Sent Events response that returns an
// LLM "no_action" tool call, which is the simplest valid context-mgmt response.
func sseNoActionResponse() string {
	// Simulate an OpenAI-compatible streaming response that calls no_action.
	chunks := []string{
		`data: {"id":"chatcmpl-test","object":"chat.completion.chunk","choices":[{"index":0,"delta":{"role":"assistant","content":null,"tool_calls":[{"index":0,"id":"call_noaction","type":"function","function":{"name":"no_action","arguments":""}}]},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl-test","object":"chat.completion.chunk","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
	}
	return strings.Join(chunks, "\n\n") + "\n\n"
}

func TestCompactWithCtx_RetriesOn500ThenSucceeds(t *testing.T) {
	var attempts int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&attempts, 1)
		if n == 1 {
			// First attempt: return 500
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprintf(w, `{"error":{"message":"Operation failed"}}`)
			return
		}
		// Second attempt: return success with no_action tool call
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseNoActionResponse())
	}))
	defer server.Close()

	model := llm.Model{
		ID:            "test-model",
		ContextWindow: 200000,
		BaseURL:       server.URL,
		API:           "openai",
	}

	agentCtx := agentctx.NewAgentContext("system")
	for i := 0; i < 10; i++ {
		agentCtx.RecentMessages = append(agentCtx.RecentMessages, agentctx.NewUserMessage("hello"))
	}
	agentCtx.AgentState.ToolCallsSinceLastTrigger = 20

	ctxManager := NewContextManager(DefaultContextManagerConfig(), model, "test-key", 200000, "system", nil)

	// Use short max retries and backoff via a child context with timeout
	result, err := ctxManager.CompactWithCtx(context.Background(), agentCtx)

	if err != nil {
		t.Fatalf("expected success after retry, got error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if atomic.LoadInt32(&attempts) != 2 {
		t.Fatalf("expected 2 attempts (1 fail + 1 success), got %d", atomic.LoadInt32(&attempts))
	}
}

func TestCompactWithCtx_AllRetriesExhausted(t *testing.T) {
	var attempts int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, `{"error":{"message":"Operation failed"}}`)
	}))
	defer server.Close()

	model := llm.Model{
		ID:            "test-model",
		ContextWindow: 200000,
		BaseURL:       server.URL,
		API:           "openai",
	}

	agentCtx := agentctx.NewAgentContext("system")
	for i := 0; i < 10; i++ {
		agentCtx.RecentMessages = append(agentCtx.RecentMessages, agentctx.NewUserMessage("hello"))
	}
	agentCtx.AgentState.ToolCallsSinceLastTrigger = 20

	ctxManager := NewContextManager(DefaultContextManagerConfig(), model, "test-key", 200000, "system", nil)

	result, err := ctxManager.CompactWithCtx(context.Background(), agentCtx)

	if err == nil {
		t.Fatal("expected error when all retries exhausted, got nil")
	}
	if result != nil {
		t.Fatal("expected nil result on failure")
	}
	if !strings.Contains(err.Error(), "context management LLM call failed") {
		t.Fatalf("error should wrap with context management prefix, got: %v", err)
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("error should mention 500 status, got: %v", err)
	}
	if atomic.LoadInt32(&attempts) != 3 {
		t.Fatalf("expected 3 attempts (all failed), got %d", atomic.LoadInt32(&attempts))
	}
}

func TestCompactWithCtx_NonRetryableErrorNotRetried(t *testing.T) {
	var attempts int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		// 401 Unauthorized — non-retryable
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprintf(w, `{"error":{"message":"Invalid API key"}}`)
	}))
	defer server.Close()

	model := llm.Model{
		ID:            "test-model",
		ContextWindow: 200000,
		BaseURL:       server.URL,
		API:           "openai",
	}

	agentCtx := agentctx.NewAgentContext("system")
	for i := 0; i < 10; i++ {
		agentCtx.RecentMessages = append(agentCtx.RecentMessages, agentctx.NewUserMessage("hello"))
	}
	agentCtx.AgentState.ToolCallsSinceLastTrigger = 20

	ctxManager := NewContextManager(DefaultContextManagerConfig(), model, "test-key", 200000, "system", nil)

	result, err := ctxManager.CompactWithCtx(context.Background(), agentCtx)

	if err == nil {
		t.Fatal("expected error for 401, got nil")
	}
	if result != nil {
		t.Fatal("expected nil result on failure")
	}
	if atomic.LoadInt32(&attempts) != 1 {
		t.Fatalf("expected 1 attempt (non-retryable), got %d", atomic.LoadInt32(&attempts))
	}
}

func TestCompactWithCtx_ContextCancellationStopsRetry(t *testing.T) {
	var attempts int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, `{"error":{"message":"Operation failed"}}`)
	}))
	defer server.Close()

	model := llm.Model{
		ID:            "test-model",
		ContextWindow: 200000,
		BaseURL:       server.URL,
		API:           "openai",
	}

	agentCtx := agentctx.NewAgentContext("system")
	for i := 0; i < 10; i++ {
		agentCtx.RecentMessages = append(agentCtx.RecentMessages, agentctx.NewUserMessage("hello"))
	}
	agentCtx.AgentState.ToolCallsSinceLastTrigger = 20

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel after first attempt completes (the backoff sleep will detect cancellation)
	go func() {
		time.Sleep(1 * time.Second)
		cancel()
	}()

	ctxManager := NewContextManager(DefaultContextManagerConfig(), model, "test-key", 200000, "system", nil)

	result, err := ctxManager.CompactWithCtx(ctx, agentCtx)

	if err == nil {
		t.Fatal("expected error due to context cancellation, got nil")
	}
	if result != nil {
		t.Fatal("expected nil result on cancellation")
	}
	// Should have made at most 2 attempts (first fail, then cancelled during backoff)
	n := atomic.LoadInt32(&attempts)
	if n > 2 {
		t.Fatalf("expected at most 2 attempts before cancellation, got %d", n)
	}
}
