package context

import "testing"

func TestDecisionReminderTriggersAfterPendingThreshold(t *testing.T) {
	wm := NewLLMContext(t.TempDir())

	wm.SetDecisionNeededThisTurn(true)
	wm.SetUpdatedOverview()
	wm.AdvanceDecisionState(false)
	if wm.NeedsDecisionReminder() {
		t.Fatal("did not expect reminder in overview update turn")
	}

	wm.SetDecisionNeededThisTurn(true)
	wm.AdvanceDecisionState(false)
	if wm.NeedsDecisionReminder() {
		t.Fatal("did not expect reminder before threshold")
	}

	wm.SetDecisionNeededThisTurn(true)
	wm.AdvanceDecisionState(false)
	if !wm.NeedsDecisionReminder() {
		t.Fatal("expected reminder after two pending rounds")
	}
}

func TestDecisionReminderClearsWhenDecisionNotNeeded(t *testing.T) {
	wm := NewLLMContext(t.TempDir())

	wm.SetDecisionNeededThisTurn(true)
	wm.SetUpdatedOverview()
	wm.AdvanceDecisionState(false)

	wm.SetDecisionNeededThisTurn(true)
	wm.AdvanceDecisionState(false)
	if wm.NeedsDecisionReminder() {
		t.Fatal("did not expect reminder before threshold")
	}

	wm.SetDecisionNeededThisTurn(false)
	wm.AdvanceDecisionState(false)
	if wm.NeedsDecisionReminder() {
		t.Fatal("expected reminder state to reset when decision is not needed")
	}

	wm.SetDecisionNeededThisTurn(true)
	wm.AdvanceDecisionState(false)
	if wm.NeedsDecisionReminder() {
		t.Fatal("did not expect reminder without a new overview update")
	}
}

func TestDecisionReminderClearsWhenDecisionMade(t *testing.T) {
	wm := NewLLMContext(t.TempDir())

	wm.SetDecisionNeededThisTurn(true)
	wm.SetUpdatedOverview()
	wm.AdvanceDecisionState(false)

	wm.SetDecisionNeededThisTurn(true)
	wm.AdvanceDecisionState(false)
	wm.SetDecisionNeededThisTurn(true)
	wm.AdvanceDecisionState(false)
	if !wm.NeedsDecisionReminder() {
		t.Fatal("expected reminder before decision is made")
	}

	wm.AdvanceDecisionState(true)
	if wm.NeedsDecisionReminder() {
		t.Fatal("expected reminder state to clear after decision is made")
	}
}
