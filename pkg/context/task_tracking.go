package context

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
)

const (
	// Task tracking thresholds
	baseRoundsBeforeReminder = 10
	minRoundsBeforeCheck     = 3
)

// TaskTrackingState manages the state for llm_context_update tool.
// This tracks the agent's task tracking behavior and provides reminders.
type TaskTrackingState struct {
	mu sync.RWMutex

	// File paths (shared with LLMContext for file operations)
	sessionDir   string
	overviewPath string
	detailPath   string

	// Update tracking
	lastUpdateTime        time.Time
	lastCheckTime         time.Time
	roundsSinceUpdate     int
	silentRoundsRemaining int  // Rounds to skip reminder after update
	wasRemindedLastRound  bool // Was update reminder injected in the current turn?

	// Update statistics for adaptive reminder frequency
	totalUpdates      int // Total number of updates
	autonomousUpdates int // Updates without prompt (LLM self-initiated)
	promptedUpdates   int // Updates after prompt
	nextReminderRound int // Dynamic threshold for next reminder (5-30)
}

// NewTaskTrackingState creates a new TaskTrackingState for the given session directory.
func NewTaskTrackingState(sessionDir string) *TaskTrackingState {
	return &TaskTrackingState{
		sessionDir:        sessionDir,
		overviewPath:      sessionDir + "/" + LLMContextDir + "/" + OverviewFile,
		detailPath:        sessionDir + "/" + LLMContextDir + "/" + DetailDir,
		nextReminderRound: baseRoundsBeforeReminder,
	}
}

// GetPath returns the path to overview.md.
func (t *TaskTrackingState) GetPath() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.overviewPath
}

// GetDetailDir returns the path to the detail directory.
func (t *TaskTrackingState) GetDetailDir() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.detailPath
}

// NeedsReminderMessage checks if a reminder should be shown and returns the message if needed.
func (t *TaskTrackingState) NeedsReminderMessage() bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.roundsSinceUpdate++
	t.lastCheckTime = time.Now()

	// If LLM recently updated, don't remind
	if t.silentRoundsRemaining > 0 {
		t.silentRoundsRemaining--
		slog.Debug("[TaskTracking] Silent rounds remaining", "remaining", t.silentRoundsRemaining)
		return false
	}

	// Use dynamic threshold
	threshold := t.nextReminderRound
	if t.roundsSinceUpdate >= threshold {
		slog.Info("[TaskTracking] Reminder needed",
			"rounds_since_update", t.roundsSinceUpdate,
			"threshold", threshold,
			"total_updates", t.totalUpdates,
			"autonomous", t.autonomousUpdates,
			"prompted", t.promptedUpdates)
		return true
	}

	return false
}

// GetReminderUserMessage returns a user message reminder for updating the llm context.
func (t *TaskTrackingState) GetReminderUserMessage() string {
	t.mu.RLock()
	defer t.mu.RUnlock()

	return `<agent:remind comment="system message by agent, not from real user">

💡 Remember to update your llm context to track progress.

Use llm_context_update tool with:
- content: markdown with current task, decisions, progress
- skip: true if no significant change (just answering questions)

Why this matters:
- Your context is external memory that persists across conversations
- Without updates, you lose track of what you're working on
- Skip when inactive → more reminders (penalty)

Pattern:
1. Task changed → llm_context_update with content
2. No change but active → llm_context_update with skip=true + reasoning
3. Neither → You get frequent reminders (bad)
</agent:remind>`
}

// MarkUpdated marks that the LLM context was updated.
// If wasReminded is true, this update was prompted by a reminder.
func (t *TaskTrackingState) MarkUpdated() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.lastUpdateTime = time.Now()
	t.roundsSinceUpdate = 0
	t.silentRoundsRemaining = minRoundsBeforeCheck
	t.totalUpdates++

	if t.wasRemindedLastRound {
		t.promptedUpdates++
	} else {
		t.autonomousUpdates++
	}

	// Adaptive: if LLM is updating autonomously, reduce reminder frequency
	if t.autonomousUpdates > t.promptedUpdates && t.nextReminderRound < 30 {
		t.nextReminderRound++
		slog.Debug("[TaskTracking] Increasing reminder interval (good behavior)",
			"nextReminderRound", t.nextReminderRound)
	}

	t.wasRemindedLastRound = false
	slog.Info("[TaskTracking] Context updated",
		"total", t.totalUpdates,
		"autonomous", t.autonomousUpdates,
		"prompted", t.promptedUpdates)
}

// MarkSkipped marks that the LLM explicitly skipped updating.
func (t *TaskTrackingState) MarkSkipped(reasoning string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.roundsSinceUpdate = 0
	t.silentRoundsRemaining = minRoundsBeforeCheck
	t.wasRemindedLastRound = false

	slog.Debug("[TaskTracking] Context update skipped", "reasoning", reasoning)
}

// SetWasReminded marks that a reminder was injected in this round.
func (t *TaskTrackingState) SetWasReminded() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.wasRemindedLastRound = true
}

// GetUpdateStats returns statistics about llm_context_update tool calls.
func (t *TaskTrackingState) GetUpdateStats() UpdateStats {
	t.mu.RLock()
	defer t.mu.RUnlock()

	stats := UpdateStats{
		Total:      t.totalUpdates,
		Autonomous: t.autonomousUpdates,
		Prompted:   t.promptedUpdates,
	}

	if t.totalUpdates > 0 {
		stats.ConsciousPct = int(float64(t.autonomousUpdates) * 100 / float64(t.totalUpdates))
		switch {
		case stats.ConsciousPct >= 80:
			stats.Score = "excellent"
		case stats.ConsciousPct >= 60:
			stats.Score = "good"
		default:
			stats.Score = "needs_improvement"
		}
	} else {
		stats.Score = "no_data"
	}

	return stats
}

// GetNextReminderRound returns the current dynamic threshold.
func (t *TaskTrackingState) GetNextReminderRound() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.nextReminderRound
}

// GetUpdateConsciousness returns the percentage of autonomous updates.
func (t *TaskTrackingState) GetUpdateConsciousness() float64 {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.totalUpdates == 0 {
		return 0.0
	}
	return float64(t.autonomousUpdates) / float64(t.totalUpdates)
}

// GetRoundsSinceUpdate returns rounds since last update.
func (t *TaskTrackingState) GetRoundsSinceUpdate() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.roundsSinceUpdate
}

// GetSessionDir returns the session directory path.
func (t *TaskTrackingState) GetSessionDir() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.sessionDir
}

// GetDecisionReminderMessage returns a user message reminder for llm_context_decision.
// This is kept here for backward compatibility but uses ContextMgmtState.
func (t *TaskTrackingState) GetDecisionReminderMessage(availableToolIDs []string, state *ContextMgmtState, urgency string, meta ContextMeta, staleCount int) string {
	proactive := 0
	reminded := 0
	frequency := 10
	score := "no_data"
	if state != nil {
		proactive = state.ProactiveDecisions
		reminded = state.ReminderNeeded
		frequency = state.ReminderFrequency
		score = state.GetScore()
	}

	// Build truncate_ids example from available (non-truncated) tool IDs
	var truncateIDsExample string
	if len(availableToolIDs) > 0 {
		limit := 5
		if len(availableToolIDs) < limit {
			limit = len(availableToolIDs)
		}
		exampleIDs := make([]string, limit)
		for i := 0; i < limit; i++ {
			exampleIDs[i] = availableToolIDs[i]
		}
		if len(availableToolIDs) > limit {
			truncateIDsExample = fmt.Sprintf(`"%s, ...%d more"`,
				strings.Join(exampleIDs, ", "), len(availableToolIDs)-limit)
		} else {
			truncateIDsExample = fmt.Sprintf(`"%s"`, strings.Join(exampleIDs, ", "))
		}
	}

	return fmt.Sprintf(`<agent:remind comment="system message by agent, not from real user">

💡 Context management required (%s): tokens at %d%%, %d stale tool outputs.

<context_meta>
tokens_used: %d
tokens_max: %d
tokens_percent: %.0f%%
messages_in_history: %d
</context_meta>

<decision_autonomy>
proactive_decisions: %d
reminder_needed: %d
reminder_frequency_turns: %d
autonomy_score: %s
</decision_autonomy>

Suggested: %s

HOW TO TRUNCATE (IMPORTANT):
1. Find IDs with stale="N" attribute: <agent:tool id="call_xxx" stale="5" />
2. Batch clean: get 50-100 IDs at once
3. Pass as comma-separated string: truncate_ids: "call_abc, call_def, ..."

EXAMPLE (copy and modify):
decision: "truncate"
reasoning: "Cleaning up %d stale tool outputs"
truncate_ids: %s

⚠️ WARNING: Including already-truncated IDs will result in "0 truncated".`,
		urgency, int(meta.TokensPercent), staleCount,
		meta.TokensUsed, meta.TokensMax, meta.TokensPercent, meta.MessagesInHistory,
		proactive, reminded, frequency, score,
		getSuggestedAction(meta.TokensPercent, staleCount),
		staleCount, truncateIDsExample)
}