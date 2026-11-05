package agent

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/traceevent"
)

// TestTurnStartEmittedPerTurn verifies that every turn is bracketed by
// turn_start/turn_end.
//
// turn_start used to be pushed once, before the loop, while turn_end was
// pushed inside it. A two-call run therefore traced one turn_start and two
// turn_end, so turns 2..N were invisible and every run looked like it lasted
// a single turn.
func TestTurnStartEmittedPerTurn(t *testing.T) {
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
	ag := NewAgentFromConfigWithContext(model, "test-key", agentctx.NewAgentContext("You are a test assistant."), DefaultLoopConfig())
	defer ag.Shutdown()
	ag.AddTool(&steerSlowTool{started: make(chan struct{}), finished: make(chan struct{}), delay: 10 * time.Millisecond})

	ch := ag.Events()
	if err := ag.Prompt("run the slow tool"); err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}
	events := collectAgentEvents(t, ch, 10*time.Second)
	ag.Wait()

	starts := countEvent(events, EventTurnStart)
	ends := countEvent(events, EventTurnEnd)
	if starts != ends {
		t.Errorf("turn_start = %d, turn_end = %d: every turn must be opened and closed", starts, ends)
	}
	if starts != 2 {
		t.Errorf("turn_start = %d, want 2 (one per LLM call)", starts)
	}
}

// traceRecorder captures trace event names recorded while it is the active
// buffer.
type traceRecorder struct {
	mu     sync.Mutex
	events []traceevent.TraceEvent
}

func (r *traceRecorder) add(e traceevent.TraceEvent) {
	r.mu.Lock()
	r.events = append(r.events, e)
	r.mu.Unlock()
}

func (r *traceRecorder) find(name string) []traceevent.TraceEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []traceevent.TraceEvent
	for _, e := range r.events {
		if e.Name == name {
			out = append(out, e)
		}
	}
	return out
}

func (r *traceRecorder) field(e traceevent.TraceEvent, key string) string {
	for _, f := range e.Fields {
		if f.Key == key {
			return fmt.Sprint(f.Value)
		}
	}
	return ""
}

// TestQueueMarkersTraced verifies the enqueue/inject markers that make queue
// latency measurable in a trace: a message that is queued and later injected
// leaves both timestamps, so the wait is read from the trace instead of
// guessed from the surrounding spans.
func TestQueueMarkersTraced(t *testing.T) {
	const markerEvents = 4
	for _, name := range []string{"steer_enqueued", "steer_injected", "event_enqueued", "event_injected"} {
		traceevent.EnableEvent(name)
	}

	llmCall := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = b
		llmCall++
		w.Header().Set("Content-Type", "text/event-stream")
		if llmCall == 1 {
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
	ag := NewAgentFromConfigWithContext(model, "test-key", agentctx.NewAgentContext("You are a test assistant."), DefaultLoopConfig())
	defer ag.Shutdown()

	// The constructor installs the agent's own trace buffer as the active
	// fallback, which is what the cross-goroutine enqueue calls resolve to.
	rec := &traceRecorder{}
	traceevent.GetTraceBuf(context.Background()).AddSink(rec.add)

	tool := &steerSlowTool{started: make(chan struct{}), finished: make(chan struct{}), delay: 300 * time.Millisecond}
	ag.AddTool(tool)

	if err := ag.Prompt("run the slow tool"); err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}
	<-tool.started

	if err := ag.Steer("human-stopped-by"); err != nil {
		t.Fatalf("Steer failed: %v", err)
	}
	if err := ag.EnqueueEvent("BG-EVENT-X"); err != nil {
		t.Fatalf("EnqueueEvent failed: %v", err)
	}
	ag.Wait()

	for _, name := range []string{"steer_enqueued", "steer_injected", "event_enqueued", "event_injected"} {
		if n := len(rec.find(name)); n != 1 {
			t.Errorf("trace event %q recorded %d times, want 1", name, n)
		}
	}

	// The enqueue marker must precede the inject marker: that ordering is
	// what makes the wait visible.
	enq := rec.find("event_enqueued")
	inj := rec.find("event_injected")
	if len(enq) == 1 && len(inj) == 1 {
		if rec.field(enq[0], "message") != "BG-EVENT-X" {
			t.Errorf("event_enqueued message = %q, want BG-EVENT-X", rec.field(enq[0], "message"))
		}
		if !inj[0].Timestamp.After(enq[0].Timestamp) && !inj[0].Timestamp.Equal(enq[0].Timestamp) {
			t.Errorf("event_injected at %v precedes enqueue at %v", inj[0].Timestamp, enq[0].Timestamp)
		}
		// The inject marker reports the wait directly: the event was queued
		// while the 300ms tool was running, so it waited most of that.
		waited, err := strconv.ParseInt(rec.field(inj[0], "waited_ms"), 10, 64)
		if err != nil {
			t.Fatalf("event_injected waited_ms = %q, want an integer", rec.field(inj[0], "waited_ms"))
		}
		if waited < 100 {
			t.Errorf("waited_ms = %d, want >= 100 for an event queued during a 300ms tool call", waited)
		}
		// This one was delivered by the loop before an LLM call, not by the
		// run tail.
		if got := rec.field(inj[0], "path"); got != "boundary" {
			t.Errorf("event_injected path = %q, want boundary", got)
		}
		if rec.field(inj[0], "turn") == "" {
			t.Error("boundary inject marker carries no turn")
		}
	}
}

// TestRunTailDeliveryTraced covers the second delivery path: a message that
// arrives while the run is producing its final response is never seen by the
// loop's boundary drain, so the run tail delivers it as the next prompt in
// the chain. That path must emit the inject marker too — otherwise the trace
// shows an enqueue marker with no counterpart, which reads exactly like a
// dropped message.
func TestRunTailDeliveryTraced(t *testing.T) {
	for _, name := range []string{"event_enqueued", "event_injected"} {
		traceevent.EnableEvent(name)
	}

	release := make(chan struct{})
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = b
		calls++
		call := calls
		w.Header().Set("Content-Type", "text/event-stream")
		if call == 1 {
			// Keep the run alive: a final text response here would end it
			// before the second call could hold a message in flight.
			fmt.Fprint(w, sseToolCallsResponse(
				[]map[string]any{{"id": "call-slow", "name": "slow", "arguments": map[string]any{}}},
				"", "tool_calls",
			))
			return
		}
		// The second call is the run's final response. Hold it open so the
		// event lands after the loop's last boundary drain.
		<-release
		fmt.Fprint(w, sseTextResponse("done"))
	}))
	defer server.Close()

	model := llm.Model{ID: "test-model", Provider: "test", BaseURL: server.URL, API: "openai-completions"}
	ag := NewAgentFromConfigWithContext(model, "test-key", agentctx.NewAgentContext("You are a test assistant."), DefaultLoopConfig())
	defer ag.Shutdown()

	ag.AddTool(&steerSlowTool{started: make(chan struct{}), finished: make(chan struct{}), delay: 5 * time.Millisecond})

	rec := &traceRecorder{}
	traceevent.GetTraceBuf(context.Background()).AddSink(rec.add)

	if err := ag.Prompt("start"); err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}
	// Wait until the final response is in flight, then queue an event.
	deadline := time.Now().Add(5 * time.Second)
	for calls < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if calls < 2 {
		t.Fatal("second LLM call never started")
	}
	if err := ag.EnqueueEvent("BG-LATE"); err != nil {
		t.Fatalf("EnqueueEvent failed: %v", err)
	}
	close(release)
	ag.Wait()

	enq := rec.find("event_enqueued")
	if len(enq) != 1 {
		t.Fatalf("event_enqueued recorded %d times, want 1", len(enq))
	}
	inj := rec.find("event_injected")
	if len(inj) != 1 {
		t.Fatalf("event_injected recorded %d times, want 1: a message delivered by the run tail is still delivered", len(inj))
	}
	if got := rec.field(inj[0], "path"); got != "run_tail" {
		t.Errorf("event_injected path = %q, want run_tail (the loop's boundary drain cannot see it)", got)
	}
	if got := rec.field(inj[0], "message"); got != "BG-LATE" {
		t.Errorf("event_injected message = %q, want BG-LATE", got)
	}
	if rec.field(inj[0], "waited_ms") == "" {
		t.Error("event_injected carries no waited_ms")
	}
}
