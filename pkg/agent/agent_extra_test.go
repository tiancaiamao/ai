package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
	traceevent "github.com/tiancaiamao/ai/pkg/traceevent"
)

func TestShouldLogAgentEvent(t *testing.T) {
	// Save and restore global event mask.
	restore := func() {
		traceevent.DisableEvent("log:agent_event_stream")
		traceevent.DisableEvent("message_update")
		traceevent.DisableEvent("text_delta")
		traceevent.DisableEvent("thinking_delta")
		traceevent.DisableEvent("tool_call_delta")
	}
	defer restore()

	// Master switch off: nothing is logged.
	traceevent.DisableEvent("log:agent_event_stream")
	if shouldLogAgentEvent(EventMessageStart) {
		t.Error("master disabled: should be false")
	}

	// Master on, low-frequency events pass through.
	traceevent.EnableEvent("log:agent_event_stream")
	if !shouldLogAgentEvent(EventMessageStart) {
		t.Error("master enabled, low-freq event should be true")
	}

	// High-frequency events need their individual switches.
	highFreq := map[string]string{
		EventMessageUpdate: "message_update",
		EventTextDelta:     "text_delta",
		EventThinkingDelta: "thinking_delta",
		EventToolCallDelta: "tool_call_delta",
	}
	for eventType, switchName := range highFreq {
		traceevent.DisableEvent(switchName)
		if shouldLogAgentEvent(eventType) {
			t.Errorf("%s with switch off should be false", eventType)
		}
		traceevent.EnableEvent(switchName)
		if !shouldLogAgentEvent(eventType) {
			t.Errorf("%s with switch on should be true", eventType)
		}
	}
}

// TestAgentSteerWhileBusy steers while a prompt is streaming; the steer is
// queued and applied at the next model-call boundary — it does not cancel
// the in-flight prompt.
func TestAgentSteerWhileBusy(t *testing.T) {
	orig := streamAssistantResponseFn
	defer func() { streamAssistantResponseFn = orig }()

	release := make(chan struct{})
	started := make(chan struct{})
	streamAssistantResponseFn = func(
		_ context.Context,
		_ *agentctx.AgentContext,
		_ *LoopConfig,
		_ *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
	) (*agentctx.AgentMessage, error) {
		select {
		case started <- struct{}{}:
		default:
		}
		// Block until released; ctx cancellation unblocks via the caller's select.
		<-release
		msg := agentctx.NewAssistantMessage()
		msg.Content = []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "done"}}
		msg.StopReason = "stop"
		return &msg, nil
	}

	ag := NewAgent(llm.Model{}, "test-key", "test")
	if err := ag.Prompt("first"); err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for first prompt to start")
	}

	// Steer while busy — must not block.
	steerDone := make(chan struct{})
	go func() {
		ag.Steer("new direction")
		close(steerDone)
	}()

	select {
	case <-steerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Steer blocked while agent busy")
	}

	close(release)
	ag.Wait()
}

// TestSteerQueueDepth verifies one-at-a-time depth enforcement: while the
// agent is busy, a steer beyond the configured depth is rejected instead of
// silently dropped, and claiming the queue drains it atomically.
func TestSteerQueueDepth(t *testing.T) {
	ag := NewAgent(llm.Model{}, "test-key", "test")
	ag.SetSteerQueueDepth(1)

	// Occupy the run lock to simulate a busy agent without an LLM call.
	ag.mu <- struct{}{}
	defer func() { <-ag.mu }()

	if err := ag.Steer("first"); err != nil {
		t.Fatalf("first steer should queue while busy, got %v", err)
	}
	if err := ag.Steer("second"); err == nil || !strings.Contains(err.Error(), "steer queue full") {
		t.Fatalf("expected queue full error, got %v", err)
	}
	if n := ag.GetPendingSteer(); n != 1 {
		t.Errorf("pending steer = %d, want 1", n)
	}

	if got := ag.drainSteers(); len(got) != 1 || got[0] != "first" {
		t.Errorf("drainSteers = %v, want [first]", got)
	}
	if n := ag.GetPendingSteer(); n != 0 {
		t.Errorf("pending steer after drain = %d, want 0", n)
	}
}

// TestSteerDefaultQueueDepth verifies the documented default depth is enforced
// when no explicit depth is configured (steerDepth == 0), so a busy or stuck
// run cannot accumulate an unbounded number of steering messages.
func TestSteerDefaultQueueDepth(t *testing.T) {
	ag := NewAgent(llm.Model{}, "test-key", "test")

	// Occupy the run lock to simulate a busy agent without an LLM call.
	ag.mu <- struct{}{}
	defer func() { <-ag.mu }()

	for i := 0; i < defaultSteerQueueDepth; i++ {
		if err := ag.Steer("m"); err != nil {
			t.Fatalf("steer %d/%d should queue at default depth, got %v", i+1, defaultSteerQueueDepth, err)
		}
	}
	if err := ag.Steer("overflow"); err == nil || !strings.Contains(err.Error(), "steer queue full") {
		t.Fatalf("expected queue full at default depth %d, got %v", defaultSteerQueueDepth, err)
	}
	if n := ag.GetPendingSteer(); n != defaultSteerQueueDepth {
		t.Errorf("pending steer = %d, want %d", n, defaultSteerQueueDepth)
	}

	// Drain so the re-armed tick cannot start a run once the test releases mu.
	ag.drainSteers()
	ag.disarmSteerTick()
}

// TestSteerTickDrainsOrphan verifies scheduleSteerTick closes the
// enqueue-vs-run-exit race: a steer queued while the agent was busy, whose
// run exited without draining it, is picked up by the one-shot tick and
// processed instead of sitting in the queue until the next Prompt.
func TestSteerTickDrainsOrphan(t *testing.T) {
	orig := streamAssistantResponseFn
	defer func() { streamAssistantResponseFn = orig }()

	processed := make(chan struct{}, 1)
	streamAssistantResponseFn = func(
		_ context.Context,
		_ *agentctx.AgentContext,
		_ *LoopConfig,
		_ *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
	) (*agentctx.AgentMessage, error) {
		select {
		case processed <- struct{}{}:
		default:
		}
		msg := agentctx.NewAssistantMessage()
		msg.Content = []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "ok"}}
		msg.StopReason = "stop"
		return &msg, nil
	}

	ag := NewAgent(llm.Model{}, "test-key", "test")

	// Occupy the run lock so the agent looks busy: Steer queues the message
	// (and arms the drain-retry tick) instead of starting a run.
	ag.mu <- struct{}{}
	if err := ag.Steer("orphaned steer"); err != nil {
		t.Fatalf("Steer should queue while busy, got %v", err)
	}
	if n := ag.GetPendingSteer(); n != 1 {
		t.Fatalf("pending steer = %d, want 1", n)
	}

	// Release the lock without any run draining the queue — the orphaned
	// state the tick must recover from.
	<-ag.mu

	select {
	case <-processed:
	case <-time.After(3 * time.Second):
		t.Fatal("tick did not drain the orphaned steer")
	}
	ag.Wait()

	if n := ag.GetPendingSteer(); n != 0 {
		t.Errorf("pending steer after tick = %d, want 0", n)
	}
}

// TestEventDrainsAfterHumans verifies human-first drain: a machine event
// queued BEFORE a human steer is still processed after it, so event traffic
// cannot jump ahead of a person regardless of arrival order.
func TestEventDrainsAfterHumans(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	// processed records the prompt text of each runLoopFn invocation in
	// order. Only the run goroutine writes it; the test reads it after Wait.
	var processed []string

	ag := NewAgent(llm.Model{}, "test-key", "test")
	defer ag.Shutdown()
	ag.runLoopFn = func(
		_ context.Context,
		prompts []agentctx.AgentMessage,
		_ *agentctx.AgentContext,
		_ *LoopConfig,
	) *llm.EventStream[AgentEvent, []agentctx.AgentMessage] {
		text := ""
		if len(prompts) > 0 {
			text = prompts[0].ExtractText()
		}
		processed = append(processed, text)
		if len(processed) == 1 {
			close(started)
			<-release // hold the run busy while the queues are filled
		}
		stream := llm.NewEventStream[AgentEvent, []agentctx.AgentMessage](
			func(e AgentEvent) bool { return e.Type == EventAgentEnd },
			func(e AgentEvent) []agentctx.AgentMessage { return e.Messages },
		)
		go func() {
			stream.Push(NewAgentStartEvent())
			stream.Push(NewAgentEndEvent([]agentctx.AgentMessage{agentctx.NewUserMessage(text)}))
			stream.End(nil)
		}()
		return stream
	}

	if err := ag.Prompt("first"); err != nil {
		t.Fatalf("Prompt failed: %v", err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for first prompt to start")
	}

	// Queue the machine event FIRST, then the human steer. Priority must put
	// the human first despite the later arrival.
	if err := ag.EnqueueEvent("machine-event"); err != nil {
		t.Fatalf("EnqueueEvent failed: %v", err)
	}
	if err := ag.Steer("human-steer"); err != nil {
		t.Fatalf("Steer failed: %v", err)
	}

	close(release)
	ag.Wait()

	want := []string{"first", "human-steer", "machine-event"}
	if len(processed) != len(want) {
		t.Fatalf("processed order = %v, want %v", processed, want)
	}
	for i := range want {
		if processed[i] != want[i] {
			t.Fatalf("processed[%d] = %q, want %q (full: %v)", i, processed[i], want[i], processed)
		}
	}
	if n := ag.GetPendingEvent(); n != 0 {
		t.Errorf("pending event after drain = %d, want 0", n)
	}
}

// TestAgentSteerEmptyMessage verifies empty steer is a no-op.
func TestAgentSteerEmptyMessage(t *testing.T) {
	ag := NewAgent(llm.Model{}, "test-key", "test")
	ag.Steer("   ")
	ag.Steer("")
	// Agent should still be usable.
	if ag.GetContext() == nil {
		t.Error("context should not be nil")
	}
}

// TestSetCompactorAndClearFollowUps covers the remaining simple setters.
func TestSetCompactorAndClearFollowUps(t *testing.T) {
	ag := NewAgent(llm.Model{}, "test-key", "test")

	// SetCompactor just stores the value.
	ag.SetCompactor(nil)
	if ag.Compactor != nil {
		t.Error("SetCompactor(nil) should leave nil compactor")
	}

	// clearFollowUps is exercised via Abort after a stream was aborted;
	// here we call it directly for coverage of the drain loop.
	go func() {
		// Feed the queue so drain has something to consume.
		_ = ag.FollowUp("stale")
	}()
	// Give the goroutine a moment to enqueue.
	time.Sleep(50 * time.Millisecond)
	ag.clearFollowUps()

	// followUpQueue should be drained: VerifyPending is 0.
	if got := ag.GetPendingFollowUps(); got != 0 {
		t.Errorf("pending follow-ups after clear = %d, want 0", got)
	}
}
