package agent

import (
	"context"
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

// llmRecorder captures the request body of every LLM call served by a mock
// OpenAI-compatible SSE endpoint, in call order.
type llmRecorder struct {
	mu     sync.Mutex
	bodies []string
}

func (r *llmRecorder) add(b []byte) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.bodies = append(r.bodies, string(b))
	return len(r.bodies)
}

func (r *llmRecorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.bodies...)
}

func (r *llmRecorder) waitFor(t *testing.T, n int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		r.mu.Lock()
		got := len(r.bodies)
		r.mu.Unlock()
		if got >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %d LLM calls, got %d", n, got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestEventWakesIdleAgent verifies the bash auto-backgrounding contract: an
// event enqueued while the agent is idle starts a run immediately and reaches
// the model. This is how a detached command's output returns to the
// conversation, so it must not sit in the queue waiting for someone to prompt.
func TestEventWakesIdleAgent(t *testing.T) {
	rec := &llmRecorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		rec.add(b)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sseTextResponse("ok"))
	}))
	defer server.Close()

	model := llm.Model{ID: "test-model", Provider: "test", BaseURL: server.URL, API: "openai-completions"}
	agentCtx := agentctx.NewAgentContext("You are a test assistant.")
	ag := NewAgentFromConfigWithContext(model, "test-key", agentCtx, DefaultLoopConfig())
	defer ag.Shutdown()

	if err := ag.EnqueueEvent("Background operation op-1 completed.\n\nDONE-OUTPUT"); err != nil {
		t.Fatalf("EnqueueEvent failed: %v", err)
	}
	rec.waitFor(t, 1, 5*time.Second)
	ag.Wait()

	bodies := rec.snapshot()
	if len(bodies) != 1 {
		t.Fatalf("LLM calls = %d, want 1 for a single idle event", len(bodies))
	}
	if !strings.Contains(bodies[0], "DONE-OUTPUT") {
		t.Errorf("event text never reached the model:\n%s", bodies[0])
	}
	if n := ag.GetPendingEvent(); n != 0 {
		t.Errorf("pending events = %d, want 0", n)
	}
}

// steerSlowTool signals when it starts and when it runs to completion. It never
// closes finished on context cancellation, so a closed channel proves the
// call was not aborted.
type steerSlowTool struct {
	started  chan struct{}
	finished chan struct{}
	delay    time.Duration
}

func (t *steerSlowTool) Name() string        { return "slow" }
func (t *steerSlowTool) Description() string { return "slow tool for steering tests" }
func (t *steerSlowTool) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (t *steerSlowTool) Execute(ctx context.Context, _ map[string]any) ([]agentctx.ContentBlock, error) {
	close(t.started)
	select {
	case <-time.After(t.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	close(t.finished)
	return []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "slow result"}}, nil
}

// TestSteerKeepsInFlightToolAndFollowUpWaits locks in the two semantics the
// run loop promises:
//
//  1. A steer arriving while a tool call is in flight does not cancel it: the
//     tool runs to completion and the steer is injected at the next
//     model-call boundary, so the following LLM request carries both the tool
//     result and the steer.
//  2. A follow-up arriving at the same time does not preempt the current turn;
//     it runs in a later turn.
func TestSteerKeepsInFlightToolAndFollowUpWaits(t *testing.T) {
	rec := &llmRecorder{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		call := rec.add(b)
		w.Header().Set("Content-Type", "text/event-stream")
		if call == 1 {
			fmt.Fprint(w, sseToolCallsResponse(
				[]map[string]any{{"id": "call-slow", "name": "slow", "arguments": map[string]any{}}},
				"", "tool_calls",
			))
			return
		}
		fmt.Fprint(w, sseTextResponse("done"))
	}))
	defer server.Close()

	model := llm.Model{ID: "test-model", Provider: "test", BaseURL: server.URL, API: "openai-completions"}
	agentCtx := agentctx.NewAgentContext("You are a test assistant.")
	cfg := DefaultLoopConfig()
	// Bound the run: a regression that never drains the steer queue keeps the
	// loop alive on HasPendingSteer forever, which must fail instead of hang.
	cfg.MaxTurns = 4
	ag := NewAgentFromConfigWithContext(model, "test-key", agentCtx, cfg)
	defer ag.Shutdown()

	tool := &steerSlowTool{started: make(chan struct{}), finished: make(chan struct{}), delay: 300 * time.Millisecond}
	ag.AddTool(tool)

	if err := ag.Prompt("run the slow tool"); err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}
	select {
	case <-tool.started:
	case <-time.After(5 * time.Second):
		t.Fatal("slow tool never started")
	}

	if err := ag.Steer("STEER-X"); err != nil {
		t.Fatalf("Steer failed: %v", err)
	}
	if err := ag.FollowUp("FOLLOW-Y"); err != nil {
		t.Fatalf("FollowUp failed: %v", err)
	}

	select {
	case <-tool.finished:
	case <-time.After(5 * time.Second):
		t.Fatal("steer cancelled the in-flight tool call")
	}
	ag.Wait()

	bodies := rec.snapshot()
	afterTool := -1
	for i, b := range bodies {
		if strings.Contains(b, "slow result") {
			afterTool = i
			break
		}
	}
	if afterTool == -1 {
		t.Fatalf("no LLM call carried the tool result (%d calls)", len(bodies))
	}
	if !strings.Contains(bodies[afterTool], "STEER-X") {
		t.Errorf("call %d should carry the tool result and the steer injected at that boundary", afterTool+1)
	}
	if strings.Contains(bodies[afterTool], "FOLLOW-Y") {
		t.Errorf("follow-up must not preempt the current turn (call %d carries it)", afterTool+1)
	}
	if last := bodies[len(bodies)-1]; !strings.Contains(last, "FOLLOW-Y") {
		t.Errorf("follow-up never ran in a later turn (%d calls)", len(bodies))
	}
}
