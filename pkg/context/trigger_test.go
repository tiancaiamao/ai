package context

import (
	"testing"
)

// TestNewTriggerChecker tests creating a new trigger checker with default config.
func TestNewTriggerChecker(t *testing.T) {
	checker := NewTriggerChecker()

	if checker == nil {
		t.Fatal("NewTriggerChecker returned nil")
	}

	// Verify default values
	if checker.TokenUrgent != TokenUrgent {
		t.Errorf("Expected TokenUrgent %f, got %f", TokenUrgent, checker.TokenUrgent)
	}
	if checker.TokenHigh != TokenHigh {
		t.Errorf("Expected TokenHigh %f, got %f", TokenHigh, checker.TokenHigh)
	}
	if checker.TokenMedium != TokenMedium {
		t.Errorf("Expected TokenMedium %f, got %f", TokenMedium, checker.TokenMedium)
	}
	if checker.TokenLow != TokenLow {
		t.Errorf("Expected TokenLow %f, got %f", TokenLow, checker.TokenLow)
	}
	if checker.IntervalAtLow != IntervalAtLow {
		t.Errorf("Expected IntervalAtLow %d, got %d", IntervalAtLow, checker.IntervalAtLow)
	}
	if checker.IntervalAtMedium != IntervalAtMedium {
		t.Errorf("Expected IntervalAtMedium %d, got %d", IntervalAtMedium, checker.IntervalAtMedium)
	}
	if checker.IntervalAtHigh != IntervalAtHigh {
		t.Errorf("Expected IntervalAtHigh %d, got %d", IntervalAtHigh, checker.IntervalAtHigh)
	}
	if checker.IntervalAtUrgent != IntervalAtUrgent {
		t.Errorf("Expected IntervalAtUrgent %d, got %d", IntervalAtUrgent, checker.IntervalAtUrgent)
	}
	if checker.StaleCount != StaleCount {
		t.Errorf("Expected StaleCount %d, got %d", StaleCount, checker.StaleCount)
	}
}

// TestNewTriggerCheckerWithConfig tests creating a trigger checker with custom config.
func TestNewTriggerCheckerWithConfig(t *testing.T) {
	config := TriggerConfig{
		TokenUrgent:      0.8,
		TokenHigh:        0.6,
		TokenMedium:      0.4,
		TokenLow:         0.2,
		IntervalAtLow:    20,
		IntervalAtMedium: 10,
		IntervalAtHigh:   3,
		IntervalAtUrgent: 1,
		StaleCount:       20,
	}

	checker := NewTriggerCheckerWithConfig(config)

	if checker.TokenUrgent != 0.8 {
		t.Errorf("Expected TokenUrgent 0.8, got %f", checker.TokenUrgent)
	}
	if checker.TokenHigh != 0.6 {
		t.Errorf("Expected TokenHigh 0.6, got %f", checker.TokenHigh)
	}
	if checker.IntervalAtLow != 20 {
		t.Errorf("Expected IntervalAtLow 20, got %d", checker.IntervalAtLow)
	}
	if checker.StaleCount != 20 {
		t.Errorf("Expected StaleCount 20, got %d", checker.StaleCount)
	}
}

// TestShouldTrigger_NilSnapshot tests trigger check with nil snapshot.
func TestShouldTrigger_NilSnapshot(t *testing.T) {
	checker := NewTriggerChecker()

	shouldTrigger, urgency, reason := checker.ShouldTrigger(nil)

	if shouldTrigger {
		t.Error("Expected shouldTrigger to be false for nil snapshot")
	}
	if urgency != "" {
		t.Errorf("Expected empty urgency, got %s", urgency)
	}
	if reason != "no_snapshot" {
		t.Errorf("Expected reason 'no_snapshot', got %s", reason)
	}
}

// TestShouldTrigger_BelowAllThresholds tests that low token usage doesn't trigger.
func TestShouldTrigger_BelowAllThresholds(t *testing.T) {
	checker := NewTriggerChecker()
	snapshot := NewContextSnapshot("test-session", "/test/dir")
	snapshot.AgentState.TokensUsed = 10000 // 5% of 200000
	snapshot.AgentState.TokensLimit = 200000
	snapshot.AgentState.ToolCallsSinceLastTrigger = 50

	shouldTrigger, urgency, reason := checker.ShouldTrigger(snapshot)

	if shouldTrigger {
		t.Error("Expected shouldTrigger to be false for very low token usage")
	}
	if urgency != UrgencySkip {
		t.Errorf("Expected urgency %s, got %s", UrgencySkip, urgency)
	}
	if reason != "context_healthy_5%" {
		t.Errorf("Expected reason 'context_healthy_5%%', got %s", reason)
	}
}

// TestShouldTrigger_UrgentTokenUsage tests urgent trigger at high token usage.
func TestShouldTrigger_UrgentTokenUsage(t *testing.T) {
	checker := NewTriggerChecker()
	snapshot := NewContextSnapshot("test-session", "/test/dir")
	snapshot.AgentState.TokensUsed = 150000       // 75% of 200000
	snapshot.AgentState.TokensLimit = 200000
	snapshot.AgentState.ToolCallsSinceLastTrigger = 1 // IntervalAtUrgent = 1

	shouldTrigger, urgency, reason := checker.ShouldTrigger(snapshot)

	if !shouldTrigger {
		t.Error("Expected shouldTrigger to be true for urgent token usage")
	}
	if urgency != UrgencyUrgent {
		t.Errorf("Expected urgency %s, got %s", UrgencyUrgent, urgency)
	}
	if reason != "token_usage_75%" {
		t.Errorf("Expected reason 'token_usage_75%%', got %s", reason)
	}
}

// TestShouldTrigger_UrgentButWaitingInterval tests urgent but waiting for interval.
func TestShouldTrigger_UrgentButWaitingInterval(t *testing.T) {
	checker := NewTriggerChecker()
	snapshot := NewContextSnapshot("test-session", "/test/dir")
	snapshot.AgentState.TokensUsed = 150000 // 75% of 200000
	snapshot.AgentState.TokensLimit = 200000
	snapshot.AgentState.ToolCallsSinceLastTrigger = 0 // Not yet 1 tool call

	shouldTrigger, _, reason := checker.ShouldTrigger(snapshot)

	if shouldTrigger {
		t.Error("Expected shouldTrigger to be false when interval not met")
	}
	if reason != "urgent_but_interval_0/1" {
		t.Errorf("Expected reason 'urgent_but_interval_0/1', got %s", reason)
	}
}

// TestShouldTrigger_HighTokenUsage tests high token usage trigger.
func TestShouldTrigger_HighTokenUsage(t *testing.T) {
	checker := NewTriggerChecker()
	snapshot := NewContextSnapshot("test-session", "/test/dir")
	snapshot.AgentState.TokensUsed = 110000       // 55% of 200000
	snapshot.AgentState.TokensLimit = 200000
	snapshot.AgentState.ToolCallsSinceLastTrigger = 5 // IntervalAtHigh = 5

	shouldTrigger, urgency, _ := checker.ShouldTrigger(snapshot)

	if !shouldTrigger {
		t.Error("Expected shouldTrigger to be true for high token usage")
	}
	if urgency != UrgencyNormal {
		t.Errorf("Expected urgency %s, got %s", UrgencyNormal, urgency)
	}
}

// TestShouldTrigger_MediumTokenUsage tests medium token usage trigger.
func TestShouldTrigger_MediumTokenUsage(t *testing.T) {
	checker := NewTriggerChecker()
	snapshot := NewContextSnapshot("test-session", "/test/dir")
	snapshot.AgentState.TokensUsed = 70000        // 35% of 200000
	snapshot.AgentState.TokensLimit = 200000
	snapshot.AgentState.ToolCallsSinceLastTrigger = 15 // IntervalAtMedium = 15

	shouldTrigger, urgency, _ := checker.ShouldTrigger(snapshot)

	if !shouldTrigger {
		t.Error("Expected shouldTrigger to be true for medium token usage")
	}
	if urgency != UrgencyNormal {
		t.Errorf("Expected urgency %s, got %s", UrgencyNormal, urgency)
	}
}

// TestShouldTrigger_LowTokenUsage tests low token usage trigger.
func TestShouldTrigger_LowTokenUsage(t *testing.T) {
	checker := NewTriggerChecker()
	snapshot := NewContextSnapshot("test-session", "/test/dir")
	snapshot.AgentState.TokensUsed = 45000        // 22.5% of 200000
	snapshot.AgentState.TokensLimit = 200000
	snapshot.AgentState.ToolCallsSinceLastTrigger = 30 // IntervalAtLow = 30

	shouldTrigger, urgency, _ := checker.ShouldTrigger(snapshot)

	if !shouldTrigger {
		t.Error("Expected shouldTrigger to be true for low token usage with enough tool calls")
	}
	if urgency != UrgencyPeriodic {
		t.Errorf("Expected urgency %s, got %s", UrgencyPeriodic, urgency)
	}
}

// TestShouldTrigger_StaleOutputFallback tests stale output trigger below token threshold.
func TestShouldTrigger_StaleOutputFallback(t *testing.T) {
	checker := NewTriggerChecker()
	snapshot := NewContextSnapshot("test-session", "/test/dir")
	snapshot.AgentState.TokensUsed = 30000        // 15% — below 20% threshold
	snapshot.AgentState.TokensLimit = 200000
	snapshot.AgentState.ToolCallsSinceLastTrigger = 30

	// Need 25 tool results so that CountStaleOutputs(10) >= StaleCount(15)
	// With 25 results: stale >= 10 means the 15 oldest (indices 10-24) qualify
	for i := 0; i < 25; i++ {
		msg := NewToolResultMessage("call-123", "test_tool", []ContentBlock{
			TextContent{Type: "text", Text: "Tool output content"},
		}, false)
		snapshot.RecentMessages = append(snapshot.RecentMessages, msg)
	}

	shouldTrigger, urgency, reason := checker.ShouldTrigger(snapshot)

	if !shouldTrigger {
		t.Error("Expected shouldTrigger to be true for stale outputs")
	}
	if urgency != UrgencyPeriodic {
		t.Errorf("Expected urgency %s, got %s", UrgencyPeriodic, urgency)
	}
	if reason != "stale_outputs_15" {
		t.Errorf("Expected reason 'stale_outputs_15', got %s", reason)
	}
}

// TestShouldTrigger_StaleOutputButIntervalNotMet tests stale outputs with interval not met.
func TestShouldTrigger_StaleOutputButIntervalNotMet(t *testing.T) {
	checker := NewTriggerChecker()
	snapshot := NewContextSnapshot("test-session", "/test/dir")
	snapshot.AgentState.TokensUsed = 30000 // 15%
	snapshot.AgentState.TokensLimit = 200000
	snapshot.AgentState.ToolCallsSinceLastTrigger = 5 // Below IntervalAtLow (30)

	// Add 25 tool results
	for i := 0; i < 25; i++ {
		msg := NewToolResultMessage("call-123", "test_tool", []ContentBlock{
			TextContent{Type: "text", Text: "Tool output content"},
		}, false)
		snapshot.RecentMessages = append(snapshot.RecentMessages, msg)
	}

	shouldTrigger, _, reason := checker.ShouldTrigger(snapshot)

	if shouldTrigger {
		t.Error("Expected shouldTrigger to be false when interval not met")
	}
	if reason != "stale_but_interval_5/30" {
		t.Errorf("Expected reason 'stale_but_interval_5/30', got %s", reason)
	}
}

// TestShouldTrigger_TokenEscalation tests that higher token usage needs fewer tool calls.
func TestShouldTrigger_TokenEscalation(t *testing.T) {
	checker := NewTriggerChecker()

	tests := []struct {
		name              string
		tokensPercent     float64
		toolCallsSince    int
		expectTrigger     bool
		expectedUrgency   string
	}{
		// At 75% (urgent): needs 1 tool call
		{"urgent_0_calls", 0.75, 0, false, ""},
		{"urgent_1_call", 0.75, 1, true, UrgencyUrgent},

		// At 55% (high): needs 5 tool calls
		{"high_4_calls", 0.55, 4, false, ""},
		{"high_5_calls", 0.55, 5, true, UrgencyNormal},

		// At 35% (medium): needs 15 tool calls
		{"medium_14_calls", 0.35, 14, false, ""},
		{"medium_15_calls", 0.35, 15, true, UrgencyNormal},

		// At 25% (low): needs 30 tool calls
		{"low_29_calls", 0.25, 29, false, ""},
		{"low_30_calls", 0.25, 30, true, UrgencyPeriodic},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snapshot := NewContextSnapshot("test-session", "/test/dir")
			snapshot.AgentState.TokensUsed = int(tt.tokensPercent * 200000)
			snapshot.AgentState.TokensLimit = 200000
			snapshot.AgentState.ToolCallsSinceLastTrigger = tt.toolCallsSince

			shouldTrigger, urgency, _ := checker.ShouldTrigger(snapshot)

			if shouldTrigger != tt.expectTrigger {
				t.Errorf("Expected shouldTrigger=%v, got %v", tt.expectTrigger, shouldTrigger)
			}
			if shouldTrigger && urgency != tt.expectedUrgency {
				t.Errorf("Expected urgency=%s, got %s", tt.expectedUrgency, urgency)
			}
		})
	}
}

// TestShouldTrigger_CurrentRealState tests with the user's actual runtime state.
// tokens_percent: 23.3%, recent_messages: 199, stale_outputs: 85, turn: 2
func TestShouldTrigger_CurrentRealState(t *testing.T) {
	checker := NewTriggerChecker()
	snapshot := NewContextSnapshot("test-session", "/test/dir")
	snapshot.AgentState.TokensUsed = 46609
	snapshot.AgentState.TokensLimit = 200000
	// ToolCallsSinceLastTrigger would be ~100 if 199 messages with many tool calls
	snapshot.AgentState.ToolCallsSinceLastTrigger = 100

	// At 23.3%, this is in the "low" band (20-30%), interval = 30
	// With 100 tool calls since last trigger, should trigger
	shouldTrigger, urgency, _ := checker.ShouldTrigger(snapshot)

	if !shouldTrigger {
		t.Error("Expected shouldTrigger to be true with 23.3% tokens and 100 tool calls")
	}
	if urgency != UrgencyPeriodic {
		t.Errorf("Expected urgency %s, got %s", UrgencyPeriodic, urgency)
	}
}

// ============================================================================
// Stale and token estimation tests (kept from original)
// ============================================================================

// TestCalculateStale tests the stale calculation function.
func TestCalculateStale(t *testing.T) {
	tests := []struct {
		name                    string
		resultIndex             int
		totalVisibleToolResults int
		expected                int
	}{
		{
			name:                    "First result (newest)",
			resultIndex:             0,
			totalVisibleToolResults: 10,
			expected:                9,
		},
		{
			name:                    "Middle result",
			resultIndex:             5,
			totalVisibleToolResults: 10,
			expected:                4,
		},
		{
			name:                    "Last result (oldest)",
			resultIndex:             9,
			totalVisibleToolResults: 10,
			expected:                0,
		},
		{
			name:                    "Single result",
			resultIndex:             0,
			totalVisibleToolResults: 1,
			expected:                0,
		},
		{
			name:                    "Zero total results",
			resultIndex:             0,
			totalVisibleToolResults: 0,
			expected:                0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := CalculateStale(tt.resultIndex, tt.totalVisibleToolResults)
			if result != tt.expected {
				t.Errorf("CalculateStale(%d, %d) = %d, expected %d",
					tt.resultIndex, tt.totalVisibleToolResults, result, tt.expected)
			}
		})
	}
}

// TestCountStaleOutputs tests counting stale outputs.
func TestCountStaleOutputs(t *testing.T) {
	snapshot := NewContextSnapshot("test-session", "/test/dir")

	// Add 20 tool results
	for i := 0; i < 20; i++ {
		msg := NewToolResultMessage("call-123", "test_tool", []ContentBlock{
			TextContent{Type: "text", Text: "Tool output content"},
		}, false)
		snapshot.RecentMessages = append(snapshot.RecentMessages, msg)
	}

	// Count with threshold 10
	count := snapshot.CountStaleOutputs(10)
	if count != 10 {
		t.Errorf("CountStaleOutputs(10) = %d, expected 10", count)
	}

	// Count with threshold 5
	count = snapshot.CountStaleOutputs(5)
	if count != 15 {
		t.Errorf("CountStaleOutputs(5) = %d, expected 15", count)
	}
}

// TestGetVisibleToolResults tests getting visible tool results.
func TestGetVisibleToolResults(t *testing.T) {
	snapshot := NewContextSnapshot("test-session", "/test/dir")

	// Add various messages
	snapshot.RecentMessages = append(snapshot.RecentMessages, NewUserMessage("Hello"))
	snapshot.RecentMessages = append(snapshot.RecentMessages, NewAssistantMessage())

	// Add tool result
	toolResult := NewToolResultMessage("call-123", "test_tool", []ContentBlock{
		TextContent{Type: "text", Text: "Tool output"},
	}, false)
	snapshot.RecentMessages = append(snapshot.RecentMessages, toolResult)

	// Add truncated tool result (should not be included)
	truncatedResult := NewToolResultMessage("call-456", "test_tool", []ContentBlock{
		TextContent{Type: "text", Text: "Truncated output"},
	}, false)
	truncatedResult.Truncated = true
	snapshot.RecentMessages = append(snapshot.RecentMessages, truncatedResult)

	results := snapshot.GetVisibleToolResults()

	if len(results) != 1 {
		t.Errorf("GetVisibleToolResults() returned %d results, expected 1", len(results))
	}

	if results[0].ToolCallID != "call-123" {
		t.Errorf("Expected ToolCallID 'call-123', got %s", results[0].ToolCallID)
	}
}

// TestEstimateTokens tests token estimation.
func TestEstimateTokens(t *testing.T) {
	snapshot := NewContextSnapshot("test-session", "/test/dir")
	snapshot.AgentState.TokensLimit = 200000

	// Test with actual tokens used
	snapshot.AgentState.TokensUsed = 50000
	tokens := snapshot.EstimateTokens()
	if tokens != 50000 {
		t.Errorf("EstimateTokens() with actual tokens = %d, expected 50000", tokens)
	}

	// Test with estimation
	snapshot.AgentState.TokensUsed = 0
	snapshot.LLMContext = "This is a test context with some content"
	snapshot.RecentMessages = append(snapshot.RecentMessages, NewUserMessage("Hello, this is a test message"))

	tokens = snapshot.EstimateTokens()
	if tokens == 0 {
		t.Error("EstimateTokens() returned 0, expected > 0")
	}

	expectedMin := (len(snapshot.LLMContext) + len("Hello, this is a test message")) / 4
	if tokens < expectedMin {
		t.Errorf("EstimateTokens() = %d, expected at least %d", tokens, expectedMin)
	}
}

// TestEstimateTokenPercent tests token percentage calculation.
func TestEstimateTokenPercent(t *testing.T) {
	snapshot := NewContextSnapshot("test-session", "/test/dir")
	snapshot.AgentState.TokensLimit = 200000
	snapshot.AgentState.TokensUsed = 50000

	percent := snapshot.EstimateTokenPercent()
	expected := 0.25

	if percent != expected {
		t.Errorf("EstimateTokenPercent() = %f, expected %f", percent, expected)
	}

	// Test with zero limit
	snapshot.AgentState.TokensLimit = 0
	percent = snapshot.EstimateTokenPercent()
	if percent != 0 {
		t.Errorf("EstimateTokenPercent() with zero limit = %f, expected 0", percent)
	}

	// Test with nil snapshot
	var nilSnapshot *ContextSnapshot
	percent = nilSnapshot.EstimateTokenPercent()
	if percent != 0 {
		t.Errorf("EstimateTokenPercent() with nil snapshot = %f, expected 0", percent)
	}
}
