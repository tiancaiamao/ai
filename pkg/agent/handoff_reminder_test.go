package agent

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// --- estimateContextTokens tests ---

func TestEstimateContextTokens_EmptyMessages(t *testing.T) {
	assert.Equal(t, 0, estimateContextTokens(nil))
	assert.Equal(t, 0, estimateContextTokens([]agentctx.AgentMessage{}))
}

func TestEstimateContextTokens_NoUsage(t *testing.T) {
	msgs := []agentctx.AgentMessage{
		agentctx.NewUserMessage("hello"),
		agentctx.NewAssistantMessage(),
	}
	assert.Equal(t, 0, estimateContextTokens(msgs))
}

func TestEstimateContextTokens_LastAssistantWithUsage(t *testing.T) {
	msg1 := agentctx.NewAssistantMessage()
	msg1.Usage = &agentctx.Usage{TotalTokens: 50000}
	msg1.StopReason = "stop"

	msg2 := agentctx.NewUserMessage("thanks")
	msg3 := agentctx.NewAssistantMessage()
	msg3.Usage = &agentctx.Usage{TotalTokens: 80000}
	msg3.StopReason = "stop"

	msgs := []agentctx.AgentMessage{msg1, msg2, msg3}
	assert.Equal(t, 80000, estimateContextTokens(msgs))
}

func TestEstimateContextTokens_SkipsAbortedOrError(t *testing.T) {
	msg1 := agentctx.NewAssistantMessage()
	msg1.Usage = &agentctx.Usage{TotalTokens: 40000}
	msg1.StopReason = "stop"

	msg2 := agentctx.NewAssistantMessage()
	msg2.Usage = &agentctx.Usage{TotalTokens: 90000}
	msg2.StopReason = "aborted"

	msgs := []agentctx.AgentMessage{msg1, msg2}
	// The aborted message should be skipped; fall back to msg1
	assert.Equal(t, 40000, estimateContextTokens(msgs))
}

func TestEstimateContextTokens_SkipsErrorStopReason(t *testing.T) {
	msg1 := agentctx.NewAssistantMessage()
	msg1.Usage = &agentctx.Usage{TotalTokens: 40000}
	msg1.StopReason = "stop"

	msg2 := agentctx.NewAssistantMessage()
	msg2.Usage = &agentctx.Usage{TotalTokens: 90000}
	msg2.StopReason = "error"

	msgs := []agentctx.AgentMessage{msg1, msg2}
	assert.Equal(t, 40000, estimateContextTokens(msgs))
}

func TestEstimateContextTokens_TotalTokensZeroFallsBack(t *testing.T) {
	msg := agentctx.NewAssistantMessage()
	msg.Usage = &agentctx.Usage{
		InputTokens:  10000,
		OutputTokens: 5000,
	}
	msg.StopReason = "stop"
	// TotalTokens is 0; should fall back to input + output
	assert.Equal(t, 15000, estimateContextTokens([]agentctx.AgentMessage{msg}))
}

func TestEstimateContextTokens_SkipsNonAgentVisible(t *testing.T) {
	msg := agentctx.NewAssistantMessage()
	msg.Usage = &agentctx.Usage{TotalTokens: 70000}
	msg.StopReason = "stop"
	hiddenMsg := msg.WithVisibility(false, false)

	assert.Equal(t, 0, estimateContextTokens([]agentctx.AgentMessage{hiddenMsg}))
}

func TestEstimateContextTokens_AllZeroUsage(t *testing.T) {
	msg := agentctx.NewAssistantMessage()
	msg.Usage = &agentctx.Usage{}
	msg.StopReason = "stop"
	assert.Equal(t, 0, estimateContextTokens([]agentctx.AgentMessage{msg}))
}

// --- handoffThresholds tests ---

func TestHandoffThresholds_DefaultWindow(t *testing.T) {
	soft, hard := handoffThresholds(0)
	assert.Equal(t, 40000, soft)
	assert.Equal(t, 150000, hard)
}

func TestHandoffThresholds_128KWindow(t *testing.T) {
	soft, hard := handoffThresholds(128000)
	assert.Equal(t, 40000, soft)
	assert.Equal(t, 150000, hard)
}

func TestHandoffThresholds_200KWindow(t *testing.T) {
	soft, hard := handoffThresholds(200000)
	assert.Equal(t, 40000, soft)
	assert.Equal(t, 150000, hard)
}

func TestHandoffThresholds_500KWindow(t *testing.T) {
	soft, hard := handoffThresholds(500000)
	assert.Equal(t, 100000, soft)
	assert.Equal(t, 200000, hard)
}

func TestHandoffThresholds_1MWindow(t *testing.T) {
	soft, hard := handoffThresholds(1000000)
	assert.Equal(t, 100000, soft)
	assert.Equal(t, 200000, hard)
}

// --- injectionInterval tests ---

func TestInjectionInterval_ZeroSoftThreshold(t *testing.T) {
	assert.Equal(t, 10, injectionInterval(50000, 0))
}

func TestInjectionInterval_BelowSoft(t *testing.T) {
	// currentTokens=30000, soft=40000 → decay=0 → interval=10
	assert.Equal(t, 10, injectionInterval(30000, 40000))
}

func TestInjectionInterval_AtSoft(t *testing.T) {
	// currentTokens=40000, soft=40000 → decay=1 → interval=9
	assert.Equal(t, 9, injectionInterval(40000, 40000))
}

func TestInjectionInterval_DecreasesWithTokens(t *testing.T) {
	// currentTokens=80000, soft=40000 → decay=2 → interval=8
	assert.Equal(t, 8, injectionInterval(80000, 40000))
	// currentTokens=120000, soft=40000 → decay=3 → interval=7
	assert.Equal(t, 7, injectionInterval(120000, 40000))
	// currentTokens=360000, soft=40000 → decay=9 → interval=1
	assert.Equal(t, 1, injectionInterval(360000, 40000))
}

func TestInjectionInterval_MinimumOne(t *testing.T) {
	// decay >= 10 → interval clamped to 1
	assert.Equal(t, 1, injectionInterval(400000, 40000))
	assert.Equal(t, 1, injectionInterval(1000000, 40000))
}

// --- maybeInjectHandoffReminder tests ---

// newTestLoopState creates a minimal loopState for testing
// maybeInjectHandoffReminder.
func newTestLoopState(config *LoopConfig, agentCtx *agentctx.AgentContext) *loopState {
	return &loopState{
		config:      config,
		agentCtx:    agentCtx,
		newMessages: []agentctx.AgentMessage{},
	}
}

func newHandoffTestAgentCtx(tokens int) *agentctx.AgentContext {
	ctx := &agentctx.AgentContext{
		RecentMessages: []agentctx.AgentMessage{
			func() agentctx.AgentMessage {
				m := agentctx.NewAssistantMessage()
				if tokens > 0 {
					m.Usage = &agentctx.Usage{TotalTokens: tokens}
					m.StopReason = "stop"
				}
				return m
			}(),
		},
		AgentState: agentctx.NewAgentState("test", "/tmp"),
	}
	return ctx
}

func TestMaybeInjectHandoffReminder_NotHandoffMode(t *testing.T) {
	config := &LoopConfig{ContextManagementMode: "legacy"}
	agentCtx := newHandoffTestAgentCtx(100000)
	state := newTestLoopState(config, agentCtx)

	autoExecute := state.maybeInjectHandoffReminder(agentCtx)
	assert.False(t, autoExecute)
	assert.Equal(t, 1, len(agentCtx.RecentMessages), "no injection in legacy mode")
}

func TestMaybeInjectHandoffReminder_BelowSoftThreshold(t *testing.T) {
	config := &LoopConfig{ContextManagementMode: "handoff", ContextWindow: 200000}
	agentCtx := newHandoffTestAgentCtx(30000) // below soft=40000
	state := newTestLoopState(config, agentCtx)
	agentCtx.AgentState.ToolCallsSinceLastTrigger = 100

	autoExecute := state.maybeInjectHandoffReminder(agentCtx)
	assert.False(t, autoExecute)
	assert.Equal(t, 1, len(agentCtx.RecentMessages), "no injection below soft threshold")
	// Hard floor state should be reset
	assert.False(t, state.hardFloorCrossed)
	assert.Equal(t, 0, state.hardFloorTurns)
}

func TestMaybeInjectHandoffReminder_AboveSoftIntervalNotMet(t *testing.T) {
	config := &LoopConfig{ContextManagementMode: "handoff", ContextWindow: 200000}
	agentCtx := newHandoffTestAgentCtx(50000) // above soft=40000
	state := newTestLoopState(config, agentCtx)
	// interval = 10 - (50000/40000) = 10 - 1 = 9
	// ToolCallsSinceLastTrigger = 5, which is < 9
	agentCtx.AgentState.ToolCallsSinceLastTrigger = 5

	autoExecute := state.maybeInjectHandoffReminder(agentCtx)
	assert.False(t, autoExecute)
	assert.Equal(t, 1, len(agentCtx.RecentMessages), "no injection when interval not met")
}

func TestMaybeInjectHandoffReminder_AboveSoftIntervalMet(t *testing.T) {
	config := &LoopConfig{ContextManagementMode: "handoff", ContextWindow: 200000}
	agentCtx := newHandoffTestAgentCtx(50000) // above soft=40000
	state := newTestLoopState(config, agentCtx)
	// interval = 9, ToolCallsSinceLastTrigger = 10 >= 9
	agentCtx.AgentState.ToolCallsSinceLastTrigger = 10

	autoExecute := state.maybeInjectHandoffReminder(agentCtx)
	assert.False(t, autoExecute)
	assert.Equal(t, 2, len(agentCtx.RecentMessages), "should inject one reminder")

	// Verify the injected message content
	injected := agentCtx.RecentMessages[1]
	assert.Equal(t, "user", injected.Role)
	text := injected.ExtractText()
	assert.Contains(t, text, "<context_management>")
	assert.Contains(t, text, "Context usage: 50000 tokens of 200000 window.")
	assert.Contains(t, text, "Soft threshold: 40000. Hard limit: 150000.")
	assert.Contains(t, text, "handoff_complete")

	// Counter should be reset
	assert.Equal(t, 0, agentCtx.AgentState.ToolCallsSinceLastTrigger)
}

func TestMaybeInjectHandoffReminder_AboveHardInjectsUrgent(t *testing.T) {
	config := &LoopConfig{ContextManagementMode: "handoff", ContextWindow: 200000}
	agentCtx := newHandoffTestAgentCtx(160000) // above hard=150000
	state := newTestLoopState(config, agentCtx)
	// Set high enough to trigger soft injection too
	agentCtx.AgentState.ToolCallsSinceLastTrigger = 100

	autoExecute := state.maybeInjectHandoffReminder(agentCtx)
	// First call above hard → hardFloorTurns becomes 1, not > 2 yet
	assert.False(t, autoExecute)

	// Should have injected 2 messages: soft reminder + urgent reminder
	assert.Equal(t, 3, len(agentCtx.RecentMessages), "should inject soft + urgent reminders")

	// Verify the last message is urgent
	urgentMsg := agentCtx.RecentMessages[2]
	text := urgentMsg.ExtractText()
	assert.Contains(t, text, "URGENT")
	assert.Contains(t, text, "Handoff is mandatory")

	assert.True(t, state.hardFloorCrossed)
	assert.Equal(t, 1, state.hardFloorTurns)
}

func TestMaybeInjectHandoffReminder_AutoExecuteAfterThreeCalls(t *testing.T) {
	config := &LoopConfig{ContextManagementMode: "handoff", ContextWindow: 200000}
	agentCtx := newHandoffTestAgentCtx(160000)
	state := newTestLoopState(config, agentCtx)

	// Call 1: hardFloorTurns goes 0 → 1
	agentCtx.AgentState.ToolCallsSinceLastTrigger = 100
	autoExecute := state.maybeInjectHandoffReminder(agentCtx)
	assert.False(t, autoExecute)
	assert.Equal(t, 1, state.hardFloorTurns)

	// Call 2: hardFloorTurns goes 1 → 2
	agentCtx.AgentState.ToolCallsSinceLastTrigger = 100
	autoExecute = state.maybeInjectHandoffReminder(agentCtx)
	assert.False(t, autoExecute)
	assert.Equal(t, 2, state.hardFloorTurns)

	// Call 3: hardFloorTurns goes 2 → 3, which is > 2 → auto-execute
	agentCtx.AgentState.ToolCallsSinceLastTrigger = 100
	autoExecute = state.maybeInjectHandoffReminder(agentCtx)
	assert.True(t, autoExecute)
	assert.Equal(t, 3, state.hardFloorTurns)
}

func TestMaybeInjectHandoffReminder_ResetWhenBelowSoft(t *testing.T) {
	config := &LoopConfig{ContextManagementMode: "handoff", ContextWindow: 200000}
	agentCtx := newHandoffTestAgentCtx(160000)
	state := newTestLoopState(config, agentCtx)

	// First go above hard to set hardFloorCrossed
	agentCtx.AgentState.ToolCallsSinceLastTrigger = 100
	state.maybeInjectHandoffReminder(agentCtx)
	assert.True(t, state.hardFloorCrossed)
	assert.Equal(t, 1, state.hardFloorTurns)

	// Now drop below soft — should reset
	agentCtx2 := newHandoffTestAgentCtx(30000)
	// Keep the same AgentState
	agentCtx2.AgentState = agentCtx.AgentState
	autoExecute := state.maybeInjectHandoffReminder(agentCtx2)
	assert.False(t, autoExecute)
	assert.False(t, state.hardFloorCrossed)
	assert.Equal(t, 0, state.hardFloorTurns)
}

func TestMaybeInjectHandoffReminder_ResetWhenBetweenSoftAndHard(t *testing.T) {
	config := &LoopConfig{ContextManagementMode: "handoff", ContextWindow: 200000}

	// First go above hard
	agentCtx := newHandoffTestAgentCtx(160000)
	state := newTestLoopState(config, agentCtx)
	agentCtx.AgentState.ToolCallsSinceLastTrigger = 100
	state.maybeInjectHandoffReminder(agentCtx)
	assert.True(t, state.hardFloorCrossed)

	// Now drop to between soft and hard (e.g. 80000)
	agentCtx2 := newHandoffTestAgentCtx(80000)
	agentCtx2.AgentState = agentCtx.AgentState
	agentCtx2.AgentState.ToolCallsSinceLastTrigger = 100
	autoExecute := state.maybeInjectHandoffReminder(agentCtx2)
	assert.False(t, autoExecute)
	assert.False(t, state.hardFloorCrossed, "hard floor should reset when below hard")
	assert.Equal(t, 0, state.hardFloorTurns)
}

func TestMaybeInjectHandoffReminder_LargeWindowThresholds(t *testing.T) {
	config := &LoopConfig{ContextManagementMode: "handoff", ContextWindow: 1000000}
	// soft=100000, hard=200000
	agentCtx := newHandoffTestAgentCtx(120000) // above soft=100000
	state := newTestLoopState(config, agentCtx)
	// interval = 10 - (120000/100000) = 10 - 1 = 9
	agentCtx.AgentState.ToolCallsSinceLastTrigger = 10

	autoExecute := state.maybeInjectHandoffReminder(agentCtx)
	assert.False(t, autoExecute)
	assert.Equal(t, 2, len(agentCtx.RecentMessages), "should inject one reminder")

	injected := agentCtx.RecentMessages[1]
	text := injected.ExtractText()
	assert.Contains(t, text, "Soft threshold: 100000. Hard limit: 200000.")
}

func TestMaybeInjectHandoffReminder_DefaultContextWindow(t *testing.T) {
	config := &LoopConfig{ContextManagementMode: "handoff", ContextWindow: 0}
	// Default window = 200000, soft=40000, hard=150000
	agentCtx := newHandoffTestAgentCtx(50000)
	state := newTestLoopState(config, agentCtx)
	agentCtx.AgentState.ToolCallsSinceLastTrigger = 100

	autoExecute := state.maybeInjectHandoffReminder(agentCtx)
	assert.False(t, autoExecute)

	injected := agentCtx.RecentMessages[1]
	text := injected.ExtractText()
	// Should use 200000 as default window
	assert.Contains(t, text, "tokens of 200000 window.")
}

func TestMaybeInjectHandoffReminder_MessageMetadata(t *testing.T) {
	config := &LoopConfig{ContextManagementMode: "handoff", ContextWindow: 200000}
	agentCtx := newHandoffTestAgentCtx(50000)
	state := newTestLoopState(config, agentCtx)
	agentCtx.AgentState.ToolCallsSinceLastTrigger = 100

	state.maybeInjectHandoffReminder(agentCtx)

	injected := agentCtx.RecentMessages[1]
	assert.Equal(t, "user", injected.Role)
	assert.NotNil(t, injected.Metadata)
	assert.Equal(t, "context_management", injected.Metadata.Kind)
	// Should be agent-visible
	assert.True(t, injected.IsAgentVisible())
}

func TestMaybeInjectHandoffReminder_NoUsageReturnsFalse(t *testing.T) {
	config := &LoopConfig{ContextManagementMode: "handoff", ContextWindow: 200000}
	agentCtx := &agentctx.AgentContext{
		RecentMessages: []agentctx.AgentMessage{
			agentctx.NewUserMessage("hello"),
		},
		AgentState: agentctx.NewAgentState("test", "/tmp"),
	}
	state := newTestLoopState(config, agentCtx)
	agentCtx.AgentState.ToolCallsSinceLastTrigger = 100

	// No assistant message with usage → tokens = 0, below soft → no injection
	autoExecute := state.maybeInjectHandoffReminder(agentCtx)
	assert.False(t, autoExecute)
	assert.Equal(t, 1, len(agentCtx.RecentMessages))
}

func TestMaybeInjectHandoffReminder_SoftCounterResetsAfterInjection(t *testing.T) {
	config := &LoopConfig{ContextManagementMode: "handoff", ContextWindow: 200000}
	agentCtx := newHandoffTestAgentCtx(50000)
	state := newTestLoopState(config, agentCtx)
	agentCtx.AgentState.ToolCallsSinceLastTrigger = 10

	state.maybeInjectHandoffReminder(agentCtx)
	assert.Equal(t, 0, agentCtx.AgentState.ToolCallsSinceLastTrigger,
		"ToolCallsSinceLastTrigger should reset after injection")

	// Second call immediately: interval=9, counter=0 → 0 < 9 → no injection
	before := len(agentCtx.RecentMessages)
	agentCtx.AgentState.ToolCallsSinceLastTrigger = 0
	autoExecute := state.maybeInjectHandoffReminder(agentCtx)
	assert.False(t, autoExecute)
	assert.Equal(t, before, len(agentCtx.RecentMessages),
		"no injection when counter just reset")
}

func TestMaybeInjectHandoffReminder_AboveHardEveryCall(t *testing.T) {
	config := &LoopConfig{ContextManagementMode: "handoff", ContextWindow: 200000}
	agentCtx := newHandoffTestAgentCtx(160000)
	state := newTestLoopState(config, agentCtx)
	agentCtx.AgentState.ToolCallsSinceLastTrigger = 100

	// Each call above hard should inject urgent
	state.maybeInjectHandoffReminder(agentCtx)
	msgsAfterCall1 := len(agentCtx.RecentMessages)

	agentCtx.AgentState.ToolCallsSinceLastTrigger = 100
	state.maybeInjectHandoffReminder(agentCtx)
	msgsAfterCall2 := len(agentCtx.RecentMessages)

	// Each call should add at least 1 message (urgent)
	assert.Greater(t, msgsAfterCall2, msgsAfterCall1,
		"each call above hard should inject at least one message")
}

func TestHandoffReminderText_Format(t *testing.T) {
	text := handoffReminderText(123456, 200000, 40000, 150000)
	assert.Contains(t, text, "<context_management>")
	assert.Contains(t, text, "</context_management>")
	assert.Contains(t, text, "123456 tokens of 200000 window")
	assert.Contains(t, text, "Soft threshold: 40000")
	assert.Contains(t, text, "Hard limit: 150000")
	assert.Contains(t, text, "handoff_complete")
}

func TestUrgentHandoffReminderText_Format(t *testing.T) {
	text := urgentHandoffReminderText(160000, 200000, 40000, 150000)
	assert.Contains(t, text, "<context_management>")
	assert.Contains(t, text, "URGENT")
	assert.Contains(t, text, "Handoff is mandatory")
	assert.Contains(t, text, "160000 tokens of 200000 window")
}

func TestHandoffReminderText_NoUrgentInStandard(t *testing.T) {
	text := handoffReminderText(50000, 200000, 40000, 150000)
	assert.False(t, strings.Contains(text, "URGENT"),
		"standard reminder should not contain URGENT")
}
