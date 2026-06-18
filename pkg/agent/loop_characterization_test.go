package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
)

// characterizationTestTool is a simple tool that echoes its arguments.
type characterizationTestTool struct {
	name string
}

func (t *characterizationTestTool) Name() string { return t.name }
func (t *characterizationTestTool) Description() string {
	return t.name + " tool for characterization tests"
}
func (t *characterizationTestTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"input": map[string]any{"type": "string"}}}
}
func (t *characterizationTestTool) Execute(_ context.Context, args map[string]any) ([]agentctx.ContentBlock, error) {
	return []agentctx.ContentBlock{
		agentctx.TextContent{Type: "text", Text: fmt.Sprintf("%s result: %v", t.name, args["input"])},
	}, nil
}

// sseToolCallsResponse builds an SSE response that returns assistant text with optional tool calls.
// Each toolCall entry should have: "id" (string), "name" (string), "arguments" (map[string]any).
func sseToolCallsResponse(toolCalls []map[string]any, text string, stopReason string) string {
	var chunks []string

	// If tool calls present, emit tool_call deltas first
	for i, tc := range toolCalls {
		// arguments must be a JSON-encoded string (double-encoded) because
		// the API sends function.arguments as a string value, not a JSON object.
		argsJSON, _ := json.Marshal(tc["arguments"])
		escapedArgs, _ := json.Marshal(string(argsJSON))
		chunk := fmt.Sprintf(
			`{"choices":[{"delta":{"tool_calls":[{"index":%d,"id":"%s","type":"function","function":{"name":"%s","arguments":%s}}]}}]}`,
			i, tc["id"], tc["name"], string(escapedArgs),
		)
		chunks = append(chunks, chunk)
	}

	// If text present, emit text delta
	if text != "" {
		chunks = append(chunks, fmt.Sprintf(
			`{"choices":[{"delta":{"content":%q}}]}`,
			text,
		))
	}

	// Final chunk with finish_reason
	chunks = append(chunks, fmt.Sprintf(
		`{"choices":[{"delta":{},"finish_reason":"%s"}],"usage":{"prompt_tokens":10,"completion_tokens":20,"total_tokens":30}}`,
		stopReason,
	))

	var sb strings.Builder
	for _, chunk := range chunks {
		sb.WriteString("data: ")
		sb.WriteString(chunk)
		sb.WriteString("\n\n")
	}
	sb.WriteString("data: [DONE]\n\n")
	return sb.String()
}

// sseTextResponse builds a simple SSE response with text content and stop.
func sseTextResponse(text string) string {
	return sseToolCallsResponse(nil, text, "stop")
}

// collectAgentEvents reads all events from the agent event channel until it's closed
// or a timeout fires. Returns collected events.
func collectAgentEvents(t *testing.T, ch <-chan AgentEvent, timeout time.Duration) []AgentEvent {
	t.Helper()
	var events []AgentEvent
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return events
			}
			events = append(events, ev)
			if ev.Type == EventAgentEnd {
				return events
			}
		case <-timer.C:
			t.Fatal("timed out waiting for events")
			return events
		}
	}
}

// countEvent counts events of a specific type.
func countEvent(events []AgentEvent, eventType string) int {
	n := 0
	for _, e := range events {
		if e.Type == eventType {
			n++
		}
	}
	return n
}

// hasEvent checks if an event of a specific type exists.
func hasEvent(events []AgentEvent, eventType string) bool {
	return countEvent(events, eventType) > 0
}

// ---------------------------------------------------------------------------
// Test 1: MaxTurns enforcement via public API
// ---------------------------------------------------------------------------

// TestCharacterization_MaxTurnsEnforcement verifies that the agent stops after
// exactly MaxTurns LLM calls, even when the LLM keeps returning tool_calls.
// This tests through the full public API (Prompt → Events → Wait).
func TestCharacterization_MaxTurnsEnforcement(t *testing.T) {
	llmCallCount := 0
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		llmCallCount++
		count := llmCallCount
		mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")

		// Every LLM call returns a tool call — the agent should stop at MaxTurns anyway
		fmt.Fprint(w, sseToolCallsResponse(
			[]map[string]any{
				{"id": fmt.Sprintf("call-%d", count), "name": "echo", "arguments": map[string]any{"input": "test"}},
			},
			"",
			"tool_calls",
		))
	}))
	defer server.Close()

	agentCtx := agentctx.NewAgentContext("You are a test assistant.")
	cfg := DefaultLoopConfig()
	cfg.MaxTurns = 2
	cfg.MaxConsecutiveToolCalls = 100 // don't trigger loop guard
	cfg.EnableCheckpoint = false

	model := llm.Model{
		ID:       "test-model",
		Provider: "test",
		BaseURL:  server.URL,
		API:      "openai-completions",
	}

	agent := NewAgentFromConfigWithContext(model, "test-key", agentCtx, cfg)
	agent.AddTool(&characterizationTestTool{name: "echo"})

	if err := agent.Prompt("keep calling echo"); err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}

	events := collectAgentEvents(t, agent.Events(), 10*time.Second)
	agent.Wait()

	// Agent should have made exactly 2 LLM calls (MaxTurns=2)
	mu.Lock()
	count := llmCallCount
	mu.Unlock()
	if count != 2 {
		t.Errorf("expected exactly 2 LLM calls with MaxTurns=2, got %d", count)
	}

	// Must have an agent_end event
	if !hasEvent(events, EventAgentEnd) {
		t.Error("expected EventAgentEnd")
	}

	// Turn end events should be exactly MaxTurns
	turnEnds := countEvent(events, EventTurnEnd)
	if turnEnds != 2 {
		t.Errorf("expected 2 turn_end events, got %d", turnEnds)
	}
}

// ---------------------------------------------------------------------------
// Test 2: Tool loop guard via public API
// ---------------------------------------------------------------------------

// TestCharacterization_ToolLoopGuard verifies that consecutive identical tool
// call signatures are blocked after the configured threshold.
func TestCharacterization_ToolLoopGuard(t *testing.T) {
	llmCallCount := 0
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		llmCallCount++
		mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")

		// Always return the same tool call (same name + same arguments)
		fmt.Fprint(w, sseToolCallsResponse(
			[]map[string]any{
				{"id": "call-same", "name": "echo", "arguments": map[string]any{"input": "identical"}},
			},
			"",
			"tool_calls",
		))
	}))
	defer server.Close()

	agentCtx := agentctx.NewAgentContext("You are a test assistant.")
	cfg := DefaultLoopConfig()
	cfg.MaxConsecutiveToolCalls = 3 // trigger after 3 consecutive identical calls
	cfg.MaxTurns = 20               // high enough that MaxTurns doesn't kick in first
	cfg.EnableCheckpoint = false

	model := llm.Model{
		ID:       "test-model",
		Provider: "test",
		BaseURL:  server.URL,
		API:      "openai-completions",
	}

	agent := NewAgentFromConfigWithContext(model, "test-key", agentCtx, cfg)
	agent.AddTool(&characterizationTestTool{name: "echo"})

	if err := agent.Prompt("keep calling echo with the same argument"); err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}

	events := collectAgentEvents(t, agent.Events(), 10*time.Second)
	agent.Wait()

	// Should see loop_guard_triggered event
	if !hasEvent(events, EventLoopGuardTriggered) {
		t.Error("expected EventLoopGuardTriggered event")
	}

	// LLM should be called MaxConsecutiveToolCalls + 1 + defaultLoopGuardMaxFeedback times
	// (3 identical calls allowed, then guard triggers → feedback #1 → LLM retries,
	//  feedback #2 → LLM retries, then hard abort → recovery turn → hard abort again)
	// Total: 3 normal + 2 feedback + 1 hard abort + 1 recovery turn = 7
	mu.Lock()
	count := llmCallCount
	mu.Unlock()
	if count != 7 { // 3 allowed + 2 feedback + 1 hard abort + 1 recovery turn
		t.Errorf("expected 7 LLM calls (3 allowed + 2 feedback + 1 hard abort + 1 recovery turn), got %d", count)
	}

	// Must end with agent_end
	if !hasEvent(events, EventAgentEnd) {
		t.Error("expected EventAgentEnd")
	}
}

// ---------------------------------------------------------------------------
// Test 3: Context cancellation via public API (Abort)
// ---------------------------------------------------------------------------

// TestCharacterization_ContextCancellation verifies that calling Abort() mid-loop
// produces EventAgentEnd with messages accumulated so far.
func TestCharacterization_ContextCancellation(t *testing.T) {
	proceed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// First call: return tool call quickly
		// Second call: block until cancelled or proceed signal
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseToolCallsResponse(
			[]map[string]any{
				{"id": "call-slow", "name": "echo", "arguments": map[string]any{"input": "test"}},
			},
			"",
			"tool_calls",
		))
		<-proceed
	}))
	defer server.Close()

	agentCtx := agentctx.NewAgentContext("You are a test assistant.")
	cfg := DefaultLoopConfig()
	cfg.MaxTurns = 100
	cfg.EnableCheckpoint = false

	model := llm.Model{
		ID:       "test-model",
		Provider: "test",
		BaseURL:  server.URL,
		API:      "openai-completions",
	}

	agent := NewAgentFromConfigWithContext(model, "test-key", agentCtx, cfg)
	agent.AddTool(&characterizationTestTool{name: "echo"})

	if err := agent.Prompt("start tool loop"); err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}

	// Give the agent time to start and make at least one LLM call
	time.Sleep(200 * time.Millisecond)

	// Abort the agent
	agent.Abort()

	// Signal the server to unblock (in case it's still holding)
	close(proceed)

	events := collectAgentEvents(t, agent.Events(), 10*time.Second)
	agent.Wait()

	// Should have EventAgentEnd
	if !hasEvent(events, EventAgentEnd) {
		t.Error("expected EventAgentEnd after Abort()")
	}

	// Should have at least some events before abort (agent_start, turn_start, etc.)
	if len(events) == 0 {
		t.Error("expected at least some events before cancellation")
	}
}

// ---------------------------------------------------------------------------
// Test 4: Compaction trigger via public API
// ---------------------------------------------------------------------------

// characterizationTriggerCompactor triggers compaction on the first call.
type characterizationTriggerCompactor struct {
	calls         int
	shouldCompact bool
}

func (c *characterizationTriggerCompactor) ShouldCompact(_ context.Context, _ *agentctx.AgentContext) bool {
	return c.shouldCompact
}

func (c *characterizationTriggerCompactor) Compact(_ context.Context, ctx *agentctx.AgentContext) (*agentctx.CompactionResult, error) {
	c.calls++
	ctx.RecentMessages = []agentctx.AgentMessage{
		agentctx.NewUserMessage("[compacted summary]"),
		agentctx.NewUserMessage("latest request"),
	}
	return &agentctx.CompactionResult{
		Summary:      "[compacted]",
		Type:         "major",
		TokensBefore: 10000,
		TokensAfter:  1000,
	}, nil
}

func (c *characterizationTriggerCompactor) CalculateDynamicThreshold() int {
	return 100000
}

// TestCharacterization_CompactionTrigger verifies that when ShouldCompact returns
// true, compaction fires automatically and emits the correct events.
func TestCharacterization_CompactionTrigger(t *testing.T) {
	compactor := &characterizationTriggerCompactor{shouldCompact: true}

	llmCallCount := 0
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		llmCallCount++
		mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")

		// First call: return text to end the loop
		fmt.Fprint(w, sseTextResponse("done"))
	}))
	defer server.Close()

	agentCtx := agentctx.NewAgentContext("You are a test assistant.")
	agentCtx.RecentMessages = append(agentCtx.RecentMessages, agentctx.NewUserMessage("hello"))

	cfg := DefaultLoopConfig()
	cfg.Compactors = []Compactor{compactor}
	cfg.EnableCheckpoint = false

	model := llm.Model{
		ID:       "test-model",
		Provider: "test",
		BaseURL:  server.URL,
		API:      "openai-completions",
	}

	agent := NewAgentFromConfigWithContext(model, "test-key", agentCtx, cfg)

	if err := agent.Prompt("trigger compaction"); err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}

	events := collectAgentEvents(t, agent.Events(), 10*time.Second)
	agent.Wait()

	// Compactor should have been called
	if compactor.calls != 1 {
		t.Errorf("expected compactor.Compact to be called once, got %d", compactor.calls)
	}

	// Should see compaction_start and compaction_end events
	if !hasEvent(events, EventCompactionStart) {
		t.Error("expected EventCompactionStart")
	}
	if !hasEvent(events, EventCompactionEnd) {
		t.Error("expected EventCompactionEnd")
	}

	// Should still have agent_end
	if !hasEvent(events, EventAgentEnd) {
		t.Error("expected EventAgentEnd")
	}
}

// ---------------------------------------------------------------------------
// Test 5: MaxTurns=2 with text final response
// ---------------------------------------------------------------------------

// TestCharacterization_MaxTurnsStopsExactlyAtLimit verifies the agent stops
// exactly at the turn limit, even if the LLM switches from tool_calls to text.
func TestCharacterization_MaxTurnsStopsExactlyAtLimit(t *testing.T) {
	llmCallCount := 0
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		llmCallCount++
		count := llmCallCount
		mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")

		if count <= 2 {
			// First 2 calls return tool calls
			fmt.Fprint(w, sseToolCallsResponse(
				[]map[string]any{
					{"id": fmt.Sprintf("call-%d", count), "name": "echo", "arguments": map[string]any{"input": "test"}},
				},
				"",
				"tool_calls",
			))
		} else {
			// Would return text, but MaxTurns should prevent this call
			fmt.Fprint(w, sseTextResponse("final answer"))
		}
	}))
	defer server.Close()

	agentCtx := agentctx.NewAgentContext("You are a test assistant.")
	cfg := DefaultLoopConfig()
	cfg.MaxTurns = 2
	cfg.MaxConsecutiveToolCalls = 100
	cfg.EnableCheckpoint = false

	model := llm.Model{
		ID:       "test-model",
		Provider: "test",
		BaseURL:  server.URL,
		API:      "openai-completions",
	}

	agent := NewAgentFromConfigWithContext(model, "test-key", agentCtx, cfg)
	agent.AddTool(&characterizationTestTool{name: "echo"})

	if err := agent.Prompt("test max turns"); err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}

	events := collectAgentEvents(t, agent.Events(), 10*time.Second)
	agent.Wait()

	mu.Lock()
	count := llmCallCount
	mu.Unlock()

	if count != 2 {
		t.Errorf("expected exactly 2 LLM calls with MaxTurns=2, got %d", count)
	}

	turnEnds := countEvent(events, EventTurnEnd)
	if turnEnds != 2 {
		t.Errorf("expected 2 turn_end events, got %d", turnEnds)
	}

	// Should end cleanly with agent_end
	if !hasEvent(events, EventAgentEnd) {
		t.Error("expected EventAgentEnd")
	}
}

// ---------------------------------------------------------------------------
// Test 6: Compaction does not fire when ShouldCompact returns false
// ---------------------------------------------------------------------------

// TestCharacterization_NoCompactionWhenNotNeeded verifies that compaction events
// are NOT emitted when ShouldCompact returns false.
func TestCharacterization_NoCompactionWhenNotNeeded(t *testing.T) {
	compactor := &characterizationTriggerCompactor{shouldCompact: false}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseTextResponse("done"))
	}))
	defer server.Close()

	agentCtx := agentctx.NewAgentContext("You are a test assistant.")
	cfg := DefaultLoopConfig()
	cfg.Compactors = []Compactor{compactor}
	cfg.EnableCheckpoint = false

	model := llm.Model{
		ID:       "test-model",
		Provider: "test",
		BaseURL:  server.URL,
		API:      "openai-completions",
	}

	agent := NewAgentFromConfigWithContext(model, "test-key", agentCtx, cfg)

	if err := agent.Prompt("test no compaction"); err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}

	events := collectAgentEvents(t, agent.Events(), 10*time.Second)
	agent.Wait()

	// Should NOT see compaction events
	if hasEvent(events, EventCompactionStart) {
		t.Error("did not expect EventCompactionStart when ShouldCompact returns false")
	}
	if hasEvent(events, EventCompactionEnd) {
		t.Error("did not expect EventCompactionEnd when ShouldCompact returns false")
	}
	if compactor.calls != 0 {
		t.Errorf("expected compactor.Compact NOT to be called, got %d calls", compactor.calls)
	}

	// Should still end normally
	if !hasEvent(events, EventAgentEnd) {
		t.Error("expected EventAgentEnd")
	}
}

// ---------------------------------------------------------------------------
// Test 7: Event stream completeness — single text response
// ---------------------------------------------------------------------------

// TestCharacterization_SingleTurnEventSequence verifies the full event sequence
// for a single-turn conversation ending with text (no tool calls).
func TestCharacterization_SingleTurnEventSequence(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request structure
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("failed to read request body: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		var req map[string]any
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("failed to parse request JSON: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		// Verify auth header
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("expected Authorization 'Bearer test-key', got %q", got)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseTextResponse("Hello from the agent!"))
	}))
	defer server.Close()

	agentCtx := agentctx.NewAgentContext("You are a test assistant.")
	cfg := DefaultLoopConfig()
	cfg.EnableCheckpoint = false

	model := llm.Model{
		ID:       "test-model",
		Provider: "test",
		BaseURL:  server.URL,
		API:      "openai-completions",
	}

	agent := NewAgentFromConfigWithContext(model, "test-key", agentCtx, cfg)

	if err := agent.Prompt("say hello"); err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}

	events := collectAgentEvents(t, agent.Events(), 10*time.Second)
	agent.Wait()

	// Verify event sequence
	if !hasEvent(events, EventAgentStart) {
		t.Error("expected EventAgentStart")
	}
	if !hasEvent(events, EventTurnStart) {
		t.Error("expected EventTurnStart")
	}
	if !hasEvent(events, EventTurnEnd) {
		t.Error("expected EventTurnEnd")
	}
	if !hasEvent(events, EventAgentEnd) {
		t.Error("expected EventAgentEnd")
	}

	// Should NOT have tool_execution events
	if hasEvent(events, EventToolExecutionStart) {
		t.Error("did not expect EventToolExecutionStart for text-only response")
	}

	// Turn ends should be exactly 1
	turnEnds := countEvent(events, EventTurnEnd)
	if turnEnds != 1 {
		t.Errorf("expected exactly 1 turn_end, got %d", turnEnds)
	}
}

// ---------------------------------------------------------------------------
// Test 8: Tool execution events via public API
// ---------------------------------------------------------------------------

// TestCharacterization_ToolExecutionEvents verifies that tool call → execute →
// result events are emitted in the correct order through the public API.
func TestCharacterization_ToolExecutionEvents(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")

		// First call: return a tool call
		fmt.Fprint(w, sseToolCallsResponse(
			[]map[string]any{
				{"id": "call-echo-1", "name": "echo", "arguments": map[string]any{"input": "hello"}},
			},
			"",
			"tool_calls",
		))
	}))
	defer server.Close()

	agentCtx := agentctx.NewAgentContext("You are a test assistant.")
	cfg := DefaultLoopConfig()
	cfg.MaxTurns = 1
	cfg.EnableCheckpoint = false

	model := llm.Model{
		ID:       "test-model",
		Provider: "test",
		BaseURL:  server.URL,
		API:      "openai-completions",
	}

	agent := NewAgentFromConfigWithContext(model, "test-key", agentCtx, cfg)
	agent.AddTool(&characterizationTestTool{name: "echo"})

	if err := agent.Prompt("call echo tool"); err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}

	events := collectAgentEvents(t, agent.Events(), 10*time.Second)
	agent.Wait()

	// Should see tool execution events
	if !hasEvent(events, EventToolExecutionStart) {
		t.Error("expected EventToolExecutionStart")
	}
	if !hasEvent(events, EventToolExecutionEnd) {
		t.Error("expected EventToolExecutionEnd")
	}

	// Should end normally
	if !hasEvent(events, EventAgentEnd) {
		t.Error("expected EventAgentEnd")
	}
}
