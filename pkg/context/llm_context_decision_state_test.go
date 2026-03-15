package context

import "testing"

func TestDecisionReminderTriggersAfterPendingThreshold(t *testing.T) {
	wm := NewLLMContext(t.TempDir())

	// Set pressure: 35% tokens + stale outputs
	wm.UpdateMeta(70000, 200000, 10) // 35% tokens
	wm.SetStaleToolCount(5)

	wm.SetUpdatedOverview()
	wm.AdvanceDecisionState(false)
	if wm.NeedsDecisionReminder() {
		t.Fatal("did not expect reminder in overview update turn")
	}

	wm.AdvanceDecisionState(false)
	if wm.NeedsDecisionReminder() {
		t.Fatal("did not expect reminder before threshold")
	}

	wm.AdvanceDecisionState(false)
	if !wm.NeedsDecisionReminder() {
		t.Fatal("expected reminder after two pending rounds")
	}
}

func TestDecisionReminderClearsWhenDecisionNotNeeded(t *testing.T) {
	wm := NewLLMContext(t.TempDir())

	// Set pressure: 35% tokens + stale outputs
	wm.UpdateMeta(70000, 200000, 10) // 35% tokens
	wm.SetStaleToolCount(5)

	wm.SetUpdatedOverview()
	wm.AdvanceDecisionState(false)

	wm.AdvanceDecisionState(false)
	if wm.NeedsDecisionReminder() {
		t.Fatal("did not expect reminder before threshold")
	}

	// Remove pressure: low tokens + no stale outputs
	wm.UpdateMeta(10000, 200000, 10) // 5% tokens
	wm.SetStaleToolCount(0)

	wm.AdvanceDecisionState(false)
	if wm.NeedsDecisionReminder() {
		t.Fatal("expected reminder state to reset when decision is not needed")
	}

	// Re-add pressure
	wm.UpdateMeta(70000, 200000, 10) // 35% tokens
	wm.SetStaleToolCount(5)

	wm.AdvanceDecisionState(false)
	if wm.NeedsDecisionReminder() {
		t.Fatal("did not expect reminder without a new overview update")
	}
}

func TestDecisionReminderClearsWhenDecisionMade(t *testing.T) {
	wm := NewLLMContext(t.TempDir())

	// Set pressure: 35% tokens + stale outputs
	wm.UpdateMeta(70000, 200000, 10) // 35% tokens
	wm.SetStaleToolCount(5)

	wm.SetUpdatedOverview()
	wm.AdvanceDecisionState(false)

	wm.AdvanceDecisionState(false)
	wm.AdvanceDecisionState(false)
	if !wm.NeedsDecisionReminder() {
		t.Fatal("expected reminder before decision is made")
	}

	wm.AdvanceDecisionState(true)
	if wm.NeedsDecisionReminder() {
		t.Fatal("expected reminder state to clear after decision is made")
	}
}

func TestDecisionReminderNoPressure(t *testing.T) {
	wm := NewLLMContext(t.TempDir())

	// No pressure: low tokens + no stale outputs
	wm.UpdateMeta(10000, 200000, 10) // 5% tokens
	wm.SetStaleToolCount(0)

	wm.SetUpdatedOverview()
	wm.AdvanceDecisionState(false)
	wm.AdvanceDecisionState(false)
	wm.AdvanceDecisionState(false)

	if wm.NeedsDecisionReminder() {
		t.Fatal("expected no reminder when there's no pressure")
	}
}

func TestDecisionReminderHighTokens(t *testing.T) {
	wm := NewLLMContext(t.TempDir())

	// High pressure: 55% tokens (no stale outputs needed)
	wm.UpdateMeta(110000, 200000, 10) // 55% tokens
	wm.SetStaleToolCount(0)

	wm.SetUpdatedOverview()
	wm.AdvanceDecisionState(false)
	wm.AdvanceDecisionState(false)
	wm.AdvanceDecisionState(false)

	if !wm.NeedsDecisionReminder() {
		t.Fatal("expected reminder when tokens >= 50%")
	}
}

func TestDecisionReminderManyStaleOutputs(t *testing.T) {
	wm := NewLLMContext(t.TempDir())

	// High pressure: 10+ stale outputs (low tokens)
	wm.UpdateMeta(10000, 200000, 10) // 5% tokens
	wm.SetStaleToolCount(15)

	wm.SetUpdatedOverview()
	wm.AdvanceDecisionState(false)
	wm.AdvanceDecisionState(false)
	wm.AdvanceDecisionState(false)

	if !wm.NeedsDecisionReminder() {
		t.Fatal("expected reminder when stale outputs >= 10")
	}
}