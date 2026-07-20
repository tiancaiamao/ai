package compact

import (
	"context"
	"strings"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
)

// TestSteer_NoStaleCache verifies that without llmDecideAnswer/llmDecideAnswerCount
// (removed in the fix), there is no stale cached decision to carry over across steer.
// The compactor re-evaluates based on the current counter + interval.
//
// This was Bug 1: the old llmDecideAnswer cache could return a stale decision
// from the old loop if ToolCallsSinceLastTrigger happened to match after steer.
func TestSteer_NoStaleCache(t *testing.T) {
	cfg := &Config{
		AutoCompact: true,
		LLMDecide: &LLMDecideConfig{
			SoftThreshold:  100,
			HardLimit:      50000,
			TierMedium:     200,
			TierHigh:       300,
			IntervalLow:    5,
			IntervalMedium: 5,
			IntervalHigh:   7,
		},
	}
	c := NewCompactor(cfg, llm.Model{}, "key", "sys", 1_000_000, "")

	askCount := 0
	c.askFunc = func(_ context.Context, _ *agentctx.AgentContext, _ int) (bool, error) {
		askCount++
		return true, nil // LLM says yes
	}

	longText := strings.Repeat("a", 800)
	agentCtx := &agentctx.AgentContext{
		RecentMessages: []agentctx.AgentMessage{
			agentctx.NewUserMessage(longText),
		},
		AgentState: &agentctx.AgentState{
			ToolCallsSinceLastTrigger: 5,
		},
	}

	// First call: interval reached (5 >= 5), LLM says yes.
	if !c.ShouldCompact(context.Background(), agentCtx) {
		t.Error("expected compaction on first call")
	}
	if askCount != 1 {
		t.Fatalf("askLLM should be called once, got %d", askCount)
	}

	// Second call at same counter: interval not elapsed since last ask,
	// returns false — but that's fine, performCompaction only calls once.
	// Crucially: there is no llmDecideAnswer cache to return stale "yes".
	if c.ShouldCompact(context.Background(), agentCtx) {
		t.Error("second call same counter should return false (interval not elapsed)")
	}
	if askCount != 1 {
		t.Errorf("askLLM should NOT be called again, got %d calls", askCount)
	}
}

// TestSteer_CounterReset verifies that after steering resets
// ToolCallsSinceLastTrigger to 0, the compactor does not trigger
// compaction prematurely on the new loop's first turn.
//
// This was Bug 2: ToolCallsSinceLastTrigger carried over after steer,
// causing premature compaction of accumulated pre-steer messages.
func TestSteer_CounterReset(t *testing.T) {
	cfg := &Config{
		AutoCompact: true,
		LLMDecide: &LLMDecideConfig{
			SoftThreshold:  100,
			HardLimit:      50000,
			TierMedium:     200,
			TierHigh:       300,
			IntervalLow:    5,
			IntervalMedium: 5,
			IntervalHigh:   7,
		},
	}
	c := NewCompactor(cfg, llm.Model{}, "key", "sys", 1_000_000, "")

	askCount := 0
	c.askFunc = func(_ context.Context, _ *agentctx.AgentContext, _ int) (bool, error) {
		askCount++
		return true, nil
	}

	longText := strings.Repeat("a", 800)
	agentCtx := &agentctx.AgentContext{
		RecentMessages: []agentctx.AgentMessage{
			agentctx.NewUserMessage(longText),
		},
		AgentState: &agentctx.AgentState{
			ToolCallsSinceLastTrigger: 8, // 8 tool calls in old loop, asked LLM at counter=8
		},
	}

	// Before steer: counter=8 >= interval=5, ShouldCompact asks LLM and returns true.
	// This sets llmDecideLastAskCount = 8.
	if !c.ShouldCompact(context.Background(), agentCtx) {
		t.Error("expected compact with counter=8")
	}

	// --- Simulate steer: agent.Steer() resets ToolCallsSinceLastTrigger to 0 ---
	agentCtx.AgentState.ToolCallsSinceLastTrigger = 0

	// After steer: counter=0, below soft threshold → no compaction.
	// This is the key fix for Bug 2 — without the reset, counter=8 would
	// trigger compaction immediately on the new loop's first turn.
	if c.ShouldCompact(context.Background(), agentCtx) {
		t.Error("after steer reset, should NOT compact: counter=0 is below soft threshold")
	}

	// llmDecideLastAskCount is still 8 from the old loop, so the next re-ask
	// will be delayed until counter >= 8 + 5 = 13. This is not a correctness
	// issue — compaction will happen eventually once enough tool calls accumulate.
	// The critical fix is that we don't compact PREMATURELY after steer.

	// At counter=5: 5 - 8 = -3 < 5, interval not elapsed.
	agentCtx.AgentState.ToolCallsSinceLastTrigger = 5
	if c.ShouldCompact(context.Background(), agentCtx) {
		t.Error("should NOT compact yet: interval not elapsed relative to old last-ask")
	}

	// At counter=13: 13 - 8 = 5 >= 5, interval reached, re-asks LLM.
	agentCtx.AgentState.ToolCallsSinceLastTrigger = 13
	if !c.ShouldCompact(context.Background(), agentCtx) {
		t.Error("should compact: counter=13 >= 8+interval")
	}
	if askCount != 2 {
		t.Errorf("askLLM should be called a second time, got %d calls", askCount)
	}
}
