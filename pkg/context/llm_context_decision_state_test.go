package context

import "testing"

func TestDecisionReminderTriggersAfterPressureWarmup(t *testing.T) {
	state := DefaultContextMgmtState()

	show, _ := state.ShouldShowDecisionReminder(1, 35, 5)
	if show {
		t.Fatal("did not expect reminder on first pressured turn")
	}

	show, _ = state.ShouldShowDecisionReminder(2, 35, 5)
	if !show {
		t.Fatal("expected reminder after warmup turns under pressure")
	}
}

func TestDecisionReminderRespectsReminderFrequency(t *testing.T) {
	state := DefaultContextMgmtState()

	// Warmup + first reminder.
	state.ShouldShowDecisionReminder(1, 35, 5)
	show, urgency := state.ShouldShowDecisionReminder(2, 35, 5)
	if !show {
		t.Fatal("expected first reminder")
	}
	state.RecordReminder(2, urgency)
	state.ResetTurnTracking()

	// Default frequency = 10, next one at turn 12.
	show, _ = state.ShouldShowDecisionReminder(11, 35, 5)
	if show {
		t.Fatal("did not expect reminder before frequency window")
	}
	show, _ = state.ShouldShowDecisionReminder(12, 35, 5)
	if !show {
		t.Fatal("expected reminder when frequency window is reached")
	}
}

func TestDecisionReminderResetsAfterPressureClears(t *testing.T) {
	state := DefaultContextMgmtState()

	state.ShouldShowDecisionReminder(1, 35, 5)
	show, urgency := state.ShouldShowDecisionReminder(2, 35, 5)
	if !show {
		t.Fatal("expected reminder before pressure clears")
	}
	state.RecordReminder(2, urgency)
	state.ResetTurnTracking()

	// Pressure clears.
	show, _ = state.ShouldShowDecisionReminder(3, 5, 0)
	if show {
		t.Fatal("did not expect reminder without pressure")
	}

	// Pressure returns: warmup should restart.
	show, _ = state.ShouldShowDecisionReminder(4, 35, 5)
	if show {
		t.Fatal("did not expect reminder on first pressured turn after reset")
	}
}

func TestDecisionReminderFrequencyChangesWithAutonomy(t *testing.T) {
	proactive := DefaultContextMgmtState()
	proactive.RecordDecision(1, "truncate", false)
	proactive.RecordDecision(2, "truncate", false)
	if proactive.ReminderFrequency <= 10 {
		t.Fatalf("expected proactive decisions to reduce reminder frequency, got %d", proactive.ReminderFrequency)
	}

	ignored := DefaultContextMgmtState()
	ignored.RecordReminder(1, "high")
	ignored.CheckAndApplyCompliance()
	ignored.ResetTurnTracking()
	ignored.RecordReminder(2, "high")
	ignored.CheckAndApplyCompliance()
	ignored.ResetTurnTracking()
	if ignored.ReminderFrequency >= 10 {
		t.Fatalf("expected ignored reminders to increase reminder frequency, got %d", ignored.ReminderFrequency)
	}
}
