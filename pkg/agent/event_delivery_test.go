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

// TestEventInjectedWhileBusy verifies that a machine event (the completion of
// a detached background command) is delivered at the next model-call boundary
// instead of waiting for the whole run to end.
//
// Before DrainEvent existed the event sat in the queue until runMessage
// finished, so a long tool-calling run could delay a background result by
// minutes (observed: 161s across 22 turns). Here the event is enqueued while a
// tool call is in flight and must appear in the very next LLM request,
// alongside that tool's result.
func TestEventInjectedWhileBusy(t *testing.T) {
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
	ag := NewAgentFromConfigWithContext(model, "test-key", agentCtx, DefaultLoopConfig())
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

	if err := ag.EnqueueEvent("Background operation op-1 completed.\n\nBG-EVENT-X"); err != nil {
		t.Fatalf("EnqueueEvent failed: %v", err)
	}
	ag.Wait()

	bodies := rec.snapshot()
	if len(bodies) < 2 {
		t.Fatalf("LLM calls = %d, want at least 2", len(bodies))
	}
	next := bodies[1]
	if !strings.Contains(next, "BG-EVENT-X") {
		t.Errorf("event should be injected at the boundary right after the tool result, not later in the run")
	}
	if !strings.Contains(next, "slow result") {
		t.Errorf("event injection must not replace the tool result in the same request")
	}
	if n := ag.GetPendingEvent(); n != 0 {
		t.Errorf("pending events after delivery = %d, want 0", n)
	}
}

// TestEnqueueEventQueueDepth verifies the machine-event queue is bounded the
// same way the steer queue is: a full queue rejects the new event instead of
// growing without limit or silently overwriting an undelivered result.
func TestEnqueueEventQueueDepth(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	var startOnce sync.Once
	ag := NewAgent(llm.Model{}, "test-key", "test")
	defer ag.Shutdown()
	ag.runLoopFn = func(
		_ context.Context,
		_ []agentctx.AgentMessage,
		_ *agentctx.AgentContext,
		_ *LoopConfig,
	) *llm.EventStream[AgentEvent, []agentctx.AgentMessage] {
		startOnce.Do(func() { close(started) })
		<-release
		stream := llm.NewEventStream[AgentEvent, []agentctx.AgentMessage](
			func(e AgentEvent) bool { return e.Type == EventAgentEnd },
			func(e AgentEvent) []agentctx.AgentMessage { return nil },
		)
		go func() {
			stream.Push(NewAgentStartEvent())
			stream.Push(NewAgentEndEvent(nil))
			stream.End(nil)
		}()
		return stream
	}

	if err := ag.Prompt("hold the run open"); err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("run never started")
	}

	for i := 0; i < defaultEventQueueDepth; i++ {
		if err := ag.EnqueueEvent(fmt.Sprintf("event-%d", i)); err != nil {
			t.Fatalf("event %d should fit in the queue, got %v", i, err)
		}
	}
	err := ag.EnqueueEvent("one-too-many")
	if err == nil || !strings.Contains(err.Error(), "event queue full") {
		t.Errorf("full queue should reject the event, got %v", err)
	}
	if n := ag.GetPendingEvent(); n != defaultEventQueueDepth {
		t.Errorf("pending events = %d, want %d", n, defaultEventQueueDepth)
	}

	close(release)
	ag.Wait()
}
