package agent

import (
	"context"
	"strings"
	"testing"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
)

// newDeltaTestState builds a loopState suitable for delta compaction tests.
// It uses a non-persisting config so no session I/O is required.
func newDeltaTestState(messages []agentctx.AgentMessage) (*loopState, *agentctx.AgentContext) {
	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.RecentMessages = messages
	stream := llm.NewEventStream[AgentEvent, []agentctx.AgentMessage](
		func(e AgentEvent) bool { return e.Type == EventAgentEnd },
		func(e AgentEvent) []agentctx.AgentMessage { return e.Messages },
	)
	config := &LoopConfig{}
	state := newLoopState(config, agentCtx, stream, nil)
	return state, agentCtx
}

// collectStreamEvents drains the stream's buffered events into a slice.
func collectStreamEvents(stream *llm.EventStream[AgentEvent, []agentctx.AgentMessage]) []AgentEvent {
	var events []AgentEvent
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	ch := stream.Iterator(ctx)
	for {
		select {
		case iter, ok := <-ch:
			if !ok || iter.Done {
				return events
			}
			events = append(events, iter.Value)
		case <-ctx.Done():
			return events
		}
	}
}

// bigText returns a string of n tokens (4*n chars).
func bigText(tokens int) string {
	return strings.Repeat("x", tokens*4)
}

// compressibleMessages builds a delta message set where the first two messages
// (12000 tokens each) are compressed and the last two (5000 tokens each) are
// protected — total delta exceeds the 10K protected budget, so a cut is
// established at index 2.
func compressibleMessages() []agentctx.AgentMessage {
	return []agentctx.AgentMessage{
		textMsg("user", bigText(12000)),
		textMsg("assistant", bigText(12000)),
		textMsg("user", bigText(5000)),
		textMsg("user", bigText(5000)),
	}
}

// --- executeDeltaCompaction -------------------------------------------------

func TestExecuteDeltaCompaction_ReplacesDeltaWithSummary(t *testing.T) {
	msgs := compressibleMessages()
	state, agentCtx := newDeltaTestState(msgs)

	state.executeDeltaCompaction(context.Background(), "compressed summary")

	got := agentCtx.RecentMessages
	// Expect: [delta_summary, protected msg[2], protected msg[3]]
	if len(got) != 3 {
		t.Fatalf("expected 3 messages after compaction, got %d", len(got))
	}
	if messageKind(got[0]) != "delta_summary" {
		t.Errorf("msgs[0] kind = %q, want delta_summary", messageKind(got[0]))
	}
	if extractDeltaText(got[0]) != "compressed summary" {
		t.Errorf("msgs[0] text = %q, want summary", extractDeltaText(got[0]))
	}
	// The protected tail (last two messages) is preserved.
	if extractDeltaText(got[1]) != bigText(5000) {
		t.Errorf("msgs[1] text mismatch — protected tail not preserved")
	}
	if extractDeltaText(got[2]) != bigText(5000) {
		t.Errorf("msgs[2] text mismatch — protected tail not preserved")
	}
}

func TestExecuteDeltaCompaction_ResetsCounters(t *testing.T) {
	msgs := compressibleMessages()
	state, agentCtx := newDeltaTestState(msgs)
	agentCtx.AgentState.TokensSinceLastDeltaCompaction = 50000
	agentCtx.AgentState.ToolCallsSinceLastTrigger = 42

	state.executeDeltaCompaction(context.Background(), "summary")

	if agentCtx.AgentState.TokensSinceLastDeltaCompaction != 0 {
		t.Errorf("TokensSinceLastDeltaCompaction = %d, want 0", agentCtx.AgentState.TokensSinceLastDeltaCompaction)
	}
	if agentCtx.AgentState.ToolCallsSinceLastTrigger != 0 {
		t.Errorf("ToolCallsSinceLastTrigger = %d, want 0", agentCtx.AgentState.ToolCallsSinceLastTrigger)
	}
}

func TestExecuteDeltaCompaction_PersistsViaCallback(t *testing.T) {
	msgs := []agentctx.AgentMessage{
		withEntryID(textMsg("user", bigText(12000)), "entry-1"),
		withEntryID(textMsg("assistant", bigText(12000)), "entry-2"),
		withEntryID(textMsg("user", bigText(5000)), "entry-3"),
		withEntryID(textMsg("user", bigText(5000)), "entry-4"),
	}
	state, _ := newDeltaTestState(msgs)

	var persistedSummary, fromID, toID string
	called := false
	state.config.PersistDeltaCompact = func(summary, fromEntryID, toEntryID string) error {
		called = true
		persistedSummary = summary
		fromID = fromEntryID
		toID = toEntryID
		return nil
	}

	state.executeDeltaCompaction(context.Background(), "my summary")

	if !called {
		t.Fatal("PersistDeltaCompact callback was not invoked")
	}
	if persistedSummary != "my summary" {
		t.Errorf("summary = %q, want %q", persistedSummary, "my summary")
	}
	if fromID != "entry-1" {
		t.Errorf("fromID = %q, want entry-1", fromID)
	}
	// toID is the last compressed message (entry-2); protected region starts
	// at entry-3.
	if toID != "entry-2" {
		t.Errorf("toID = %q, want entry-2", toID)
	}
}

func TestExecuteDeltaCompaction_NothingToCompress(t *testing.T) {
	// Entire delta fits within the protected budget -> nothing to compress.
	msgs := []agentctx.AgentMessage{
		textMsg("user", "small"),
	}
	state, agentCtx := newDeltaTestState(msgs)
	original := len(agentCtx.RecentMessages)

	state.executeDeltaCompaction(context.Background(), "summary")

	// Messages unchanged.
	if len(agentCtx.RecentMessages) != original {
		t.Fatalf("expected %d messages (nothing compressed), got %d", original, len(agentCtx.RecentMessages))
	}
}

// --- checkDeltaCompactionTrigger -------------------------------------------

func TestCheckDeltaTrigger_InjectsDecisionMessage(t *testing.T) {
	// 30K tokens + interval met => TriggerDecision.
	msgs := []agentctx.AgentMessage{
		textMsg("user", bigText(15000)),
		textMsg("assistant", bigText(15000)),
	}
	state, agentCtx := newDeltaTestState(msgs)
	agentCtx.AgentState.ToolCallsSinceLastTrigger = 10
	before := len(agentCtx.RecentMessages)

	state.checkDeltaCompactionTrigger(context.Background())

	// A decision prompt was appended.
	if len(agentCtx.RecentMessages) != before+1 {
		t.Fatalf("expected %d messages, got %d", before+1, len(agentCtx.RecentMessages))
	}
	injected := agentCtx.RecentMessages[len(agentCtx.RecentMessages)-1]
	if messageKind(injected) != "context_compaction_decision" {
		t.Errorf("injected kind = %q, want context_compaction_decision", messageKind(injected))
	}
	body := injected.ExtractText()
	if !strings.Contains(body, "<decision>") {
		t.Errorf("expected decision prompt, got %q", body)
	}
	// Counter reset + pending flag set.
	if agentCtx.AgentState.ToolCallsSinceLastTrigger != 0 {
		t.Errorf("ToolCallsSinceLastTrigger = %d, want 0", agentCtx.AgentState.ToolCallsSinceLastTrigger)
	}
	if !state.deltaPromptPending {
		t.Error("deltaPromptPending should be true after decision injection")
	}
	if state.deltaPromptForced {
		t.Error("deltaPromptForced should be false for decision mode")
	}
}

func TestCheckDeltaTrigger_InjectsForcedMessage(t *testing.T) {
	// 120K tokens => TriggerForced regardless of interval.
	msgs := []agentctx.AgentMessage{
		textMsg("user", bigText(60000)),
		textMsg("assistant", bigText(60000)),
	}
	state, agentCtx := newDeltaTestState(msgs)
	agentCtx.AgentState.ToolCallsSinceLastTrigger = 0 // forced ignores interval
	before := len(agentCtx.RecentMessages)

	state.checkDeltaCompactionTrigger(context.Background())

	if len(agentCtx.RecentMessages) != before+1 {
		t.Fatalf("expected %d messages, got %d", before+1, len(agentCtx.RecentMessages))
	}
	injected := agentCtx.RecentMessages[len(agentCtx.RecentMessages)-1]
	if messageKind(injected) != "context_compaction_decision" {
		t.Errorf("injected kind = %q, want context_compaction_decision", messageKind(injected))
	}
	body := injected.ExtractText()
	if !strings.Contains(body, "<agent:context_compaction>") {
		t.Errorf("expected forced prompt, got %q", body)
	}
	if strings.Contains(body, "<decision>") {
		t.Errorf("forced prompt must not contain <decision>: %q", body)
	}
	if !state.deltaPromptForced {
		t.Error("deltaPromptForced should be true after forced injection")
	}
}

func TestCheckDeltaTrigger_NoTriggerBelowThreshold(t *testing.T) {
	msgs := []agentctx.AgentMessage{
		textMsg("user", bigText(100)),
	}
	state, agentCtx := newDeltaTestState(msgs)
	agentCtx.AgentState.ToolCallsSinceLastTrigger = 100
	before := len(agentCtx.RecentMessages)

	state.checkDeltaCompactionTrigger(context.Background())

	if len(agentCtx.RecentMessages) != before {
		t.Fatalf("expected no injection, got %d messages (was %d)", len(agentCtx.RecentMessages), before)
	}
	if state.deltaPromptPending {
		t.Error("deltaPromptPending should be false when no trigger")
	}
}

// --- processDeltaCompactionResponse ----------------------------------------

func TestProcessDeltaResponse_YesExecutesCompaction(t *testing.T) {
	msgs := compressibleMessages()
	state, agentCtx := newDeltaTestState(msgs)
	state.deltaPromptPending = true
	state.deltaPromptForced = false

	resp := textMsg("assistant", "<decision>yes</decision>\n<summary>task state here</summary>")
	state.processDeltaCompactionResponse(context.Background(), &resp)

	found := false
	for _, m := range agentCtx.RecentMessages {
		if messageKind(m) == "delta_summary" {
			found = true
			if extractDeltaText(m) != "task state here" {
				t.Errorf("summary = %q, want %q", extractDeltaText(m), "task state here")
			}
		}
	}
	if !found {
		t.Fatal("expected delta_summary message after yes decision")
	}
	if agentCtx.AgentState.TokensSinceLastDeltaCompaction != 0 {
		t.Errorf("tokens counter not reset: %d", agentCtx.AgentState.TokensSinceLastDeltaCompaction)
	}
}

func TestProcessDeltaResponse_NoResetsCounter(t *testing.T) {
	msgs := []agentctx.AgentMessage{
		textMsg("user", bigText(3000)),
	}
	state, agentCtx := newDeltaTestState(msgs)
	agentCtx.AgentState.ToolCallsSinceLastTrigger = 15
	state.deltaPromptPending = true
	state.deltaPromptForced = false

	resp := textMsg("assistant", "<decision>no</decision>")
	state.processDeltaCompactionResponse(context.Background(), &resp)

	if agentCtx.AgentState.ToolCallsSinceLastTrigger != 0 {
		t.Errorf("ToolCallsSinceLastTrigger = %d, want 0 after no decision", agentCtx.AgentState.ToolCallsSinceLastTrigger)
	}
	// No delta_summary should exist.
	for _, m := range agentCtx.RecentMessages {
		if messageKind(m) == "delta_summary" {
			t.Fatal("unexpected delta_summary after no decision")
		}
	}
}

func TestProcessDeltaResponse_UnparseableResetsCounter(t *testing.T) {
	msgs := []agentctx.AgentMessage{
		textMsg("user", bigText(3000)),
	}
	state, agentCtx := newDeltaTestState(msgs)
	agentCtx.AgentState.ToolCallsSinceLastTrigger = 12
	state.deltaPromptPending = true
	state.deltaPromptForced = false

	resp := textMsg("assistant", "I'll just use a tool instead")
	state.processDeltaCompactionResponse(context.Background(), &resp)

	if agentCtx.AgentState.ToolCallsSinceLastTrigger != 0 {
		t.Errorf("ToolCallsSinceLastTrigger = %d, want 0 after unparseable", agentCtx.AgentState.ToolCallsSinceLastTrigger)
	}
}

func TestProcessDeltaResponse_ForcedExecutesCompaction(t *testing.T) {
	msgs := compressibleMessages()
	state, agentCtx := newDeltaTestState(msgs)
	state.deltaPromptPending = true
	state.deltaPromptForced = true

	resp := textMsg("assistant", "<summary>forced summary content</summary>")
	state.processDeltaCompactionResponse(context.Background(), &resp)

	found := false
	for _, m := range agentCtx.RecentMessages {
		if messageKind(m) == "delta_summary" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected delta_summary after forced compaction")
	}
}

// --- CompactionInfo source field -------------------------------------------

func TestDeltaCompactionEmitsSourceEvent(t *testing.T) {
	msgs := compressibleMessages()
	state, _ := newDeltaTestState(msgs)

	state.executeDeltaCompaction(context.Background(), "summary")
	events := collectStreamEvents(state.stream)

	var startEvt, endEvt *CompactionInfo
	for _, e := range events {
		if e.Type == EventCompactionStart && e.Compaction != nil {
			startEvt = e.Compaction
		}
		if e.Type == EventCompactionEnd && e.Compaction != nil {
			endEvt = e.Compaction
		}
	}
	if startEvt == nil {
		t.Fatal("missing compaction_start event")
	}
	if startEvt.Source != "delta" {
		t.Errorf("start source = %q, want delta", startEvt.Source)
	}
	if endEvt == nil {
		t.Fatal("missing compaction_end event")
	}
	if endEvt.Source != "delta" {
		t.Errorf("end source = %q, want delta", endEvt.Source)
	}
}

// extractDeltaText returns the text of a single-text-block message.
func extractDeltaText(m agentctx.AgentMessage) string {
	return m.ExtractText()
}

// withEntryID returns a copy of msg with EntryID set.
func withEntryID(msg agentctx.AgentMessage, entryID string) agentctx.AgentMessage {
	msg.EntryID = entryID
	return msg
}
