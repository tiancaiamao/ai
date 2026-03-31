package context

import "fmt"

// TriggerChecker evaluates trigger conditions.
type TriggerChecker struct {
	// Configuration (use constants from trigger_config.go)
	IntervalTurns int
	MinTurns      int
	TokenThreshold float64
	TokenUrgent    float64
	StaleCount     int
	MinInterval    int
}

// NewTriggerChecker creates a new TriggerChecker with default config.
func NewTriggerChecker() *TriggerChecker {
	return &TriggerChecker{
		IntervalTurns:  IntervalTurns,
		MinTurns:       MinTurns,
		TokenThreshold: TokenThreshold,
		TokenUrgent:    TokenUrgent,
		StaleCount:     StaleCount,
		MinInterval:    MinInterval,
	}
}

// NewTriggerCheckerWithConfig creates a TriggerChecker with custom config (for testing).
func NewTriggerCheckerWithConfig(config TriggerConfig) *TriggerChecker {
	return &TriggerChecker{
		IntervalTurns:  config.IntervalTurns,
		MinTurns:       config.MinTurns,
		TokenThreshold: config.TokenThreshold,
		TokenUrgent:    config.TokenUrgent,
		StaleCount:     config.StaleCount,
		MinInterval:    config.MinInterval,
	}
}

// TriggerConfig allows custom trigger configuration.
type TriggerConfig struct {
	IntervalTurns  int
	MinTurns       int
	TokenThreshold float64
	TokenUrgent    float64
	StaleCount     int
	MinInterval    int
}

// ShouldTrigger checks if context management should be triggered.
// Returns (shouldTrigger, urgency, reason).
func (t *TriggerChecker) ShouldTrigger(snapshot *ContextSnapshot) (bool, string, string) {
	if snapshot == nil {
		return false, "", "no_snapshot"
	}

	state := snapshot.AgentState

	// 1. Check minimum turn requirement
	if state.TotalTurns < t.MinTurns {
		return false, "", "below_min_turns"
	}

	tokenPercent := snapshot.EstimateTokenPercent()
	staleCount := snapshot.CountStaleOutputs(10) // Count outputs with stale >= 10

	// 2. URGENT mode: token usage critical - ignore minInterval
	if tokenPercent >= t.TokenUrgent {
		return true, UrgencyUrgent, fmt.Sprintf("token_usage_%.0f%%", tokenPercent*100)
	}

	// 3. Check minimum interval for normal triggers
	if state.TurnsSinceLastTrigger < t.MinInterval {
		return false, "", "within_min_interval"
	}

	// 4. Normal trigger conditions
	if tokenPercent >= t.TokenThreshold && staleCount >= 10 {
		return true, UrgencyNormal, "token_and_stale_threshold"
	}

	if tokenPercent >= 0.30 {
		return true, UrgencyNormal, fmt.Sprintf("token_usage_%.0f%%", tokenPercent*100)
	}

	if state.TotalTurns >= 15 && tokenPercent >= 0.25 {
		return true, UrgencyNormal, "periodic_token_check"
	}

	if staleCount >= t.StaleCount {
		return true, UrgencyNormal, fmt.Sprintf("stale_outputs_%d", staleCount)
	}

	// 5. Skip condition: context is healthy
	if state.TotalTurns >= 20 && tokenPercent < 0.30 {
		return false, UrgencySkip, "context_healthy"
	}

	// 6. Periodic check
	if state.TotalTurns%t.IntervalTurns == 0 {
		return true, UrgencyPeriodic, "periodic_check"
	}

	return false, "", "no_trigger"
}

// Trigger urgency levels.
const (
	UrgencyNone     = ""
	UrgencyUrgent   = "urgent"   // Ignores minInterval
	UrgencyNormal   = "normal"   // Standard trigger
	UrgencyPeriodic = "periodic" // Routine check
	UrgencySkip     = "skip"     // Context is healthy, skip
)
