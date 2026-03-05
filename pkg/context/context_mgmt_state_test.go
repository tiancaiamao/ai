package context

import (
	"testing"
)

func TestDefaultContextMgmtState(t *testing.T) {
	state := DefaultContextMgmtState()

	if state.ReminderFrequency != 10 {
		t.Fatalf("expected default ReminderFrequency 10, got %d", state.ReminderFrequency)
	}
	if state.ProactiveDecisions != 0 {
		t.Fatalf("expected default ProactiveDecisions 0, got %d", state.ProactiveDecisions)
	}
	if state.ReminderNeeded != 0 {
		t.Fatalf("expected default ReminderNeeded 0, got %d", state.ReminderNeeded)
	}
	if state.SkipUntilTurn != 0 {
		t.Fatalf("expected default SkipUntilTurn 0, got %d", state.SkipUntilTurn)
	}
}

func TestAdjustFrequency_Proactive(t *testing.T) {
	state := DefaultContextMgmtState()

	// Simulate proactive behavior
	state.ProactiveDecisions = 5
	state.ReminderNeeded = 0

	state.AdjustFrequency()

	// Should increase frequency (less reminders)
	if state.ReminderFrequency <= 10 {
		t.Fatalf("expected ReminderFrequency to increase after proactive decisions, got %d", state.ReminderFrequency)
	}
	if state.ReminderFrequency > 30 {
		t.Fatalf("expected ReminderFrequency capped at 30, got %d", state.ReminderFrequency)
	}
}

func TestAdjustFrequency_NeedsReminders(t *testing.T) {
	state := DefaultContextMgmtState()

	// Simulate needing reminders
	state.ProactiveDecisions = 0
	state.ReminderNeeded = 5

	state.AdjustFrequency()

	// Should decrease frequency (more reminders)
	if state.ReminderFrequency >= 10 {
		t.Fatalf("expected ReminderFrequency to decrease after needing reminders, got %d", state.ReminderFrequency)
	}
	if state.ReminderFrequency < 5 {
		t.Fatalf("expected ReminderFrequency floor at 5, got %d", state.ReminderFrequency)
	}
}

func TestAdjustFrequency_VeryProactive(t *testing.T) {
	state := DefaultContextMgmtState()
	state.ReminderFrequency = 15 // Start at 15

	// Very proactive: 10 proactive, 1 reminder
	state.ProactiveDecisions = 10
	state.ReminderNeeded = 1

	state.AdjustFrequency()

	// Should increase by 2
	expected := 17
	if state.ReminderFrequency != expected {
		t.Fatalf("expected ReminderFrequency %d after very proactive behavior, got %d", expected, state.ReminderFrequency)
	}
}

func TestAdjustFrequency_CappedAt30(t *testing.T) {
	state := DefaultContextMgmtState()
	state.ReminderFrequency = 29

	// Very proactive
	state.ProactiveDecisions = 10
	state.ReminderNeeded = 0

	state.AdjustFrequency()

	// Should cap at 30
	if state.ReminderFrequency != 30 {
		t.Fatalf("expected ReminderFrequency capped at 30, got %d", state.ReminderFrequency)
	}

	// Even more proactive shouldn't exceed 30
	state.ProactiveDecisions = 20
	state.AdjustFrequency()

	if state.ReminderFrequency != 30 {
		t.Fatalf("expected ReminderFrequency still capped at 30, got %d", state.ReminderFrequency)
	}
}

func TestAdjustFrequency_FlooredAt5(t *testing.T) {
	state := DefaultContextMgmtState()
	state.ReminderFrequency = 6

	// Needs many reminders
	state.ProactiveDecisions = 0
	state.ReminderNeeded = 10

	state.AdjustFrequency()

	// Should floor at 5
	if state.ReminderFrequency != 5 {
		t.Fatalf("expected ReminderFrequency floored at 5, got %d", state.ReminderFrequency)
	}

	// Even more reminders shouldn't go below 5
	state.ReminderNeeded = 20
	state.AdjustFrequency()

	if state.ReminderFrequency != 5 {
		t.Fatalf("expected ReminderFrequency still floored at 5, got %d", state.ReminderFrequency)
	}
}

func TestRecordDecision_Proactive(t *testing.T) {
	state := DefaultContextMgmtState()

	state.RecordDecision(1, "truncate", false) // not reminded

	if state.ProactiveDecisions != 1 {
		t.Fatalf("expected ProactiveDecisions 1, got %d", state.ProactiveDecisions)
	}
	if state.ReminderNeeded != 0 {
		t.Fatalf("expected ReminderNeeded 0, got %d", state.ReminderNeeded)
	}
	if state.LastDecisionTurn != 1 {
		t.Fatalf("expected LastDecisionTurn 1, got %d", state.LastDecisionTurn)
	}
	if state.LastActionTaken != "truncate" {
		t.Fatalf("expected LastActionTaken 'truncate', got %s", state.LastActionTaken)
	}
}

func TestRecordDecision_Reminded(t *testing.T) {
	state := DefaultContextMgmtState()

	state.RecordDecision(1, "compact", true) // was reminded

	if state.ProactiveDecisions != 0 {
		t.Fatalf("expected ProactiveDecisions 0, got %d", state.ProactiveDecisions)
	}
	if state.ReminderNeeded != 1 {
		t.Fatalf("expected ReminderNeeded 1, got %d", state.ReminderNeeded)
	}
	if state.LastDecisionTurn != 1 {
		t.Fatalf("expected LastDecisionTurn 1, got %d", state.LastDecisionTurn)
	}
	if state.LastActionTaken != "compact" {
		t.Fatalf("expected LastActionTaken 'compact', got %s", state.LastActionTaken)
	}
}

func TestSetSkipUntil(t *testing.T) {
	state := DefaultContextMgmtState()
	state.LastReminderTurn = 5

	state.SetSkipUntil(10, 15, false) // not reminded

	if state.SkipUntilTurn != 25 { // 10 + 15
		t.Fatalf("expected SkipUntilTurn 25, got %d", state.SkipUntilTurn)
	}
	// Setting skip should count as proactive
	if state.ProactiveDecisions != 1 {
		t.Fatalf("expected ProactiveDecisions 1 after setting skip, got %d", state.ProactiveDecisions)
	}
}

func TestShouldShowReminder(t *testing.T) {
	tests := []struct {
		name           string
		turn           int
		actionRequired string
		urgency        string
		skipUntil      int
		lastReminder   int
		frequency      int
		tokensPercent  int
		expected       bool
	}{
		{
			name:           "no action required",
			turn:           10,
			actionRequired: "none",
			urgency:        "low",
			skipUntil:      0,
			lastReminder:   5,
			frequency:      10,
			tokensPercent:  50,
			expected:       false,
		},
		{
			name:           "critical urgency always shows",
			turn:           10,
			actionRequired: "truncate",
			urgency:        "critical",
			skipUntil:      0,
			lastReminder:   5,
			frequency:      10,
			tokensPercent:  50,
			expected:       true,
		},
		{
			name:           "during skip period",
			turn:           10,
			actionRequired: "truncate",
			urgency:        "medium",
			skipUntil:      20,
			lastReminder:   5,
			frequency:      10,
			tokensPercent:  50,
			expected:       false,
		},
		{
			name:           "after skip period",
			turn:           25,
			actionRequired: "truncate",
			urgency:        "medium",
			skipUntil:      20,
			lastReminder:   5,
			frequency:      10,
			tokensPercent:  50,
			expected:       true,
		},
		{
			name:           "before frequency threshold",
			turn:           12,
			actionRequired: "truncate",
			urgency:        "medium",
			skipUntil:      0,
			lastReminder:   5,
			frequency:      10,
			tokensPercent:  50,
			expected:       false, // Only 7 turns since last reminder
		},
		{
			name:           "after frequency threshold",
			turn:           16,
			actionRequired: "truncate",
			urgency:        "medium",
			skipUntil:      0,
			lastReminder:   5,
			frequency:      10,
			tokensPercent:  50,
			expected:       true, // 11 turns since last reminder
		},
		{
			name:           "below 10% tokens threshold - suppress reminder",
			turn:           15,
			actionRequired: "truncate",
			urgency:        "low",
			skipUntil:      0,
			lastReminder:   5,
			frequency:      1,
			tokensPercent:  5,
			expected:       false, // Below 10% threshold
		},
		{
			name:           "at 10% tokens threshold - show reminder",
			turn:           15,
			actionRequired: "truncate",
			urgency:        "low",
			skipUntil:      0,
			lastReminder:   5,
			frequency:      1,
			tokensPercent:  10,
			expected:       true, // At 10% threshold
		},
		{
			name:           "critical overrides 10% threshold",
			turn:           15,
			actionRequired: "truncate",
			urgency:        "critical",
			skipUntil:      0,
			lastReminder:   5,
			frequency:      1,
			tokensPercent:  5,
			expected:       true, // Critical urgency overrides threshold
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := &ContextMgmtState{
				SkipUntilTurn:     tt.skipUntil,
				LastReminderTurn:  tt.lastReminder,
				ReminderFrequency: tt.frequency,
			}

			result := state.ShouldShowReminder(tt.turn, tt.actionRequired, tt.urgency, tt.tokensPercent)
			if result != tt.expected {
				t.Fatalf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

func TestGetScore(t *testing.T) {
	tests := []struct {
		name     string
		proactive int
		reminded  int
		expected string
	}{
		{
			name:     "no data",
			proactive: 0,
			reminded:  0,
			expected: "no_data",
		},
		{
			name:     "excellent",
			proactive: 10,
			reminded:  2,
			expected: "excellent",
		},
		{
			name:     "good",
			proactive: 5,
			reminded:  2,
			expected: "good",
		},
		{
			name:     "fair",
			proactive: 3,
			reminded:  3,
			expected: "fair",
		},
		{
			name:     "needs improvement",
			proactive: 2,
			reminded:  8,
			expected: "needs_improvement",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := &ContextMgmtState{
				ProactiveDecisions: tt.proactive,
				ReminderNeeded:     tt.reminded,
			}

			result := state.GetScore()
			if result != tt.expected {
				t.Fatalf("expected score %q, got %q", tt.expected, result)
			}
		})
	}
}
