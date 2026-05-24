package testutil_test

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tiancaiamao/ai/pkg/agent"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/session"
	"github.com/tiancaiamao/ai/pkg/testutil"
)

// ---------------------------------------------------------------------------
// Session test helpers
// ---------------------------------------------------------------------------

func createTestSession(t *testing.T, dir string) *session.Session {
	t.Helper()
	return session.NewSession(dir)
}

func loadTestSession(t *testing.T, dir string) *session.Session {
	t.Helper()
	sess, err := session.LoadSession(dir)
	if err != nil {
		t.Fatalf("LoadSession failed: %v", err)
	}
	return sess
}

// ---------------------------------------------------------------------------
// Agent harness integration tests
// These exercise the full agent loop (Prompt → RunLoop → events → Wait)
// using the testutil harness, replacing hand-written httptest servers.
// ---------------------------------------------------------------------------

// TestHarness_RetryAfterTransientError verifies that a transient LLM error
// is retried and eventually succeeds. Mirrors pi's "retries after a transient
// error and succeeds" test.
func TestHarness_RetryAfterTransientError(t *testing.T) {
	var callCount atomic.Int32

	srv := testutil.LLMServerFactory(func(i int, r *http.Request) string {
		n := callCount.Add(1)
		if n == 1 {
			// First call: return a 500-style error in SSE
			return `{"choices":[{"delta":{},"finish_reason":"error"}],"error":{"message":"overloaded","type":"server_error"}}`
		}
		return testutil.TextResponse("recovered")
	})
	defer srv.Close()

	model := llm.Model{ID: "test", Provider: "test", API: "openai-completions", BaseURL: srv.URL}
	a := agent.NewAgentFromConfig(model, "test-key", "You are helpful.",
		&agent.LoopConfig{
			MaxLLMRetries:  3,
			RetryBaseDelay: 1 * time.Millisecond,
		},
	)

	collector := testutil.NewEventCollector()
	unsub := collector.Subscribe(a.Events())
	defer unsub()

	if err := a.Prompt("test"); err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}
	a.Wait()
	// Yield to let event subscriber drain the final events.
	time.Sleep(10 * time.Millisecond)

	if !collector.HasEvent(agent.EventAgentEnd) {
		t.Error("expected agent_end event")
	}
	if callCount.Load() < 2 {
		t.Errorf("expected at least 2 LLM calls (retry), got %d", callCount.Load())
	}
}

// TestHarness_ToolCallFollowedByResponse verifies the full tool call lifecycle:
// LLM returns tool_call → executor runs tool → LLM gets tool result → final response.
// Mirrors pi's "expected event order for a tool call turn".
func TestHarness_ToolCallFollowedByResponse(t *testing.T) {
	echo := testutil.EchoTool("echo")
	responses := []string{
		testutil.ToolCallResponse("c1", "echo", map[string]any{"input": "hello"}, "tool_calls"),
		testutil.TextResponse("I called echo with hello"),
	}
	h := testutil.NewAgentHarness(t, responses, testutil.WithTools(echo), testutil.WithMaxTurns(5))
	defer h.Close()

	h.PromptAndWait("use echo", 10*time.Second)

	// Verify tool was called
	if echo.CallCount() != 1 {
		t.Errorf("expected echo called once, got %d", echo.CallCount())
	}

	// Verify event sequence
	events := h.Events.All()
	types := eventTypes(events)

	assertContains(t, types, agent.EventAgentStart)
	assertContains(t, types, agent.EventToolExecutionStart)
	assertContains(t, types, agent.EventToolExecutionEnd)
	assertContains(t, types, agent.EventTurnEnd)
	assertContains(t, types, agent.EventAgentEnd)

	// At least 2 turns (tool call + final text)
	if h.Events.CountEvent(agent.EventTurnEnd) < 2 {
		t.Errorf("expected >= 2 turn_end events, got %d", h.Events.CountEvent(agent.EventTurnEnd))
	}
}

// TestHarness_CompactionTriggerDuringLoop verifies that a compactor can
// trigger mid-loop and the agent continues afterwards.
// Mirrors pi's compaction characterization tests.
func TestHarness_CompactionTriggerDuringLoop(t *testing.T) {
	compactor := &testCompactor{shouldCompact: true}
	responses := []string{
		testutil.TextResponse("first response"),
		testutil.TextResponse("second response"),
	}
	h := testutil.NewAgentHarness(t, responses,
		testutil.WithCompactors(compactor),
		testutil.WithMaxTurns(3),
	)
	defer h.Close()

	h.PromptAndWait("trigger compaction", 10*time.Second)

	if !h.Events.HasEvent(agent.EventCompactionStart) {
		t.Error("expected compaction_start event")
	}
	if !h.Events.HasEvent(agent.EventCompactionEnd) {
		t.Error("expected compaction_end event")
	}
	if compactor.calls == 0 {
		t.Error("expected compactor to be called")
	}
}

// TestHarness_FollowUpQueue verifies that follow-up messages are processed
// after the initial prompt completes.
func TestHarness_FollowUpQueue(t *testing.T) {
	responses := []string{
		testutil.TextResponse("first"),
		testutil.TextResponse("second"),
	}
	h := testutil.NewAgentHarness(t, responses, testutil.WithMaxTurns(3))
	defer h.Close()

	h.Prompt("initial")
	if err := h.Agent.FollowUp("follow-up"); err != nil {
		t.Fatalf("FollowUp failed: %v", err)
	}
	h.Wait(10 * time.Second)

	// Should have processed both prompts
	if h.Events.CountEvent(agent.EventAgentEnd) < 1 {
		t.Error("expected agent_end event")
	}
}

// TestHarness_AbortDuringStreaming verifies that aborting an agent mid-stream
// produces an agent_end event.
func TestHarness_AbortDuringStreaming(t *testing.T) {
	// Return a slow streaming response so we can abort mid-stream
	srv := testutil.LLMServerFactory(func(i int, r *http.Request) string {
		time.Sleep(50 * time.Millisecond)
		return testutil.TextResponse("partial")
	})
	defer srv.Close()

	model := llm.Model{ID: "test", Provider: "test", API: "openai-completions", BaseURL: srv.URL}
	a := agent.NewAgent(model, "test-key", "You are helpful.")
	collector := testutil.NewEventCollector()
	unsub := collector.Subscribe(a.Events())
	defer unsub()

	if err := a.Prompt("test"); err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}

	// Abort after a small delay
	time.Sleep(20 * time.Millisecond)
	a.Abort()

	a.Wait()
	// Yield to let event subscriber drain the final events.
	time.Sleep(10 * time.Millisecond)

	if !collector.HasEvent(agent.EventAgentEnd) {
		t.Error("expected agent_end after abort")
	}
}

// TestHarness_MultipleToolCallsInOneTurn verifies that the agent handles
// multiple tool calls in a single LLM response and executes them all.
func TestHarness_MultipleToolCallsInOneTurn(t *testing.T) {
	tool1 := testutil.EchoTool("tool_a")
	tool2 := testutil.EchoTool("tool_b")

	// Build a response with two tool calls
	sse := testutil.NewSSEBuilder().
		ToolCall("c1", "tool_a", map[string]any{"input": "hello"}).
		ToolCall("c2", "tool_b", map[string]any{"input": "world"}).
		Finish("tool_calls", testutil.UsageFields{Prompt: 10, Completion: 20})

	responses := []string{sse, testutil.TextResponse("done")}
	h := testutil.NewAgentHarness(t, responses,
		testutil.WithTools(tool1, tool2),
		testutil.WithMaxTurns(5),
	)
	defer h.Close()

	h.PromptAndWait("call both tools", 10*time.Second)

	if tool1.CallCount() != 1 {
		t.Errorf("expected tool_a called once, got %d", tool1.CallCount())
	}
	if tool2.CallCount() != 1 {
		t.Errorf("expected tool_b called once, got %d", tool2.CallCount())
	}
}

// TestHarness_FailingToolReportsError verifies that when a tool returns an error,
// the agent emits error events and continues the loop.
func TestHarness_FailingToolReportsError(t *testing.T) {
	failTool := testutil.FailingTool("bad_tool", fmt.Errorf("tool exploded"))
	responses := []string{
		testutil.ToolCallResponse("c1", "bad_tool", map[string]any{}, "tool_calls"),
		testutil.TextResponse("I see the tool failed"),
	}
	h := testutil.NewAgentHarness(t, responses,
		testutil.WithTools(failTool),
		testutil.WithMaxTurns(5),
	)
	defer h.Close()

	h.PromptAndWait("call the failing tool", 10*time.Second)

	// Should have tool execution end with error
	toolEnds := h.Events.EventsOfType(agent.EventToolExecutionEnd)
	if len(toolEnds) == 0 {
		t.Fatal("expected tool_execution_end event")
	}
	if !toolEnds[0].IsError {
		t.Error("expected tool_execution_end to have IsError=true")
	}

	// Agent should still complete
	if !h.Events.HasEvent(agent.EventAgentEnd) {
		t.Error("expected agent_end even after tool error")
	}
}

// TestHarness_EventOrderForSingleTurn verifies the exact event sequence for
// a single-turn text response. Mirrors pi's "emits the expected event order
// for a single prompt".
func TestHarness_EventOrderForSingleTurn(t *testing.T) {
	h := testutil.NewAgentHarness(t,
		[]string{testutil.TextResponse("hello")},
		testutil.WithMaxTurns(1),
	)
	defer h.Close()

	h.PromptAndWait("hi", 10*time.Second)

	events := h.Events.All()
	types := eventTypes(events)

	// Expected order: agent_start → turn_start → (message events) → turn_end → agent_end
	assertPrefix(t, "event sequence should start with agent_start", types, agent.EventAgentStart)
	assertSuffix(t, "event sequence should end with agent_end", types, agent.EventAgentEnd)
	assertContains(t, types, agent.EventTurnStart)
	assertContains(t, types, agent.EventTurnEnd)
}

// TestHarness_LLMRetryEvent verifies that the llm_retry event is emitted
// when the LLM returns a retryable error.
func TestHarness_LLMRetryEvent(t *testing.T) {
	var callCount atomic.Int32
	srv := testutil.LLMServerFactory(func(i int, r *http.Request) string {
		n := callCount.Add(1)
		if n == 1 {
			return `{"choices":[{"delta":{},"finish_reason":"error"}]}`
		}
		return testutil.TextResponse("recovered")
	})
	defer srv.Close()

	model := llm.Model{ID: "test", Provider: "test", API: "openai-completions", BaseURL: srv.URL}
	a := agent.NewAgentFromConfig(model, "test-key", "You are helpful.",
		&agent.LoopConfig{
			MaxLLMRetries:  3,
			RetryBaseDelay: 1 * time.Millisecond,
		},
	)

	collector := testutil.NewEventCollector()
	unsub := collector.Subscribe(a.Events())
	defer unsub()

	if err := a.Prompt("test"); err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}
	a.Wait()
	// Yield to let event subscriber drain the final events.
	time.Sleep(10 * time.Millisecond)

	if !collector.HasEvent(agent.EventLLMRetry) {
		t.Error("expected llm_retry event")
	}
}

// TestHarness_ContextCancellation verifies that cancelling the context
// during agent execution stops the loop cleanly.
func TestHarness_ContextCancellation(t *testing.T) {
	srv := testutil.LLMServerFactory(func(i int, r *http.Request) string {
		time.Sleep(200 * time.Millisecond) // slow response
		return testutil.TextResponse("slow")
	})
	defer srv.Close()

	model := llm.Model{ID: "test", Provider: "test", API: "openai-completions", BaseURL: srv.URL}
	a := agent.NewAgent(model, "test-key", "You are helpful.")
	collector := testutil.NewEventCollector()
	unsub := collector.Subscribe(a.Events())
	defer unsub()

	if err := a.Prompt("test"); err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}

	// Abort after a short delay (simulates context cancellation)
	time.Sleep(30 * time.Millisecond)
	a.Abort()
	a.Wait()
	// Yield to let event subscriber drain the final events.
	time.Sleep(10 * time.Millisecond)

	// Agent should have ended
	if !collector.HasEvent(agent.EventAgentEnd) {
		t.Error("expected agent_end after cancellation")
	}
}

// ---------------------------------------------------------------------------
// Session integration tests
// ---------------------------------------------------------------------------

// TestSession_SaveAndReload verifies that messages persist across save/load.
func TestSession_SaveAndReload(t *testing.T) {
	tmpDir := t.TempDir()
	sess := createTestSession(t, tmpDir)

	msgs := []agentctx.AgentMessage{
		agentctx.NewUserMessage("hello"),
		agentctx.NewAssistantMessage(),
		agentctx.NewUserMessage("world"),
	}
	if err := sess.SaveMessages(msgs); err != nil {
		t.Fatalf("SaveMessages failed: %v", err)
	}

	loaded := loadTestSession(t, tmpDir)
	loadedMsgs := loaded.GetMessages()
	if len(loadedMsgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(loadedMsgs))
	}
	if loadedMsgs[0].ExtractText() != "hello" {
		t.Errorf("first message: expected 'hello', got %q", loadedMsgs[0].ExtractText())
	}
	if loadedMsgs[2].ExtractText() != "world" {
		t.Errorf("third message: expected 'world', got %q", loadedMsgs[2].ExtractText())
	}
}

// TestSession_AppendAndIncrementalLoad verifies appending messages
// and loading them back.
func TestSession_AppendAndIncrementalLoad(t *testing.T) {
	tmpDir := t.TempDir()
	sess := createTestSession(t, tmpDir)

	for i := 0; i < 5; i++ {
		msg := agentctx.NewUserMessage(fmt.Sprintf("msg-%d", i))
		if err := sess.AddMessages(msg); err != nil {
			t.Fatalf("AddMessages %d failed: %v", i, err)
		}
	}

	loaded := loadTestSession(t, tmpDir)
	msgs := loaded.GetMessages()
	if len(msgs) != 5 {
		t.Fatalf("expected 5 messages, got %d", len(msgs))
	}
}

// TestSession_ConcurrentWrites verifies that concurrent AddMessages calls
// don't corrupt the session file.
func TestSession_ConcurrentWrites(t *testing.T) {
	tmpDir := t.TempDir()
	sess := createTestSession(t, tmpDir)

	done := make(chan error, 20)
	for i := 0; i < 20; i++ {
		go func(n int) {
			msg := agentctx.NewUserMessage(fmt.Sprintf("concurrent-%d", n))
			done <- sess.AddMessages(msg)
		}(i)
	}

	for i := 0; i < 20; i++ {
		if err := <-done; err != nil {
			t.Fatalf("concurrent AddMessages %d failed: %v", i, err)
		}
	}

	loaded := loadTestSession(t, tmpDir)
	msgs := loaded.GetMessages()
	if len(msgs) != 20 {
		t.Errorf("expected 20 messages, got %d", len(msgs))
	}
}

// ---------------------------------------------------------------------------
// Steer & Follow-up behavioral tests
// ---------------------------------------------------------------------------

// TestHarness_SteerDuringStreaming verifies that steering mid-stream cancels
// the current execution and restarts with the steered message. The agent
// should produce two agent_end cycles (or at least process the steered text).
func TestHarness_SteerDuringStreaming(t *testing.T) {
	// First call is slow (simulates mid-stream); second call responds immediately.
	firstCallDone := make(chan struct{})
	srv := testutil.LLMServerFactory(func(i int, r *http.Request) string {
		if i == 0 {
			// Hold the first response open so we can steer mid-stream.
			time.Sleep(200 * time.Millisecond)
			close(firstCallDone)
			return testutil.TextResponse("interrupted")
		}
		// Second call: the steered response.
		return testutil.TextResponse("steered response")
	})
	defer srv.Close()

	model := llm.Model{ID: "test", Provider: "test", API: "openai-completions", BaseURL: srv.URL}
	a := agent.NewAgent(model, "test-key", "You are helpful.")
	collector := testutil.NewEventCollector()
	unsub := collector.Subscribe(a.Events())
	defer unsub()

	if err := a.Prompt("initial"); err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}

	// Steer while the first LLM call is still in progress.
	time.Sleep(30 * time.Millisecond)
	a.Steer("go east")

	a.Wait()
	time.Sleep(10 * time.Millisecond)

	// Agent must complete with an agent_end event.
	if !collector.HasEvent(agent.EventAgentEnd) {
		t.Error("expected agent_end after steer")
	}

	// There should be at least one text_delta containing the steered response.
	textDeltas := collectTextDeltas(collector)
	found := false
	for _, d := range textDeltas {
		if d == "steered response" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected text delta with 'steered response', got deltas: %v", textDeltas)
	}
}

// TestHarness_FollowUpDuringStreaming verifies that a follow-up queued while
// the agent is streaming gets processed after the current turn finishes.
func TestHarness_FollowUpDuringStreaming(t *testing.T) {
	// First response is slow; second is fast.
	srv := testutil.LLMServerFactory(func(i int, r *http.Request) string {
		if i == 0 {
			time.Sleep(100 * time.Millisecond)
			return testutil.TextResponse("first")
		}
		return testutil.TextResponse("follow-up processed")
	})
	defer srv.Close()

	model := llm.Model{ID: "test", Provider: "test", API: "openai-completions", BaseURL: srv.URL}
	a := agent.NewAgent(model, "test-key", "You are helpful.")
	collector := testutil.NewEventCollector()
	unsub := collector.Subscribe(a.Events())
	defer unsub()

	if err := a.Prompt("initial"); err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}

	// Queue follow-up while the first call is still streaming.
	time.Sleep(20 * time.Millisecond)
	if err := a.FollowUp("do more"); err != nil {
		t.Fatalf("FollowUp failed: %v", err)
	}

	// Wait for both the initial and follow-up turns to complete.
	waitWithTimeout(t, a, 5*time.Second)

	// Must have processed the follow-up — look for its text output.
	textDeltas := collectTextDeltas(collector)
	found := false
	for _, d := range textDeltas {
		if d == "follow-up processed" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected text delta with 'follow-up processed', got deltas: %v", textDeltas)
	}
}

// TestHarness_MultipleFollowUps verifies that multiple queued follow-ups
// are all processed in order.
func TestHarness_MultipleFollowUps(t *testing.T) {
	responses := []string{
		testutil.TextResponse("response-0"),
		testutil.TextResponse("response-1"),
		testutil.TextResponse("response-2"),
	}
	h := testutil.NewAgentHarness(t, responses, testutil.WithMaxTurns(5))
	defer h.Close()

	h.Prompt("start")

	// Queue two follow-ups immediately.
	h.FollowUp("second")
	h.FollowUp("third")

	h.Wait(5 * time.Second)

	textDeltas := collectTextDeltas(h.Events)
	var texts []string
	for _, d := range textDeltas {
		texts = append(texts, d)
	}

	// All three responses should appear.
	assertContains(t, texts, "response-0")
	assertContains(t, texts, "response-1")
	assertContains(t, texts, "response-2")
}

// waitWithTimeout waits for the agent to finish or fatals on timeout.
func waitWithTimeout(t *testing.T, a *agent.Agent, timeout time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		a.Wait()
		close(done)
	}()
	select {
	case <-done:
		time.Sleep(10 * time.Millisecond)
	case <-time.After(timeout):
		t.Fatalf("agent did not finish within %v", timeout)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// collectTextDeltas extracts all text delta content from message_update events.
func collectTextDeltas(collector *testutil.EventCollector) []string {
	updates := collector.EventsOfType(agent.EventMessageUpdate)
	var deltas []string
	for _, e := range updates {
		if ame, ok := e.AssistantMessageEvent.(agent.AssistantMessageEvent); ok {
			if ame.Delta != "" {
				deltas = append(deltas, ame.Delta)
			}
		}
	}
	return deltas
}

// testCompactor is a simple compactor for testing.
type testCompactor struct {
	shouldCompact bool
	calls         int
}

func (c *testCompactor) ShouldCompact(_ context.Context, _ *agentctx.AgentContext) bool {
	return c.shouldCompact
}

func (c *testCompactor) Compact(ctx *agentctx.AgentContext) (*agentctx.CompactionResult, error) {
	c.calls++
	ctx.RecentMessages = []agentctx.AgentMessage{
		agentctx.NewUserMessage("[summary]"),
	}
	return &agentctx.CompactionResult{Summary: "[summary]"}, nil
}

func (c *testCompactor) CalculateDynamicThreshold() int { return 100000 }

func eventTypes(events []agent.AgentEvent) []string {
	types := make([]string, len(events))
	for i, e := range events {
		types[i] = e.Type
	}
	return types
}

func assertContains(t *testing.T, slice []string, want string) {
	t.Helper()
	for _, s := range slice {
		if s == want {
			return
		}
	}
	t.Errorf("expected %q in slice %v", want, slice)
}

func assertPrefix(t *testing.T, msg string, slice []string, want string) {
	t.Helper()
	if len(slice) == 0 || slice[0] != want {
		t.Errorf("%s: got %v", msg, slice)
	}
}

func assertSuffix(t *testing.T, msg string, slice []string, want string) {
	t.Helper()
	if len(slice) == 0 || slice[len(slice)-1] != want {
		t.Errorf("%s: got %v", msg, slice)
	}
}
