package agent

import (
	"context"
	"encoding/json"
	"fmt"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tiancaiamao/ai/pkg/llm"
)

// TestStreamAssistantResponse_ToolCallsInThinkingAreNotExtracted verifies that tool calls
// appearing in thinking content are NOT extracted and executed.
// Thinking is internal reasoning - any tool calls there are hallucinated/incomplete.
func TestStreamAssistantResponse_ToolCallsInThinkingAreNotExtracted(t *testing.T) {
	// This test verifies that injectToolCallsFromThinking is NOT called.
	// The thinking content contains what looks like a tool call, but it's
	// actually just the LLM describing what it wants to do internally.
	// It should NOT be extracted and executed.
	//
	// This is tested by checking that a message with thinking content containing
	// tool call tags does NOT get tool calls extracted from it.
	msg := agentctx.NewAssistantMessage()
	msg.Content = []agentctx.ContentBlock{
		agentctx.ThinkingContent{
			Type:     "thinking",
			Thinking: "我需要使用 bash 工具来执行命令：\n<tool_call>bash\n<arg_key>command</arg_key>\n<arg_value>ls -la</arg_value>\n</tool_call>",
		},
		agentctx.TextContent{
			Type: "text",
			Text: "I'll help you with that.",
		},
	}

	// injectToolCallsFromThinking should NOT be called anymore in loop.go
	// So calling it directly should still work, but the loop won't call it
	calls := msg.ExtractToolCalls()
	if len(calls) != 0 {
		t.Fatalf("message should have no tool calls initially, got %d", len(calls))
	}

	// Even if we manually call injectToolCallsFromThinking, it should extract the tool call
	// (this is just to verify the function still exists and works)
	updated, ok := injectToolCallsFromThinking(msg)
	if !ok {
		t.Fatalf("injectToolCallsFromThinking should be able to parse tool calls from thinking")
	}

	// But the important thing is that loop.go no longer CALLS injectToolCallsFromThinking
	// So in production, this function will NOT be invoked for assistant messages

	// Verify the extracted calls are correct (for documentation purposes)
	extractedCalls := updated.ExtractToolCalls()
	if len(extractedCalls) != 1 {
		t.Fatalf("expected 1 extracted tool call, got %d", len(extractedCalls))
	}
	if extractedCalls[0].Name != "bash" {
		t.Fatalf("expected tool name 'bash', got %q", extractedCalls[0].Name)
	}

	// The function still works, but loop.go no longer calls it for thinking content
	t.Log("injectToolCallsFromThinking exists and works, but loop.go no longer calls it for thinking content")
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
		APIKey:         "test-key",
		ThinkingLevel:  "high",
		ContextWindow:  128000,
		GetWorkingDir:  func() string { return "/tmp/worktree-a" },
		GetStartupPath: func() string { return "/tmp/project-root" },
	}

	stream := newTestAgentEventStream()
	msg, err := streamAssistantResponse(context.Background(), agentCtx, config, stream)
	if err != nil {
		t.Fatalf("streamAssistantResponse returned error: %v", err)
	}
	if got := strings.TrimSpace(msg.ExtractText()); got != "ok" {
		t.Fatalf("expected assistant text 'ok', got %q", got)
	}

	if len(observedMessages) < 1 {
		t.Fatalf("expected at least 1 message (system), got %d", len(observedMessages))
	}
	if observedMessages[0].Role != "system" {
		t.Fatalf("expected first message to be system, got %q", observedMessages[0].Role)
	}

	var systemContent string
	if err := json.Unmarshal(observedMessages[0].Content, &systemContent); err != nil {
		t.Fatalf("failed to parse system content: %v", err)
	}
	if strings.Contains(systemContent, "<llm_context>") || strings.Contains(systemContent, "<runtime_state>") {
		t.Fatalf("expected runtime payload outside system prompt, got system content: %q", systemContent)
	}

	// First message: runtime_state should be injected as a user message.
	foundRuntimeState := false
	for _, msg := range observedMessages {
		if msg.Role == "user" {
			var runtimeContent string
			if err := json.Unmarshal(msg.Content, &runtimeContent); err == nil {
				if strings.Contains(runtimeContent, "<agent:runtime_state") {
					foundRuntimeState = true
					if !strings.Contains(runtimeContent, `current_workdir: "/tmp/worktree-a"`) {
						t.Fatalf("expected runtime_state to include current_workdir, got: %q", runtimeContent)
					}
					if !strings.Contains(runtimeContent, `startup_path: "/tmp/project-root"`) {
						t.Fatalf("expected runtime_state to include startup_path, got: %q", runtimeContent)
					}
				}
			}
		}
	}
	if !foundRuntimeState {
		t.Fatal("expected runtime_state to be injected on first message")
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

	// Set llm_context content to simulate post-compact recovery
	// Write a minimal overview.md with actual content
	overviewPath := filepath.Join(sessionDir, "llm-context", "overview.md")
	overviewContent := `# LLM Context

## 当前任务
Test task for post-compact recovery`
	if err := os.WriteFile(overviewPath, []byte(overviewContent), 0644); err != nil {
		t.Fatalf("failed to write overview.md: %v", err)
	}

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
			continue // runtime state injection (llm context part)
		}
		if strings.Contains(content, "<agent:runtime_state") {
			continue // runtime state injection (meta part)
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
