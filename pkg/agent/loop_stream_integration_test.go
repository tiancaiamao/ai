package agent

import (
	"context"
	"encoding/json"
	"fmt"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tiancaiamao/ai/pkg/llm"
)

func TestStreamAssistantResponse_RecoversToolCallFromThinkingDelta(t *testing.T) {
	thinking := "我需要查看正确的行。让我使用 sed 命令来查看第1370-1385行：\n<tool_call>bash\n<arg_key>command</arg_key>\n<arg_value>sed -n '1370,1385p' Client/GameInit.cpp</arg_value>\n</tool_call>"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("unexpected authorization header: %q", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":%q}}]}\n\n", thinking)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":6,\"total_tokens\":16}}\n\n")
	}))
	defer server.Close()

	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.Messages = append(agentCtx.Messages, agentctx.NewUserMessage("show me lines"))

	config := &LoopConfig{
		Model: llm.Model{
			ID:       "test-model",
			Provider: "test",
			BaseURL:  server.URL,
			API:      "openai-completions",
		},
		APIKey:        "test-key",
		ThinkingLevel: "high",
	}

	stream := newTestAgentEventStream()
	msg, err := streamAssistantResponse(context.Background(), agentCtx, config, stream)
	if err != nil {
		t.Fatalf("streamAssistantResponse returned error: %v", err)
	}

	calls := msg.ExtractToolCalls()
	if len(calls) != 1 {
		t.Fatalf("expected one recovered tool call, got %d", len(calls))
	}
	if calls[0].Name != "bash" {
		t.Fatalf("expected recovered tool name bash, got %q", calls[0].Name)
	}
	if got := calls[0].Arguments["command"]; got != "sed -n '1370,1385p' Client/GameInit.cpp" {
		t.Fatalf("unexpected recovered command: %v", got)
	}
}

func TestStreamAssistantResponse_RuntimeStateInjectedAsUserMessage(t *testing.T) {
	sessionDir := t.TempDir()
	wm := agentctx.NewLLMContext(sessionDir)
	if _, err := wm.Load(); err != nil {
		t.Fatalf("failed to initialize llm context: %v", err)
	}

	var observedMessages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("failed to read request body: %v", err)
		}
		var req struct {
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("failed to decode request JSON: %v", err)
		}
		observedMessages = req.Messages

		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":2,\"total_tokens\":14}}\n\n")
	}))
	defer server.Close()

	agentCtx := agentctx.NewAgentContext("static system prompt")
	agentCtx.LLMContext = wm
	agentCtx.Messages = append(agentCtx.Messages, agentctx.NewUserMessage("hello"))

	config := &LoopConfig{
		Model: llm.Model{
			ID:       "test-model",
			Provider: "test",
			BaseURL:  server.URL,
			API:      "openai-completions",
		},
		APIKey:        "test-key",
		ThinkingLevel: "high",
		ContextWindow: 128000,
	}

	stream := newTestAgentEventStream()
	msg, err := streamAssistantResponse(context.Background(), agentCtx, config, stream)
	if err != nil {
		t.Fatalf("streamAssistantResponse returned error: %v", err)
	}
	if got := strings.TrimSpace(msg.ExtractText()); got != "ok" {
		t.Fatalf("expected assistant text 'ok', got %q", got)
	}

	if len(observedMessages) < 2 {
		t.Fatalf("expected at least 2 messages (system + runtime user), got %d", len(observedMessages))
	}
	if observedMessages[0].Role != "system" {
		t.Fatalf("expected first message to be system, got %q", observedMessages[0].Role)
	}
	if observedMessages[1].Role != "user" {
		t.Fatalf("expected second message to be runtime user message, got %q", observedMessages[1].Role)
	}

	var systemContent string
	if err := json.Unmarshal(observedMessages[0].Content, &systemContent); err != nil {
		t.Fatalf("failed to parse system content: %v", err)
	}
	if strings.Contains(systemContent, "<llm_context>") || strings.Contains(systemContent, "<runtime_state>") {
		t.Fatalf("expected runtime payload outside system prompt, got system content: %q", systemContent)
	}

	var runtimeContent string
	if err := json.Unmarshal(observedMessages[1].Content, &runtimeContent); err != nil {
		t.Fatalf("failed to parse runtime content: %v", err)
	}
	// After the refactor, llm_context is NOT injected in normal requests
	// It's only injected after compact for recovery
	if strings.Contains(runtimeContent, "<llm_context>") {
		t.Fatalf("expected runtime user content to NOT include llm_context in normal request, got: %q", runtimeContent)
	}
	// runtime_state should still be injected
	if !strings.Contains(runtimeContent, "<runtime_state>") {
		t.Fatalf("expected runtime user content to include runtime_state, got: %q", runtimeContent)
	}
}

func TestStreamAssistantResponse_LLMContextInjectedAfterCompact(t *testing.T) {
	sessionDir := t.TempDir()
	wm := agentctx.NewLLMContext(sessionDir)
	if _, err := wm.Load(); err != nil {
		t.Fatalf("failed to initialize llm context: %v", err)
	}

	var observedMessages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("failed to read request body: %v", err)
		}
		var req struct {
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("failed to decode request JSON: %v", err)
		}
		observedMessages = req.Messages

		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":12,\"completion_tokens\":2,\"total_tokens\":14}}\n\n")
	}))
	defer server.Close()

	agentCtx := agentctx.NewAgentContext("static system prompt")
	agentCtx.LLMContext = wm
	// Simulate post-compact recovery state
	agentCtx.PostCompactRecovery = true
	agentCtx.Messages = append(agentCtx.Messages, agentctx.NewUserMessage("continue"))

	config := &LoopConfig{
		Model: llm.Model{
			ID:       "test-model",
			Provider: "test",
			BaseURL:  server.URL,
			API:      "openai-completions",
		},
		APIKey:        "test-key",
		ThinkingLevel: "high",
		ContextWindow: 128000,
	}

	stream := newTestAgentEventStream()
	msg, err := streamAssistantResponse(context.Background(), agentCtx, config, stream)
	if err != nil {
		t.Fatalf("streamAssistantResponse returned error: %v", err)
	}
	if got := strings.TrimSpace(msg.ExtractText()); got != "ok" {
		t.Fatalf("expected assistant text 'ok', got %q", got)
	}

	// After compact, llm_context SHOULD be injected
	var foundLLMContext bool
	for _, observed := range observedMessages {
		if observed.Role != "user" {
			continue
		}
		var content string
		if err := json.Unmarshal(observed.Content, &content); err != nil {
			continue
		}
		if strings.Contains(content, "<llm_context>") {
			foundLLMContext = true
			break
		}
	}

	if !foundLLMContext {
		t.Fatalf("expected llm_context to be injected after compact (PostCompactRecovery=true)")
	}

	// Verify flag was reset
	if agentCtx.PostCompactRecovery {
		t.Fatalf("expected PostCompactRecovery to be reset to false after injection")
	}
}

func TestStreamAssistantResponse_KeepsSeveralRecentRealUserTurns(t *testing.T) {
	sessionDir := t.TempDir()
	wm := agentctx.NewLLMContext(sessionDir)
	if _, err := wm.Load(); err != nil {
		t.Fatalf("failed to initialize llm context: %v", err)
	}
	// Mark llm context as maintained to mirror the production path where
	// runtime state is injected and history selection previously regressed.
	if err := wm.WriteContent("# LLM Context\n\n## 当前任务\n- keep recent user turns\n"); err != nil {
		t.Fatalf("failed to write llm context content: %v", err)
	}

	var observedMessages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("failed to read request body: %v", err)
		}
		var req struct {
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("failed to decode request JSON: %v", err)
		}
		observedMessages = req.Messages

		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":20,\"completion_tokens\":2,\"total_tokens\":22}}\n\n")
	}))
	defer server.Close()

	agentCtx := agentctx.NewAgentContext("static system prompt")
	agentCtx.LLMContext = wm

	userTurn1 := "user-turn-1: first requirement"
	userTurn2 := "user-turn-2: second requirement"
	userTurn3 := "user-turn-3: final requirement"

	assistantCall1 := agentctx.NewAssistantMessage()
	assistantCall1.Content = []agentctx.ContentBlock{
		agentctx.ToolCallContent{
			ID:        "call-1",
			Type:      "toolCall",
			Name:      "read",
			Arguments: map[string]any{"path": "a.txt"},
		},
	}
	assistantCall1.StopReason = "tool_calls"

	assistantCall2 := agentctx.NewAssistantMessage()
	assistantCall2.Content = []agentctx.ContentBlock{
		agentctx.ToolCallContent{
			ID:        "call-2",
			Type:      "toolCall",
			Name:      "read",
			Arguments: map[string]any{"path": "b.txt"},
		},
	}
	assistantCall2.StopReason = "tool_calls"

	largeToolOutput := strings.Repeat("X", 22000)

	agentCtx.Messages = append(agentCtx.Messages,
		agentctx.NewUserMessage(userTurn1),
		assistantCall1,
		agentctx.NewToolResultMessage("call-1", "read", []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: largeToolOutput},
		}, false),
		agentctx.NewUserMessage(userTurn2),
		assistantCall2,
		agentctx.NewToolResultMessage("call-2", "read", []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: largeToolOutput},
		}, false),
		agentctx.NewUserMessage(userTurn3),
	)

	config := &LoopConfig{
		Model: llm.Model{
			ID:       "test-model",
			Provider: "test",
			BaseURL:  server.URL,
			API:      "openai-completions",
		},
		APIKey:        "test-key",
		ThinkingLevel: "high",
		ContextWindow: 200000,
	}

	stream := newTestAgentEventStream()
	msg, err := streamAssistantResponse(context.Background(), agentCtx, config, stream)
	if err != nil {
		t.Fatalf("streamAssistantResponse returned error: %v", err)
	}
	if got := strings.TrimSpace(msg.ExtractText()); got != "ok" {
		t.Fatalf("expected assistant text 'ok', got %q", got)
	}

	realUserTurns := make([]string, 0, 3)
	for _, observed := range observedMessages {
		if observed.Role != "user" {
			continue
		}
		var content string
		if err := json.Unmarshal(observed.Content, &content); err != nil {
			t.Fatalf("failed to parse user content: %v", err)
		}
		if strings.Contains(content, "<llm_context>") {
			continue // runtime state injection
		}
		if strings.Contains(content, "[system message by agent, not from real user]") {
			continue // reminder message
		}
		realUserTurns = append(realUserTurns, strings.TrimSpace(content))
	}

	if len(realUserTurns) < 3 {
		t.Fatalf("expected at least 3 real user turns in llm request, got %d (%v)", len(realUserTurns), realUserTurns)
	}

	gotTail := realUserTurns[len(realUserTurns)-3:]
	wantTail := []string{userTurn1, userTurn2, userTurn3}
	for i := range wantTail {
		if gotTail[i] != wantTail[i] {
			t.Fatalf("expected tail user turns %v, got %v", wantTail, gotTail)
		}
	}
}
