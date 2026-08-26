package agent

import (
	"fmt"
	"strings"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// patternCall builds a tool call with a stable signature for the given name+arg.
func patternCall(name, arg string) agentctx.ToolCallContent {
	return agentctx.ToolCallContent{
		ID:        "call-" + name + "-" + arg,
		Type:      "toolCall",
		Name:      name,
		Arguments: map[string]any{"key": arg},
	}
}

// feedPattern drives the guard through one call and returns the ObserveResult.
func feedPattern(g *toolLoopGuard, name, arg string) ObserveResult {
	return g.Observe([]agentctx.ToolCallContent{patternCall(name, arg)})
}

func newPatternTestGuard(maxConsecutive int) *toolLoopGuard {
	return newToolLoopGuard(&LoopConfig{MaxConsecutiveToolCalls: maxConsecutive})
}

// TestLoopGuardPattern_TwoCycleTriggers verifies that an alternating A,B,A,B
// loop - invisible to the consecutive-identical counter - is blocked once the
// short pattern has repeated a few times.
func TestLoopGuardPattern_TwoCycleTriggers(t *testing.T) {
	g := newPatternTestGuard(100) // consecutive identical never fires

	seq := []struct {
		name string
		arg  string
	}{
		{"read", "a"}, {"read", "b"},
		{"read", "a"}, {"read", "b"},
		{"read", "a"}, {"read", "b"},
	}

	for i, c := range seq {
		res := feedPattern(g, c.name, c.arg)
		if i < 5 {
			if res.Blocked {
				t.Fatalf("call %d: unexpected block: %s", i+1, res.Reason)
			}
			continue
		}
		// 6th call: patternRun = 5 > defaultLoopMaxPatternRepeats (4).
		if !res.Blocked {
			t.Fatalf("call %d: expected pattern block, got none", i+1)
		}
		if res.HardAbort {
			t.Fatal("expected soft feedback on first pattern trigger, not hard abort")
		}
		if res.FeedbackAttempt != 1 {
			t.Errorf("expected feedback attempt 1, got %d", res.FeedbackAttempt)
		}
		if !strings.Contains(res.Reason, "period 2") {
			t.Errorf("reason should mention period 2: %q", res.Reason)
		}
	}
}

// TestLoopGuardPattern_ThreeCycleTriggers verifies period-3 patterns such as
// A,B,C,A,B,C,A are caught once they repeat.
func TestLoopGuardPattern_ThreeCycleTriggers(t *testing.T) {
	g := newPatternTestGuard(100)

	seq := []struct{ name, arg string }{
		{"read", "a"}, {"read", "b"}, {"read", "c"},
		{"read", "a"}, {"read", "b"}, {"read", "c"},
		{"read", "a"},
	}

	for i, c := range seq {
		res := feedPattern(g, c.name, c.arg)
		if i < 6 {
			if res.Blocked {
				t.Fatalf("call %d: unexpected block: %s", i+1, res.Reason)
			}
			continue
		}
		// 7th call: patternRun = 5 > 4.
		if !res.Blocked {
			t.Fatalf("call %d: expected pattern block, got none", i+1)
		}
		if !strings.Contains(res.Reason, "period 3") {
			t.Errorf("reason should mention period 3: %q", res.Reason)
		}
	}
}

// TestLoopGuardPattern_NoTriggerOnShortOrBrokenSequences verifies that short
// or interrupted sequences do not trip the pattern detector.
func TestLoopGuardPattern_NoTriggerOnShortOrBrokenSequences(t *testing.T) {
	cases := []struct {
		name string
		seq  []struct{ name, arg string }
	}{
		{
			name: "single alternation A,B,A",
			seq:  []struct{ name, arg string }{{"read", "a"}, {"read", "b"}, {"read", "a"}},
		},
		{
			name: "two alternations A,B,A,B (patternRun=3)",
			seq:  []struct{ name, arg string }{{"read", "a"}, {"read", "b"}, {"read", "a"}, {"read", "b"}},
		},
		{
			name: "interrupted A,B,X,A,B",
			seq:  []struct{ name, arg string }{{"read", "a"}, {"read", "b"}, {"read", "x"}, {"read", "a"}, {"read", "b"}},
		},
		{
			name: "three-cycle not yet repeated A,B,C,A,B,C",
			seq:  []struct{ name, arg string }{{"read", "a"}, {"read", "b"}, {"read", "c"}, {"read", "a"}, {"read", "b"}, {"read", "c"}},
		},
		{
			name: "sporadic matches A,B,A,C,A,B,A",
			seq:  []struct{ name, arg string }{{"read", "a"}, {"read", "b"}, {"read", "a"}, {"read", "c"}, {"read", "a"}, {"read", "b"}, {"read", "a"}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := newPatternTestGuard(100)
			for _, c := range tc.seq {
				if res := feedPattern(g, c.name, c.arg); res.Blocked {
					t.Fatalf("unexpected block: %s", res.Reason)
				}
			}
		})
	}
}

// TestLoopGuardPattern_OutputDifferencesDoNotSuppress verifies that output
// polling suppression (which is defined for identical tool calls only) does
// NOT apply to the pattern path: different calls within a pattern may have
// different stable outputs, and the guard must still escalate to hard abort
// rather than treating unrelated output differences as polling.
func TestLoopGuardPattern_OutputDifferencesDoNotSuppress(t *testing.T) {
	g := newPatternTestGuard(100)
	seq := []struct{ name, arg string }{
		{"read", "a"}, {"read", "b"},
		{"read", "a"}, {"read", "b"},
		{"read", "a"}, {"read", "b"},
		{"read", "a"}, {"read", "b"},
	}

	// call 6: patternRun=5 > 4 -> soft feedback #1
	// call 7: soft feedback #2
	// call 8: feedback exhausted -> hard abort (changing cross-phase output
	// must NOT be mistaken for polling).
	var hardAbortAt int
	for i, c := range seq {
		res := feedPattern(g, c.name, c.arg)
		// Each call returns a different output than the previous one, but this
		// is normal for alternating calls and must not suppress hard abort.
		g.NotifyToolOutput(fmt.Sprintf("phase-output-%d", i))
		if res.HardAbort {
			hardAbortAt = i + 1
			break
		}
	}
	if hardAbortAt != 8 {
		t.Fatalf("expected hard abort on call 8 despite differing outputs, got call %d", hardAbortAt)
	}
}

// TestLoopGuardPattern_ConsecutivePollingStillSuppressed verifies that the
// existing polling suppression for identical tool calls (same signature with
// changing output) is unaffected by the new pattern tracking.
func TestLoopGuardPattern_ConsecutivePollingStillSuppressed(t *testing.T) {
	t.Run("changing output never hard aborts", func(t *testing.T) {
		g := newPatternTestGuard(2)
		var sawBlocked, sawHardAbort bool
		var maxFeedback int
		for i := 0; i < 8; i++ {
			res := feedPattern(g, "poll", "status")
			g.NotifyToolOutput(fmt.Sprintf("hash-%d", i)) // always changing
			if res.Blocked {
				sawBlocked = true
				if res.HardAbort {
					sawHardAbort = true
				}
				if res.FeedbackAttempt > maxFeedback {
					maxFeedback = res.FeedbackAttempt
				}
				if !strings.Contains(res.Reason, "polling") {
					t.Errorf("call %d: reason should mention polling: %q", i+1, res.Reason)
				}
			}
		}
		if !sawBlocked {
			t.Fatal("expected consecutive-identical blocks while polling")
		}
		if sawHardAbort {
			t.Fatal("expected NO hard abort while identical calls keep changing output (polling)")
		}
		if maxFeedback <= defaultLoopGuardMaxFeedback {
			t.Errorf("expected feedback to keep escalating past maxFeedbackAttempts=%d while polling, got %d",
				defaultLoopGuardMaxFeedback, maxFeedback)
		}
	})

	t.Run("stable output escalates to hard abort", func(t *testing.T) {
		g := newPatternTestGuard(2)
		var hardAbortCall int
		for i := 0; i < 8; i++ {
			res := feedPattern(g, "poll", "status")
			g.NotifyToolOutput("same-result") // stuck loop
			if res.HardAbort {
				hardAbortCall = i + 1
				break
			}
		}
		if hardAbortCall == 0 {
			t.Fatal("expected hard abort once identical calls stopped changing output")
		}
	})
}

// TestLoopGuardPattern_ConsecutiveCallInvalidatesStalePattern is a regression
// test: a consecutive-identical call in the middle of a near-pattern must drop
// any active pattern candidate so stale period state cannot false-trigger later.
// Before the fix, A,B,A,A,B,A,B,A reused the vanished period-2 candidate from
// the A,B,A prefix (the consecutive A shifted the window alignment) and blocked
// on call 7 even though the sequence is not periodic.
func TestLoopGuardPattern_ConsecutiveCallInvalidatesStalePattern(t *testing.T) {
	g := newPatternTestGuard(100)
	seq := []struct{ name, arg string }{
		{"read", "a"}, {"read", "b"}, {"read", "a"},
		{"read", "a"}, // consecutive call - invalidates the A,B,A pattern
		{"read", "b"}, {"read", "a"}, {"read", "b"}, {"read", "a"},
	}
	for _, c := range seq {
		if res := feedPattern(g, c.name, c.arg); res.Blocked {
			t.Fatalf("unexpected block for non-periodic sequence: %s", res.Reason)
		}
	}

	// Sanity: a genuinely alternating sequence still triggers after the fix.
	g2 := newPatternTestGuard(100)
	clean := []struct{ name, arg string }{
		{"read", "a"}, {"read", "b"},
		{"read", "a"}, {"read", "b"},
		{"read", "a"}, {"read", "b"},
	}
	for i, c := range clean {
		res := feedPattern(g2, c.name, c.arg)
		if i == 5 && !res.Blocked {
			t.Fatal("expected alternating sequence to still trigger after the fix")
		}
	}
}

// TestLoopGuardPattern_ConsecutiveIdenticalStillWorks verifies the existing
// consecutive-identical detection is unaffected by the new pattern tracking.
func TestLoopGuardPattern_ConsecutiveIdenticalStillWorks(t *testing.T) {
	g := newPatternTestGuard(3)
	for i := 0; i < 3; i++ {
		if res := feedPattern(g, "bash", "ls"); res.Blocked {
			t.Fatalf("call %d: unexpected block: %s", i+1, res.Reason)
		}
	}
	res := feedPattern(g, "bash", "ls")
	if !res.Blocked {
		t.Fatal("expected consecutive-identical block on 4th identical call")
	}
	if !strings.Contains(res.Reason, "consecutive identical") {
		t.Errorf("reason should mention consecutive identical calls: %q", res.Reason)
	}
}

// TestLoopGuardPattern_BrokenPatternResetsFeedback verifies that when a pattern
// breaks, the feedback counter resets so a later, separate loop starts from
// soft feedback again instead of escalating straight to a hard abort.
func TestLoopGuardPattern_BrokenPatternResetsFeedback(t *testing.T) {
	g := newPatternTestGuard(100)

	// First loop: A,B,A,B,A,B -> soft feedback #1 on call 6.
	first := []struct{ name, arg string }{
		{"read", "a"}, {"read", "b"},
		{"read", "a"}, {"read", "b"},
		{"read", "a"}, {"read", "b"},
	}
	var firstFeedback int
	for _, c := range first {
		res := feedPattern(g, c.name, c.arg)
		if res.Blocked && res.FeedbackAttempt > firstFeedback {
			firstFeedback = res.FeedbackAttempt
		}
	}
	if firstFeedback != 1 {
		t.Fatalf("expected first loop to reach soft feedback #1, got %d", firstFeedback)
	}

	// Break the pattern entirely.
	feedPattern(g, "write", "notes")
	feedPattern(g, "write", "summary")

	// Second loop restarts from soft feedback #1, not a hard abort.
	second := []struct{ name, arg string }{
		{"read", "x"}, {"read", "y"},
		{"read", "x"}, {"read", "y"},
		{"read", "x"}, {"read", "y"},
	}
	for i, c := range second {
		res := feedPattern(g, c.name, c.arg)
		if i < 5 {
			if res.Blocked {
				t.Fatalf("second loop call %d: unexpected block: %s", i+1, res.Reason)
			}
			continue
		}
		if !res.Blocked {
			t.Fatalf("second loop call %d: expected pattern block", i+1)
		}
		if res.HardAbort {
			t.Fatal("expected soft feedback #1 for the new pattern, got hard abort")
		}
		if res.FeedbackAttempt != 1 {
			t.Errorf("expected fresh feedback attempt 1 after pattern break, got %d", res.FeedbackAttempt)
		}
	}
}
