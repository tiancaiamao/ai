package compact

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
)

// --- SetAgentLLMContext setter ---

func TestSetAgentLLMContext(t *testing.T) {
	c := NewCompactor(DefaultConfig(), llm.Model{}, "k", "sys", 0)
	if c.agentSystemPrompt != "" || c.agentContextPrefix != "" || c.thinkingLevel != "" || c.messageConverter != nil {
		t.Fatal("expected zero-value fields before SetAgentLLMContext")
	}

	conv := func(msgs []agentctx.AgentMessage) []llm.LLMMessage { return nil }
	c.SetAgentLLMContext("agent-sys", "prefix", "high", conv)

	if c.agentSystemPrompt != "agent-sys" {
		t.Errorf("agentSystemPrompt = %q, want %q", c.agentSystemPrompt, "agent-sys")
	}
	if c.agentContextPrefix != "prefix" {
		t.Errorf("agentContextPrefix = %q, want %q", c.agentContextPrefix, "prefix")
	}
	if c.thinkingLevel != "high" {
		t.Errorf("thinkingLevel = %q, want %q", c.thinkingLevel, "high")
	}
	if c.messageConverter == nil {
		t.Error("messageConverter should be non-nil")
	}
}

// --- Cache-friendly request format (the core of this change) ---
//
// When SetAgentLLMContext is called, the summary request must:
//   1. Use the main agent's system prompt (not the compact system prompt)
//   2. Send messages in real role-based format (not serialized text)
//   3. Prepend the agent context prefix before the first user message
//   4. Append the compact instruction as a trailing user message
// These properties ensure the request shares a prefix with the main
// conversation for provider prefix-cache hits.

// captureRequestServer returns an httptest server that captures the request
// body into the provided pointer. The server always responds with a valid
// SSE summary stream.
func captureRequestServer(captured *map[string]any) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		*captured = body
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseTextResponse("summary-result"))
	}))
}

func TestGenerateSummary_CacheFriendlyRequestFormat(t *testing.T) {
	var captured map[string]any
	server := captureRequestServer(&captured)
	defer server.Close()

	model := llm.Model{ID: "m", ContextWindow: 200000, BaseURL: server.URL, API: "openai"}
	c := NewCompactor(DefaultConfig(), model, "k", "sys", 0)
	c.SetAgentLLMContext("AGENT-SYSTEM-PROMPT", "AGENT-CONTEXT-PREFIX", "high", nil)
	// nil converter → falls back to convertAgentMessagesToLLM

	msgs := []agentctx.AgentMessage{
		agentctx.NewUserMessage("hello"),
		agentctx.NewAssistantMessage(),
	}
	_, err := c.GenerateSummary(msgs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rawMessages, ok := captured["messages"].([]any)
	if !ok {
		t.Fatalf("expected messages array, got %T", captured["messages"])
	}

	// --- Property 1: system prompt is the agent's, prepended by the LLM client ---
	// For non-reasoning models, the thinking instruction is appended (matching
	// the main agent loop) so the cache prefix matches exactly.
	sysMsg, _ := rawMessages[0].(map[string]any)
	if sysMsg["role"] != "system" {
		t.Errorf("messages[0].role = %v, want %q", sysMsg["role"], "system")
	}
	sysContent, _ := sysMsg["content"].(string)
	if !strings.HasPrefix(sysContent, "AGENT-SYSTEM-PROMPT") {
		t.Errorf("messages[0].content = %q, want prefix %q", sysContent, "AGENT-SYSTEM-PROMPT")
	}
	if !strings.Contains(sysContent, "Thinking level is high") {
		t.Errorf("messages[0].content = %q, want thinking instruction appended", sysContent)
	}

	// --- Property 2: context prefix is the next message (before first real user) ---
	prefixMsg, _ := rawMessages[1].(map[string]any)
	if prefixMsg["role"] != "user" {
		t.Errorf("messages[1].role = %v, want %q (prefix)", prefixMsg["role"], "user")
	}
	if prefixMsg["content"] != "AGENT-CONTEXT-PREFIX" {
		t.Errorf("messages[1].content = %v, want context prefix", prefixMsg["content"])
	}

	// --- Property 3: real conversation messages follow (role-based, not serialized text) ---
	userMsg, _ := rawMessages[2].(map[string]any)
	if userMsg["role"] != "user" || userMsg["content"] != "hello" {
		t.Errorf("messages[2] = %v, want real user message with content 'hello'", userMsg)
	}

	// --- Property 4: compact instruction is appended as the last user message ---
	lastMsg, _ := rawMessages[len(rawMessages)-1].(map[string]any)
	if lastMsg["role"] != "user" {
		t.Errorf("last message role = %v, want %q (compact instruction)", lastMsg["role"], "user")
	}
	lastContent, _ := lastMsg["content"].(string)
	if !strings.Contains(lastContent, "summary") {
		t.Errorf("last message should contain compact instruction, got: %s", lastContent[:min(100, len(lastContent))])
	}
}

func TestGenerateSummary_FallbackWithoutAgentContext(t *testing.T) {
	// Without SetAgentLLMContext, the compactor should still work using
	// the fallback converter and the compact system prompt.
	var captured map[string]any
	server := captureRequestServer(&captured)
	defer server.Close()

	model := llm.Model{ID: "m", ContextWindow: 200000, BaseURL: server.URL, API: "openai"}
	c := NewCompactor(DefaultConfig(), model, "k", "sys", 0)
	// NO SetAgentLLMContext call

	_, err := c.GenerateSummary([]agentctx.AgentMessage{agentctx.NewUserMessage("hi")})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rawMessages, _ := captured["messages"].([]any)
	// system prompt should be the compact system prompt (fallback)
	sysMsg, _ := rawMessages[0].(map[string]any)
	if sysMsg["content"] == "AGENT-SYSTEM-PROMPT" {
		t.Error("should not use agent system prompt in fallback mode")
	}
	// No prefix message: messages[1] should be the real user message, not a prefix
	if len(rawMessages) > 1 {
		second, _ := rawMessages[1].(map[string]any)
		if second["content"] != "hi" {
			t.Errorf("messages[1].content = %v, want 'hi' (no prefix in fallback)", second["content"])
		}
	}
}

func TestGenerateSummary_PreviousSummaryInInstruction(t *testing.T) {
	// When previousSummary is non-empty, the trailing instruction should
	// include the previous summary content (incremental update path).
	var captured map[string]any
	server := captureRequestServer(&captured)
	defer server.Close()

	model := llm.Model{ID: "m", ContextWindow: 200000, BaseURL: server.URL, API: "openai"}
	c := NewCompactor(DefaultConfig(), model, "k", "sys", 0)
	c.SetAgentLLMContext("agent-sys", "", "high", nil)

	_, err := c.GenerateSummaryWithPrevious(
		[]agentctx.AgentMessage{agentctx.NewUserMessage("new work")},
		"PREVIOUS-SUMMARY-CONTENT",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	rawMessages, _ := captured["messages"].([]any)
	lastMsg, _ := rawMessages[len(rawMessages)-1].(map[string]any)
	lastContent, _ := lastMsg["content"].(string)
	if !strings.Contains(lastContent, "PREVIOUS-SUMMARY-CONTENT") {
		t.Errorf("trailing instruction should contain previous summary, got: %s", lastContent[:min(100, len(lastContent))])
	}
}

// --- Fallback converter: convertAgentMessagesToLLM ---

func TestConvertAgentMessagesToLLM(t *testing.T) {
	asst := agentctx.NewAssistantMessage()
	asst.Content = []agentctx.ContentBlock{
		agentctx.TextContent{Type: "text", Text: "assistant reply"},
		agentctx.ToolCallContent{
			ID:        "call_1",
			Type:      "toolCall",
			Name:      "grep",
			Arguments: map[string]any{"pattern": "foo"},
		},
	}
	toolResult := agentctx.NewToolResultMessage("call_1", "grep",
		[]agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "matched lines"}},
		false,
	)

	msgs := []agentctx.AgentMessage{
		agentctx.NewUserMessage("query"),
		asst,
		toolResult,
	}

	got := convertAgentMessagesToLLM(msgs)
	if len(got) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(got))
	}

	// user
	if got[0].Role != "user" || got[0].Content != "query" {
		t.Errorf("msg[0] = %+v, want role=user content=query", got[0])
	}

	// assistant with text + tool call
	if got[1].Role != "assistant" {
		t.Errorf("msg[1].Role = %q, want assistant", got[1].Role)
	}
	if got[1].Content != "assistant reply" {
		t.Errorf("msg[1].Content = %q, want 'assistant reply'", got[1].Content)
	}
	if len(got[1].ToolCalls) != 1 {
		t.Fatalf("msg[1] ToolCalls len = %d, want 1", len(got[1].ToolCalls))
	}
	if got[1].ToolCalls[0].Function.Name != "grep" {
		t.Errorf("tool call name = %q, want grep", got[1].ToolCalls[0].Function.Name)
	}

	// tool result → role "tool"
	if got[2].Role != "tool" {
		t.Errorf("msg[2].Role = %q, want tool", got[2].Role)
	}
	if got[2].ToolCallID != "call_1" {
		t.Errorf("msg[2].ToolCallID = %q, want call_1", got[2].ToolCallID)
	}
	if got[2].Content != "matched lines" {
		t.Errorf("msg[2].Content = %q, want 'matched lines'", got[2].Content)
	}
}

func TestConvertAgentMessagesToLLM_SkipsInvisible(t *testing.T) {
	invisible := agentctx.NewUserMessage("hidden").WithVisibility(false, true)
	got := convertAgentMessagesToLLM([]agentctx.AgentMessage{invisible})
	if len(got) != 0 {
		t.Errorf("expected 0 messages (invisible filtered), got %d", len(got))
	}
}

func TestConvertAgentMessagesToLLM_Thinking(t *testing.T) {
	asst := agentctx.NewAssistantMessage()
	asst.Content = []agentctx.ContentBlock{
		agentctx.ThinkingContent{Type: "thinking", Thinking: "reasoning here"},
		agentctx.TextContent{Type: "text", Text: "answer"},
	}
	got := convertAgentMessagesToLLM([]agentctx.AgentMessage{asst})
	if len(got) != 1 || got[0].Thinking != "reasoning here" {
		t.Errorf("thinking not preserved: %+v", got)
	}
}

// --- insertBeforeFirstUserMessage ---

func TestInsertBeforeFirstUserMessage(t *testing.T) {
	msg := llm.LLMMessage{Role: "user", Content: "PREFIX"}

	t.Run("empty", func(t *testing.T) {
		got := insertBeforeFirstUserMessage(nil, msg)
		if len(got) != 1 || got[0].Content != "PREFIX" {
			t.Errorf("got %+v", got)
		}
	})

	t.Run("no_user_message", func(t *testing.T) {
		input := []llm.LLMMessage{
			{Role: "assistant", Content: "a"},
		}
		got := insertBeforeFirstUserMessage(input, msg)
		if len(got) != 2 || got[0].Content != "PREFIX" {
			t.Errorf("expected prefix first when no user, got %+v", got)
		}
	})

	t.Run("user_at_start", func(t *testing.T) {
		input := []llm.LLMMessage{
			{Role: "user", Content: "u1"},
			{Role: "assistant", Content: "a1"},
		}
		got := insertBeforeFirstUserMessage(input, msg)
		if len(got) != 3 || got[0].Content != "PREFIX" || got[1].Content != "u1" {
			t.Errorf("prefix should be before first user, got %+v", got)
		}
	})

	t.Run("user_after_assistant", func(t *testing.T) {
		input := []llm.LLMMessage{
			{Role: "system", Content: "s"},
			{Role: "user", Content: "u1"},
		}
		got := insertBeforeFirstUserMessage(input, msg)
		if len(got) != 3 || got[0].Content != "s" || got[1].Content != "PREFIX" || got[2].Content != "u1" {
			t.Errorf("prefix should be between system and first user, got %+v", got)
		}
	})
}

// TestGenerateSummary_ThinkingInstruction verifies that the system prompt in
// the summary request includes the thinking instruction for non-reasoning
// models (matching the main agent loop) and omits it for reasoning models.
func TestGenerateSummary_ThinkingInstruction(t *testing.T) {
	makeCompactor := func(reasoning bool) (*Compactor, *map[string]any, *httptest.Server) {
		var captured map[string]any
		server := captureRequestServer(&captured)
		model := llm.Model{ID: "m", ContextWindow: 200000, BaseURL: server.URL, API: "openai", Reasoning: reasoning}
		c := NewCompactor(DefaultConfig(), model, "k", "sys", 0)
		c.SetAgentLLMContext("BASE-SYS", "prefix", "high", nil)
		return c, &captured, server
	}

	t.Run("non_reasoning_model_gets_thinking_instruction", func(t *testing.T) {
		c, captured, server := makeCompactor(false)
		defer server.Close()
		_, _ = c.GenerateSummary([]agentctx.AgentMessage{agentctx.NewUserMessage("hi")})
		msgs := (*captured)["messages"].([]any)
		sysContent := msgs[0].(map[string]any)["content"].(string)
		if !strings.Contains(sysContent, "Thinking level is high") {
			t.Errorf("non-reasoning model should have thinking instruction, got %q", sysContent)
		}
	})

	t.Run("reasoning_model_skips_thinking_instruction", func(t *testing.T) {
		c, captured, server := makeCompactor(true)
		defer server.Close()
		_, _ = c.GenerateSummary([]agentctx.AgentMessage{agentctx.NewUserMessage("hi")})
		msgs := (*captured)["messages"].([]any)
		sysContent := msgs[0].(map[string]any)["content"].(string)
		if sysContent != "BASE-SYS" {
			t.Errorf("reasoning model should NOT have thinking instruction, got %q", sysContent)
		}
	})
}
