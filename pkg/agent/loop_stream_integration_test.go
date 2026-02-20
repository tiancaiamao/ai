package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
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

	agentCtx := NewAgentContext("sys")
	agentCtx.Messages = append(agentCtx.Messages, NewUserMessage("show me lines"))

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
