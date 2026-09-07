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
	"time"

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
	agentCtx.RecentMessages = append(agentCtx.RecentMessages, agentctx.NewUserMessage("show me lines"))

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

// TestStreamAssistantResponse_NoRuntimeStateInjection verifies that the LLM
// call path does not inject runtime_state: the snapshot is frozen as a
// persisted message at the turn boundary in RunLoop (see
// runtimeStateTurnMessage). Ephemeral re-injection before the last user
// message would shift mid-history positions between requests and break
// provider prefix caching.
func TestStreamAssistantResponse_NoRuntimeStateInjection(t *testing.T) {
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
	agentCtx.RecentMessages = append(agentCtx.RecentMessages, agentctx.NewUserMessage("hello"))

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
		Role:           "reviewer",
	}

	stream := newTestAgentEventStream()
	msg, err := streamAssistantResponse(context.Background(), agentCtx, config, stream)
	if err != nil {
		t.Fatalf("streamAssistantResponse returned error: %v", err)
	}
	if got := strings.TrimSpace(msg.ExtractText()); got != "ok" {
		t.Fatalf("expected assistant text 'ok', got %q", got)
	}

	for _, m := range observedMessages {
		if m.Role != "user" {
			continue
		}
		var content string
		if err := json.Unmarshal(m.Content, &content); err != nil {
			continue
		}
		if strings.Contains(content, "<agent:runtime_state") {
			t.Fatalf("runtime_state must not be injected per LLM call (breaks prefix caching), got: %q", content)
		}
	}
}

func TestStreamAssistantResponse_KeepsSeveralRecentRealUserTurns(t *testing.T) {
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

	agentCtx.RecentMessages = append(agentCtx.RecentMessages,
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

func TestStreamAssistantResponse_LengthStopReasonProducesTruncationGuidance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("unexpected authorization header: %q", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"write\",\"arguments\":\"{\\\"content\\\":\\\"hello\\\"}\"}}]}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"length\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":6,\"total_tokens\":16}}\n\n")
	}))
	defer server.Close()

	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.RecentMessages = append(agentCtx.RecentMessages, agentctx.NewUserMessage("write a file"))

	config := &LoopConfig{
		Model: llm.Model{
			ID:       "test-model",
			Provider: "test",
			BaseURL:  server.URL,
			API:      "openai-completions",
		},
		APIKey: "test-key",
	}

	stream := newTestAgentEventStream()
	assistant, err := streamAssistantResponse(context.Background(), agentCtx, config, stream)
	if err != nil {
		t.Fatalf("streamAssistantResponse returned error: %v", err)
	}
	if assistant.StopReason != "length" {
		t.Fatalf("expected stopReason=length, got %q", assistant.StopReason)
	}

	calls := assistant.ExtractToolCalls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(calls))
	}
	if _, ok := calls[0].Arguments["content"]; !ok {
		t.Fatalf("expected truncated args to preserve content field, got %+v", calls[0].Arguments)
	}

	results := executeToolCalls(
		context.Background(),
		agentCtx,
		[]agentctx.Tool{&delayTool{name: "write", delay: 10 * time.Millisecond}},
		nil,
		assistant,
		newLoopTestEventStream(),
		nil,
		DefaultToolOutputLimits(),
	)
	if len(results) != 1 {
		t.Fatalf("expected 1 tool result, got %d", len(results))
	}
	if !results[0].IsError {
		t.Fatal("expected tool result to be error")
	}

	toolMsg := results[0].ExtractText()
	if !strings.Contains(toolMsg, "max_tokens") {
		t.Fatalf("expected tool error to mention max_tokens, got: %s", toolMsg)
	}
	if !strings.Contains(toolMsg, "Please resend the SAME tool call with COMPLETE arguments") {
		t.Fatalf("expected clear retry guidance for LLM, got: %s", toolMsg)
	}
}

func TestStreamAssistantResponse_ContextCanceledMidStreamSalvagesPartial(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"现在是 \"}}]}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"16:11\"}}]}\n\n")
		flusher.Flush()
		// Stay open until the client context is canceled, then exit.
		<-r.Context().Done()
	}))
	defer server.Close()

	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.RecentMessages = append(agentCtx.RecentMessages, agentctx.NewUserMessage("what time is it"))

	config := &LoopConfig{
		Model: llm.Model{
			ID:       "test-model",
			Provider: "test",
			BaseURL:  server.URL,
			API:      "openai-completions",
		},
		APIKey: "test-key",
	}

	ctx, cancel := context.WithCancel(context.Background())
	stream := newTestAgentEventStream()

	done := make(chan *agentctx.AgentMessage, 1)
	errCh := make(chan error, 1)
	go func() {
		msg, err := streamAssistantResponse(ctx, agentCtx, config, stream)
		errCh <- err
		done <- msg
	}()

	// Give the stream time to receive both deltas, then cancel mid-stream.
	time.Sleep(150 * time.Millisecond)
	cancel()

	var msg *agentctx.AgentMessage
	select {
	case msg = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("streamAssistantResponse did not return after context cancel")
	}
	streamErr := <-errCh
	if streamErr != nil {
		t.Fatalf("expected salvaged partial message, got error: %v", streamErr)
	}
	if msg == nil {
		t.Fatal("expected non-nil salvaged message")
	}
	if got := msg.ExtractText(); got != "现在是 16:11" {
		t.Fatalf("expected partial text '现在是 16:11', got %q", got)
	}
	if msg.StopReason != "aborted" {
		t.Fatalf("expected StopReason=aborted, got %q", msg.StopReason)
	}

	// message_end event must have been pushed so the rpc layer persists it.
	endSeen := false
	it := stream.Iterator(context.Background())
drain:
	for {
		select {
		case res, ok := <-it:
			if !ok || res.Done {
				break drain
			}
			if res.Value.Type == EventMessageEnd && res.Value.Message != nil && res.Value.Message.StopReason == "aborted" {
				endSeen = true
			}
		case <-time.After(200 * time.Millisecond):
			break drain
		}
	}
	if !endSeen {
		t.Fatal("expected message_end event for salvaged aborted message")
	}
}

func TestStreamAssistantResponse_TruncationWithoutCancelNotAborted(t *testing.T) {
	// Clean EOF without a finish_reason is synthesized as StopReason "stop"
	// by the LLM client. Without a context cancel this must NOT be treated
	// as an abort.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
	}))
	defer server.Close()

	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.RecentMessages = append(agentCtx.RecentMessages, agentctx.NewUserMessage("hi"))

	config := &LoopConfig{
		Model: llm.Model{
			ID:       "test-model",
			Provider: "test",
			BaseURL:  server.URL,
			API:      "openai-completions",
		},
		APIKey: "test-key",
	}

	stream := newTestAgentEventStream()
	msg, err := streamAssistantResponse(context.Background(), agentCtx, config, stream)
	if err != nil {
		t.Fatalf("streamAssistantResponse returned error: %v", err)
	}
	if msg == nil {
		t.Fatal("expected non-nil message")
	}
	if msg.StopReason == "aborted" {
		t.Fatal("expected non-aborted stop reason for clean EOF without cancel, got aborted")
	}
	if got := msg.ExtractText(); got != "partial" {
		t.Fatalf("expected text 'partial', got %q", got)
	}
}
