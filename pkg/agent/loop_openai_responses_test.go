package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
)

// TestStreamAssistantResponse_OpenAIResponsesThinking verifies that reasoning
// deltas from the OpenAI Responses API surface as thinking content in the
// assistant message and as thinking_delta events on the agent stream.
func TestStreamAssistantResponse_OpenAIResponsesThinking(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"type":"response.created","response":{"id":"resp_1"}}`+"\n\n")
		fmt.Fprint(w, `data: {"type":"response.output_item.added","output_index":0,"item":{"type":"reasoning","id":"rs_1"}}`+"\n\n")
		fmt.Fprint(w, `data: {"type":"response.reasoning_summary_text.delta","output_index":0,"delta":"thinking about it"}`+"\n\n")
		fmt.Fprint(w, `data: {"type":"response.reasoning_summary_part.done","output_index":0}`+"\n\n")
		fmt.Fprint(w, `data: {"type":"response.output_item.done","output_index":0,"item":{"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"final summary"}]}}`+"\n\n")
		fmt.Fprint(w, `data: {"type":"response.output_item.added","output_index":1,"item":{"type":"message","id":"msg_1"}}`+"\n\n")
		fmt.Fprint(w, `data: {"type":"response.output_text.delta","output_index":1,"delta":"Hello"}`+"\n\n")
		fmt.Fprint(w, `data: {"type":"response.output_text.delta","output_index":1,"delta":"!"}`+"\n\n")
		fmt.Fprint(w, `data: {"type":"response.output_item.done","output_index":1,"item":{"type":"message","id":"msg_1","content":[{"type":"output_text","text":"Hello!"}]}}`+"\n\n")
		fmt.Fprint(w, `data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}}`+"\n\n")
	}))
	defer server.Close()

	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.RecentMessages = append(agentCtx.RecentMessages, agentctx.NewUserMessage("say hello"))

	config := &LoopConfig{
		Model: llm.Model{
			ID:        "gpt-test",
			Provider:  "opencode",
			BaseURL:   server.URL,
			API:       "openai-responses",
			Reasoning: true,
		},
		APIKey:        "test-key",
		ThinkingLevel: "high",
	}

	stream := newTestAgentEventStream()
	msg, err := streamAssistantResponse(context.Background(), agentCtx, config, stream)
	if err != nil {
		t.Fatalf("streamAssistantResponse returned error: %v", err)
	}

	// Drain events pushed during the call (all queued since producer finished).
	var sawThinkingDelta bool
	it := stream.Iterator(context.Background())
drain:
	for {
		select {
		case r, ok := <-it:
			if !ok || r.Done {
				break drain
			}
			if r.Value.Type == EventMessageUpdate {
				if ev, okEv := r.Value.AssistantMessageEvent.(AssistantMessageEvent); okEv && ev.Type == "thinking_delta" {
					sawThinkingDelta = true
				}
			}
		case <-time.After(200 * time.Millisecond):
			break drain
		}
	}
	if !sawThinkingDelta {
		t.Error("expected thinking_delta events on stream")
	}

	if got := msg.ExtractText(); got != "Hello!" {
		t.Errorf("text = %q, want %q", got, "Hello!")
	}
	if got := msg.ExtractThinking(); got != "final summary" {
		t.Errorf("thinking = %q, want %q", got, "final summary")
	}
	if msg.StopReason != "stop" {
		t.Errorf("stopReason = %q, want stop", msg.StopReason)
	}
}

// TestStreamAssistantResponse_OpenAIResponsesToolCall verifies function_call
// items surface as tool calls in the assistant message.
func TestStreamAssistantResponse_OpenAIResponsesToolCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, `data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"bash","arguments":""}}`+"\n\n")
		fmt.Fprint(w, `data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"command\":"}`+"\n\n")
		fmt.Fprint(w, `data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"\"ls\"}"}`+"\n\n")
		fmt.Fprint(w, `data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"bash","arguments":"{\"command\":\"ls\"}"}}`+"\n\n")
		fmt.Fprint(w, `data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":10,"output_tokens":5,"total_tokens":15}}}`+"\n\n")
	}))
	defer server.Close()

	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.RecentMessages = append(agentCtx.RecentMessages, agentctx.NewUserMessage("list files"))

	config := &LoopConfig{
		Model: llm.Model{
			ID:       "gpt-test",
			Provider: "opencode",
			BaseURL:  server.URL,
			API:      "openai-responses",
		},
		APIKey: "test-key",
	}

	stream := newTestAgentEventStream()
	msg, err := streamAssistantResponse(context.Background(), agentCtx, config, stream)
	if err != nil {
		t.Fatalf("streamAssistantResponse returned error: %v", err)
	}

	calls := msg.ExtractToolCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(calls))
	}
	if calls[0].Name != "bash" {
		t.Errorf("tool name = %q, want bash", calls[0].Name)
	}
	if got, _ := calls[0].Arguments["command"].(string); got != "ls" {
		t.Errorf("command = %v, want ls", calls[0].Arguments["command"])
	}
	if msg.StopReason != "tool_calls" {
		t.Errorf("stopReason = %q, want tool_calls", msg.StopReason)
	}
}
