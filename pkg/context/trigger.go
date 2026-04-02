package context

import "fmt"

// TriggerChecker evaluates trigger conditions.
type TriggerChecker struct {
	// Token thresholds
	TokenUrgent float64
	TokenHigh   float64
	TokenMedium float64
	TokenLow    float64

	// Tool-call intervals (how many tool calls between triggers)
	IntervalAtLow    int
	IntervalAtMedium int
	IntervalAtHigh   int
	IntervalAtUrgent int

	// Stale output threshold
	StaleCount int
}

// NewTriggerChecker creates a new TriggerChecker with default config.
func NewTriggerChecker() *TriggerChecker {
	return &TriggerChecker{
		TokenUrgent:       TokenUrgent,
		TokenHigh:         TokenHigh,
		TokenMedium:       TokenMedium,
		TokenLow:          TokenLow,
		IntervalAtLow:     IntervalAtLow,
		IntervalAtMedium:  IntervalAtMedium,
		IntervalAtHigh:    IntervalAtHigh,
		IntervalAtUrgent:  IntervalAtUrgent,
		StaleCount:        StaleCount,
	}
}

// NewTriggerCheckerWithConfig creates a TriggerChecker with custom config (for testing).
func NewTriggerCheckerWithConfig(config TriggerConfig) *TriggerChecker {
	return &TriggerChecker{
		TokenUrgent:       config.TokenUrgent,
		TokenHigh:         config.TokenHigh,
		TokenMedium:       config.TokenMedium,
		TokenLow:          config.TokenLow,
		IntervalAtLow:     config.IntervalAtLow,
		IntervalAtMedium:  config.IntervalAtMedium,
		IntervalAtHigh:    config.IntervalAtHigh,
		IntervalAtUrgent:  config.IntervalAtUrgent,
		StaleCount:        config.StaleCount,
	}
}

// TriggerConfig allows custom trigger configuration.
type TriggerConfig struct {
	TokenUrgent      float64
	TokenHigh        float64
	TokenMedium      float64
	TokenLow         float64
	IntervalAtLow    int
	IntervalAtMedium int
	IntervalAtHigh   int
	IntervalAtUrgent int
	StaleCount       int
}

// ShouldTrigger checks if context management should be triggered.
// The primary signal is token usage percentage. Tool call counts determine
// the interval between triggers — higher token usage means more frequent triggers.
//
// Returns (shouldTrigger, urgency, reason).
func (t *TriggerChecker) ShouldTrigger(snapshot *ContextSnapshot) (bool, string, string) {
	if snapshot == nil {
		return false, "", "no_snapshot"
	}

	tokenPercent := snapshot.EstimateTokenPercent()
	toolCallsSince := snapshot.AgentState.ToolCallsSinceLastTrigger

	// 1. URGENT: token usage critical — trigger immediately
	if tokenPercent >= t.TokenUrgent {
		if toolCallsSince >= t.IntervalAtUrgent {
			return true, UrgencyUrgent, fmt.Sprintf("token_usage_%.0f%%", tokenPercent*100)
		}
		return false, "", fmt.Sprintf("urgent_but_interval_%d/%d", toolCallsSince, t.IntervalAtUrgent)
	}

	// 2. HIGH: aggressive truncation
	if tokenPercent >= t.TokenHigh {
		if toolCallsSince >= t.IntervalAtHigh {
			return true, UrgencyNormal, fmt.Sprintf("token_high_%.0f%%", tokenPercent*100)
		}
		return false, "", fmt.Sprintf("high_but_interval_%d/%d", toolCallsSince, t.IntervalAtHigh)
	}

	// 3. MEDIUM: start truncating old outputs
	if tokenPercent >= t.TokenMedium {
		if toolCallsSince >= t.IntervalAtMedium {
			return true, UrgencyNormal, fmt.Sprintf("token_medium_%.0f%%", tokenPercent*100)
		}
		return false, "", fmt.Sprintf("medium_but_interval_%d/%d", toolCallsSince, t.IntervalAtMedium)
	}

	// 4. LOW: minimal intervention
	if tokenPercent >= t.TokenLow {
		if toolCallsSince >= t.IntervalAtLow {
			return true, UrgencyPeriodic, fmt.Sprintf("token_low_%.0f%%", tokenPercent*100)
		}
		return false, "", fmt.Sprintf("low_but_interval_%d/%d", toolCallsSince, t.IntervalAtLow)
	}

	// 5. Below 20% — only trigger if stale outputs are very high
	staleCount := snapshot.CountStaleOutputs(10)
	if staleCount >= t.StaleCount {
		if toolCallsSince >= t.IntervalAtLow {
			return true, UrgencyPeriodic, fmt.Sprintf("stale_outputs_%d", staleCount)
		}
		return false, "", fmt.Sprintf("stale_but_interval_%d/%d", toolCallsSince, t.IntervalAtLow)
	}

	// 6. Context is healthy
	return false, UrgencySkip, fmt.Sprintf("context_healthy_%.0f%%", tokenPercent*100)
}

// Trigger urgency levels.
const (
	UrgencyNone     = ""
	UrgencyUrgent   = "urgent"   // Ignores minInterval
	UrgencyNormal   = "normal"   // Standard trigger
	UrgencyPeriodic = "periodic" // Routine check
	UrgencySkip     = "skip"     // Context is healthy, skip
)
