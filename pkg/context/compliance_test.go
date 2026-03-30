package context

import (
	"testing"
)

func TestMarkReminderShown(t *testing.T) {
	state := DefaultContextMgmtState()

	if state.ReminderShownThisTurn {
		t.Error("Expected ReminderShownThisTurn to be false initially")
	}

	state.MarkReminderShown()

	if !state.ReminderShownThisTurn {
		t.Error("Expected ReminderShownThisTurn to be true after MarkReminderShown")
	}
}

func TestRecordDecisionForCurrentTurn(t *testing.T) {
	state := DefaultContextMgmtState()
	state.SetCurrentTurn(1)

	if state.DecisionMadeThisTurn {
		t.Error("Expected DecisionMadeThisTurn to be false initially")
	}

	wasReminded := state.RecordDecisionForCurrentTurn("truncate")
	if wasReminded {
		t.Error("Expected wasReminded=false without reminder")
	}

	if !state.DecisionMadeThisTurn {
		t.Error("Expected DecisionMadeThisTurn to be true after RecordDecisionForCurrentTurn")
	}
}

func TestRecordDecisionForCurrentTurnCountsRemindedDecision(t *testing.T) {
	state := DefaultContextMgmtState()
	state.SetCurrentTurn(3)
	state.MarkReminderShown()
	wasReminded := state.RecordDecisionForCurrentTurn("truncate")
	if !wasReminded {
		t.Error("Expected wasReminded=true when reminder was shown")
	}

	if state.ProactiveDecisions != 0 {
		t.Errorf("Expected ProactiveDecisions to stay 0, got %d", state.ProactiveDecisions)
	}
	if state.ReminderNeeded != 1 {
		t.Errorf("Expected ReminderNeeded to be 1, got %d", state.ReminderNeeded)
	}
}

func TestResetTurnTracking(t *testing.T) {
	state := DefaultContextMgmtState()
	state.SetCurrentTurn(1)

	state.MarkReminderShown()
	state.RecordDecisionForCurrentTurn("truncate")

	if !state.ReminderShownThisTurn || !state.DecisionMadeThisTurn {
		t.Error("Expected both flags to be true before reset")
	}

	state.ResetTurnTracking()

	if state.ReminderShownThisTurn || state.DecisionMadeThisTurn {
		t.Error("Expected both flags to be false after reset")
	}
}

func TestCheckAndApplyCompliance_ReminderIgnored(t *testing.T) {
	state := DefaultContextMgmtState()

	// Simulate reminder shown but no decision made
	state.MarkReminderShown()
	// DecisionMadeThisTurn remains false

	initialReminderNeeded := state.ReminderNeeded

	state.CheckAndApplyCompliance()

	if state.ReminderNeeded != initialReminderNeeded+1 {
		t.Errorf("Expected ReminderNeeded to increase from %d to %d, got %d",
			initialReminderNeeded, initialReminderNeeded+1, state.ReminderNeeded)
	}
}

func TestCheckAndApplyCompliance_ReminderFollowed(t *testing.T) {
	state := DefaultContextMgmtState()
	state.SetCurrentTurn(1)

	// Simulate reminder shown and decision made
	state.MarkReminderShown()
	state.RecordDecisionForCurrentTurn("truncate")

	initialReminderNeeded := state.ReminderNeeded

	state.CheckAndApplyCompliance()

	// ReminderNeeded should not increase
	if state.ReminderNeeded != initialReminderNeeded {
		t.Errorf("Expected ReminderNeeded to stay at %d, got %d",
			initialReminderNeeded, state.ReminderNeeded)
	}
}

func TestCheckAndApplyCompliance_NoReminder(t *testing.T) {
	state := DefaultContextMgmtState()

	// No reminder shown, no decision made
	// Both flags remain false

	initialReminderNeeded := state.ReminderNeeded

	state.CheckAndApplyCompliance()

	// ReminderNeeded should not increase
	if state.ReminderNeeded != initialReminderNeeded {
		t.Errorf("Expected ReminderNeeded to stay at %d, got %d",
			initialReminderNeeded, state.ReminderNeeded)
	}
}

func TestCheckAndApplyCompliance_FrequencyAdjustment(t *testing.T) {
	state := DefaultContextMgmtState()

	// Initial frequency is 10
	if state.ReminderFrequency != 10 {
		t.Errorf("Expected initial frequency to be 10, got %d", state.ReminderFrequency)
	}

	// Simulate reminder ignored multiple times to trigger frequency increase
	// Need ratio <= -2 to decrease frequency (increase reminder frequency)
	// Each ignored reminder increases ReminderNeeded by 1
	// After 2 ignored reminders: ratio = 0 - 2 = -2 <= -2, triggers frequency -1
	for i := 0; i < 2; i++ {
		state.MarkReminderShown()
		// No decision made
		state.CheckAndApplyCompliance()
		state.ResetTurnTracking()
	}

	// After 2 ignored reminders, frequency should decrease from 10 to 9
	// (ratio <= -2 triggers ReminderFrequency-1)
	if state.ReminderFrequency != 9 {
		t.Errorf("Expected frequency to decrease to 9 after ignored reminders, got %d", state.ReminderFrequency)
	}
}
