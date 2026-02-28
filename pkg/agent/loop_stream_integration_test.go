package agent

import (
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"context"
	"encoding/json"
	"fmt"
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
	wm := agentctx.NewWorkingMemory(sessionDir)
	if _, err := wm.Load(); err != nil {
		t.Fatalf("failed to initialize working memory: %v", err)
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
	agentCtx.WorkingMemory = wm
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

	if len(observedMessages) < 3 {
		t.Fatalf("expected at least 3 messages (system + runtime user + user), got %d", len(observedMessages))
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
	if strings.Contains(systemContent, "<working_memory>") || strings.Contains(systemContent, "<runtime_state>") {
		t.Fatalf("expected runtime payload outside system prompt, got system content: %q", systemContent)
	}

	var runtimeContent string
	if err := json.Unmarshal(observedMessages[1].Content, &runtimeContent); err != nil {
		t.Fatalf("failed to parse runtime content: %v", err)
	}
	if !strings.Contains(runtimeContent, "<working_memory>") {
		t.Fatalf("expected runtime user content to include working_memory, got: %q", runtimeContent)
	}
	if !strings.Contains(runtimeContent, "<runtime_state>") {
		t.Fatalf("expected runtime user content to include runtime_state, got: %q", runtimeContent)
	}
}
