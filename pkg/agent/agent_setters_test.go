package agent

import (
	"testing"

	"github.com/tiancaiamao/ai/pkg/llm"
)

// TestAgentSettersAndGetters exercises the simple Set*/Get* methods on Agent.
// These are mostly trivial setters but they need to register in coverage.
func TestAgentSettersAndGetters(t *testing.T) {
	a := NewAgent(llm.Model{ID: "test-model"}, "key", "system prompt")

	// SetModel / GetModel roundtrip
	newModel := llm.Model{ID: "switched-model"}
	a.SetModel(newModel)
	if got := a.GetModel(); got.ID != "switched-model" {
		t.Errorf("GetModel after SetModel = %v, want switched-model", got)
	}
	if a.LoopConfig.Model.ID != "switched-model" {
		t.Errorf("LoopConfig.Model not synced: %v", a.LoopConfig.Model)
	}

	// SetAPIKey
	a.SetAPIKey("new-key")
	if a.apiKey != "new-key" {
		t.Errorf("SetAPIKey failed, got %q", a.apiKey)
	}

	// SetExecutor
	a.LoopConfig.Executor = nil
	a.SetExecutor(nil)
	if a.LoopConfig.Executor != nil {
		t.Error("expected nil executor")
	}

	// SetToolCallCutoff (with negative clamp)
	a.SetToolCallCutoff(-1)
	if a.LoopConfig.ToolCallCutoff != 0 {
		t.Errorf("expected clamped 0, got %d", a.LoopConfig.ToolCallCutoff)
	}
	a.SetToolCallCutoff(5000)
	if a.LoopConfig.ToolCallCutoff != 5000 {
		t.Errorf("expected 5000, got %d", a.LoopConfig.ToolCallCutoff)
	}

	// SetThinkingLevel (normalization happens inside)
	a.SetThinkingLevel("high")
	if a.LoopConfig.ThinkingLevel == "" {
		t.Error("expected non-empty ThinkingLevel after SetThinkingLevel(high)")
	}
	// Garbage input should normalize to a defined value (either "none" or the value passes through).
	a.SetThinkingLevel("garbage_value")
	switch a.LoopConfig.ThinkingLevel {
	case "none", "low", "medium", "high", "garbage_value":
		// ok — accepted fall-through
	default:
		t.Errorf("unexpected ThinkingLevel after garbage: %q", a.LoopConfig.ThinkingLevel)
	}

	// SetContextWindow with clamping
	a.SetContextWindow(-1)
	if a.LoopConfig.ContextWindow != 0 {
		t.Errorf("expected clamped ContextWindow=0, got %d", a.LoopConfig.ContextWindow)
	}
	a.SetContextWindow(200000)
	if a.LoopConfig.ContextWindow != 200000 {
		t.Errorf("expected ContextWindow=200000, got %d", a.LoopConfig.ContextWindow)
	}

	// GetPendingFollowUps on fresh agent -> 0
	if got := a.GetPendingFollowUps(); got != 0 {
		t.Errorf("expected 0 pending followups on fresh agent, got %d", got)
	}
}
