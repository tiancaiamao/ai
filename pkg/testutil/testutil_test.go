package testutil

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tiancaiamao/ai/pkg/agent"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// ---------------------------------------------------------------------------
// MockTool tests
// ---------------------------------------------------------------------------

func TestMockToolDefaults(t *testing.T) {
	tool := &MockTool{}
	if tool.Name() != "mock_tool" {
		t.Errorf("expected default name 'mock_tool', got %q", tool.Name())
	}
	if tool.Description() != "mock tool for testing" {
		t.Errorf("expected default description")
	}
	if tool.CallCount() != 0 {
		t.Errorf("expected 0 calls initially")
	}
}

func TestMockToolExecute(t *testing.T) {
	tool := &MockTool{}
	blocks, err := tool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(blocks) != 1 {
		t.Fatalf("expected 1 block, got %d", len(blocks))
	}
	text, ok := blocks[0].(agentctx.TextContent)
	if !ok || text.Text != "mock result" {
		t.Errorf("expected 'mock result', got %v", blocks[0])
	}
	if tool.CallCount() != 1 {
		t.Errorf("expected call count 1, got %d", tool.CallCount())
	}
}

func TestEchoTool(t *testing.T) {
	tool := EchoTool("echo")
	if tool.Name() != "echo" {
		t.Errorf("expected name 'echo', got %q", tool.Name())
	}
	blocks, err := tool.Execute(context.Background(), map[string]any{"input": "hello"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := blocks[0].(agentctx.TextContent)
	if text.Text != "hello" {
		t.Errorf("expected 'hello', got %q", text.Text)
	}
}

func TestSlowTool(t *testing.T) {
	tool := SlowTool("slow", 50*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	blocks, err := tool.Execute(ctx, nil)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if elapsed < 40*time.Millisecond {
		t.Errorf("tool returned too fast: %v", elapsed)
	}
	text := blocks[0].(agentctx.TextContent)
	if !strings.Contains(text.Text, "slow result") {
		t.Errorf("expected slow result, got %q", text.Text)
	}
}

func TestSlowToolCancellation(t *testing.T) {
	tool := SlowTool("slow", 5*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()

	_, err := tool.Execute(ctx, nil)
	if err == nil {
		t.Error("expected cancellation error")
	}
}

func TestFailingTool(t *testing.T) {
	errFail := fmt.Errorf("tool failed")
	tool := FailingTool("fail", errFail)
	_, err := tool.Execute(context.Background(), nil)
	if err != errFail {
		t.Errorf("expected errFail, got %v", err)
	}
}

func TestCountingTool(t *testing.T) {
	errFail := fmt.Errorf("exhausted")
	tool := CountingTool("counter", 2, errFail)

	// First 2 calls succeed
	for i := 0; i < 2; i++ {
		_, err := tool.Execute(context.Background(), nil)
		if err != nil {
			t.Fatalf("call %d: unexpected error: %v", i+1, err)
		}
	}
	// Third call fails
	_, err := tool.Execute(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error on 3rd call")
	}
}

func TestCustomExecuteFunc(t *testing.T) {
	tool := &MockTool{
		NameFunc: func() string { return "custom" },
		ExecuteFunc: func(_ context.Context, args map[string]any) ([]agentctx.ContentBlock, error) {
			return []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: fmt.Sprintf("custom: %v", args["x"])},
			}, nil
		},
	}

	blocks, err := tool.Execute(context.Background(), map[string]any{"x": 42})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := blocks[0].(agentctx.TextContent)
	if text.Text != "custom: 42" {
		t.Errorf("expected 'custom: 42', got %q", text.Text)
	}
}

// ---------------------------------------------------------------------------
// EventCollector tests
// ---------------------------------------------------------------------------

func TestEventCollectorBasic(t *testing.T) {
	c := NewEventCollector()

	c.Record(agent.AgentEvent{Type: "agent_start"})
	c.Record(agent.AgentEvent{Type: "turn_start"})
	c.Record(agent.AgentEvent{Type: "turn_end"})
	c.Record(agent.AgentEvent{Type: "agent_end"})

	if c.Len() != 4 {
		t.Errorf("expected 4 events, got %d", c.Len())
	}
	if !c.HasEvent("agent_start") {
		t.Error("expected agent_start")
	}
	if c.HasEvent("compaction_start") {
		t.Error("should not have compaction_start")
	}
	if c.CountEvent("turn_start") != 1 {
		t.Errorf("expected 1 turn_start, got %d", c.CountEvent("turn_start"))
	}
}

func TestEventCollectorEventsOfType(t *testing.T) {
	c := NewEventCollector()
	c.Record(agent.AgentEvent{Type: "text_delta"})
	c.Record(agent.AgentEvent{Type: "text_delta"})
	c.Record(agent.AgentEvent{Type: "agent_end"})

	deltas := c.EventsOfType("text_delta")
	if len(deltas) != 2 {
		t.Errorf("expected 2 text_delta, got %d", len(deltas))
	}

	ends := c.EventsOfType("agent_end")
	if len(ends) != 1 {
		t.Errorf("expected 1 agent_end, got %d", len(ends))
	}
}

func TestEventCollectorReset(t *testing.T) {
	c := NewEventCollector()
	c.Record(agent.AgentEvent{Type: "agent_start"})
	c.Reset()
	if c.Len() != 0 {
		t.Errorf("expected 0 after reset, got %d", c.Len())
	}
}

func TestEventCollectorAllReturnsCopy(t *testing.T) {
	c := NewEventCollector()
	c.Record(agent.AgentEvent{Type: "agent_start"})

	all := c.All()
	all[0] = agent.AgentEvent{Type: "modified"} // mutate copy

	if c.All()[0].Type == "modified" {
		t.Error("All() should return a copy, not a reference")
	}
}

func TestCollectAllFromChannel(t *testing.T) {
	ch := make(chan agent.AgentEvent, 5)
	ch <- agent.AgentEvent{Type: "a"}
	ch <- agent.AgentEvent{Type: "b"}
	close(ch)

	events := CollectAll(t, ch, 5*time.Second)
	if len(events) != 2 {
		t.Errorf("expected 2 events, got %d", len(events))
	}
}

// ---------------------------------------------------------------------------
// SSEBuilder tests
// ---------------------------------------------------------------------------

func TestTextResponse(t *testing.T) {
	resp := TextResponse("hello")
	if !strings.Contains(resp, `"content":"hello"`) {
		t.Errorf("text response should contain content: %s", resp)
	}
	if !strings.Contains(resp, `"finish_reason":"stop"`) {
		t.Errorf("text response should contain finish_reason: %s", resp)
	}
	if !strings.Contains(resp, "data: [DONE]") {
		t.Errorf("text response should end with [DONE]: %s", resp)
	}
}

func TestToolCallResponse(t *testing.T) {
	args := map[string]any{"command": "ls -la"}
	resp := ToolCallResponse("call-1", "bash", args, "tool_calls")

	if !strings.Contains(resp, `"name":"bash"`) {
		t.Errorf("tool call response should contain tool name: %s", resp)
	}
	if !strings.Contains(resp, `"finish_reason":"tool_calls"`) {
		t.Errorf("tool call response should have tool_calls stop: %s", resp)
	}
}

func TestSSEBuilderChaining(t *testing.T) {
	resp := NewSSEBuilder().
		Thinking("let me think").
		Text("the answer is 42").
		Finish("stop", UsageFields{Prompt: 100, Completion: 10})

	if !strings.Contains(resp, "reasoning_content") {
		t.Errorf("should contain thinking: %s", resp)
	}
	if !strings.Contains(resp, `"content":"the answer is 42"`) {
		t.Errorf("should contain text: %s", resp)
	}
	if !strings.Contains(resp, `"prompt_tokens":100`) {
		t.Errorf("should contain usage: %s", resp)
	}
}

// ---------------------------------------------------------------------------
// LLMServer tests
// ---------------------------------------------------------------------------

func TestLLMServerServesResponsesInOrder(t *testing.T) {
	srv := LLMServer(TextResponse("first"), TextResponse("second"))
	defer srv.Close()

	// First request gets "first"
	body1 := fetchSSE(t, srv.URL)
	if !strings.Contains(body1, "first") {
		t.Errorf("first response should contain 'first': %s", body1)
	}

	// Second request gets "second"
	body2 := fetchSSE(t, srv.URL)
	if !strings.Contains(body2, "second") {
		t.Errorf("second response should contain 'second': %s", body2)
	}
}

func TestLLMServerExhausted(t *testing.T) {
	srv := LLMServer(TextResponse("only one"))
	defer srv.Close()

	// First request succeeds
	fetchSSE(t, srv.URL)

	// Second request gets 500
	resp, err := httpGet(srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != 500 {
		t.Errorf("expected 500 when responses exhausted, got %d", resp)
	}
}

func TestLLMServerFactoryDynamic(t *testing.T) {
	var count atomic.Int32
	srv := LLMServerFactory(func(i int, _ *http.Request) string {
		count.Add(1)
		return TextResponse(fmt.Sprintf("call-%d", i))
	})
	defer srv.Close()

	body := fetchSSE(t, srv.URL)
	if !strings.Contains(body, "call-0") {
		t.Errorf("expected call-0: %s", body)
	}

	body2 := fetchSSE(t, srv.URL)
	if !strings.Contains(body2, "call-1") {
		t.Errorf("expected call-1: %s", body2)
	}

	if count.Load() != 2 {
		t.Errorf("expected 2 calls, got %d", count.Load())
	}
}

// ---------------------------------------------------------------------------
// AgentHarness tests (integration)
// ---------------------------------------------------------------------------

func TestAgentHarnessTextResponse(t *testing.T) {
	h := NewAgentHarness(t, []string{TextResponse("hello world")},
		WithMaxTurns(1),
	)
	defer h.Close()

	h.PromptAndWait("say hello", 10*time.Second)

	if !h.Events.HasEvent(agent.EventAgentEnd) {
		t.Error("expected agent_end event")
	}
	if !h.Events.HasEvent(agent.EventTurnEnd) {
		t.Error("expected turn_end event")
	}
}

func TestAgentHarnessWithTools(t *testing.T) {
	echo := EchoTool("echo")
	responses := []string{
		ToolCallResponse("c1", "echo", map[string]any{"input": "ping"}, "tool_calls"),
		TextResponse("done"),
	}
	h := NewAgentHarness(t, responses,
		WithTools(echo),
		WithMaxTurns(3),
	)
	defer h.Close()

	h.PromptAndWait("call echo", 10*time.Second)

	if !h.Events.HasEvent(agent.EventToolExecutionStart) {
		t.Error("expected tool_execution_start")
	}
	if !h.Events.HasEvent(agent.EventToolExecutionEnd) {
		t.Error("expected tool_execution_end")
	}
	if echo.CallCount() != 1 {
		t.Errorf("expected echo to be called once, got %d", echo.CallCount())
	}
}

func TestAgentHarnessMaxTurnsEnforcement(t *testing.T) {
	// LLM always returns tool calls — should stop at MaxTurns
	responses := []string{
		ToolCallResponse("c1", "echo", map[string]any{"input": "a"}, "tool_calls"),
		ToolCallResponse("c2", "echo", map[string]any{"input": "b"}, "tool_calls"),
		ToolCallResponse("c3", "echo", map[string]any{"input": "c"}, "tool_calls"),
	}
	echo := EchoTool("echo")
	h := NewAgentHarness(t, responses,
		WithTools(echo),
		WithMaxTurns(2),
	)
	defer h.Close()

	h.PromptAndWait("call echo repeatedly", 10*time.Second)

	turnEnds := h.Events.CountEvent(agent.EventTurnEnd)
	if turnEnds != 2 {
		t.Errorf("expected 2 turn_end events (MaxTurns=2), got %d", turnEnds)
	}
}

// ---------------------------------------------------------------------------
// test helpers
// ---------------------------------------------------------------------------

func fetchSSE(t *testing.T, url string) string {
	t.Helper()
	resp, err := httpGetFull(url)
	if err != nil {
		t.Fatalf("failed to fetch SSE: %v", err)
	}
	return resp
}
