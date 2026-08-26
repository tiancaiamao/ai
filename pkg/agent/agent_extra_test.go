package agent

import (
	"context"
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

// TestAgentSteerWhileBusy steers while a prompt is streaming; the steer
// cancels the old loop and starts a new prompt.
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
