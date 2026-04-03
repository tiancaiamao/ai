package agent

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/traceevent"
)

// ExecutionMode represents the current execution mode in the trampoline loop.
type ExecutionMode string

const (
	// ModeNormal executes normal task processing (calls LLM, executes tools, etc.)
	ModeNormal ExecutionMode = "normal"

	// ModeContextMgmt executes context management (truncates messages, updates LLM context)
	ModeContextMgmt ExecutionMode = "context_management"

	// ModeDone indicates the turn is complete
	ModeDone ExecutionMode = "done"

	// ModeError indicates an error occurred
	ModeError ExecutionMode = "error"
)

// ExecuteTurn runs a full user turn using a trampoline pattern.
//
// The trampoline pattern allows switching between Normal and Context Management modes
// as needed. This is important because:
// - Context management may be needed BEFORE processing the user message
// - Context management may be needed DURING normal mode (after tool calls, token growth)
// - After context management, we return to normal mode to continue processing
//
// Flow:
// 1. Determine initial mode (check if context management is needed)
// 2. Loop: execute current mode, get next mode
// 3. Continue until mode is Done or Error
func (a *AgentNew) ExecuteTurn(ctx context.Context, userMessage string) error {
	promptSpan := traceevent.StartSpan(ctx, "prompt", traceevent.CategoryEvent,
		traceevent.Field{Key: "message", Value: userMessage},
	)
	defer promptSpan.End()
	ctx = promptSpan.Context()

	turnSpan := promptSpan.StartChild("turn",
		traceevent.Field{Key: "source", Value: "execute_turn"},
	)
	defer turnSpan.End()
	ctx = turnSpan.Context()

	traceevent.Log(ctx, traceevent.CategoryEvent, "turn_start",
		traceevent.Field{Key: "source", Value: "execute_turn"},
	)

	// Determine initial mode
	mode := a.determineInitialMode(ctx)

	// Trampoline loop
	var userMsgAppended bool
	var resultErr error

	for mode != ModeDone && mode != ModeError {
		// Check for context cancellation (from /steer or /abort)
		// This allows the trampoline to exit immediately when context is canceled
		select {
		case <-ctx.Done():
			slog.Info("[AgentNew] Context canceled in trampoline loop",
				"current_mode", mode,
				"user_msg_appended", userMsgAppended,
			)
			// Return context error - this will be handled by the RPC layer
			return ctx.Err()
		default:
			// Context is still valid, continue
		}

		switch mode {
		case ModeNormal:
			mode, userMsgAppended, resultErr = a.executeNormalStep(ctx, userMessage, userMsgAppended)

		case ModeContextMgmt:
			mode, resultErr = a.executeContextMgmtStep(ctx)

		default:
			resultErr = fmt.Errorf("unknown execution mode: %s", mode)
			mode = ModeError
		}
	}

	if resultErr != nil {
		traceevent.Log(ctx, traceevent.CategoryEvent, "turn_end",
			traceevent.Field{Key: "status", Value: "error"},
			traceevent.Field{Key: "error", Value: resultErr.Error()},
		)
		return resultErr
	}

	traceevent.Log(ctx, traceevent.CategoryEvent, "turn_end",
		traceevent.Field{Key: "status", Value: "success"},
	)

	return nil
}

// determineInitialMode checks if context management is needed before processing user message.
func (a *AgentNew) determineInitialMode(ctx context.Context) ExecutionMode {
	shouldTrigger, urgency, reason := a.checkTriggerConditions(ctx)

	if shouldTrigger && urgency != agentctx.UrgencySkip {
		slog.Info("[AgentNew] Context management needed before processing user message",
			"urgency", urgency,
			"reason", reason,
		)
		return ModeContextMgmt
	}

	return ModeNormal
}

// executeNormalStep executes one step of normal mode.
// It processes the user message and returns the next mode.
//
// Returns:
// - nextMode: ModeDone (complete), ModeContextMgmt (need context management), ModeError
// - userMsgAppended: whether the user message has been appended
// - err: any error that occurred
func (a *AgentNew) executeNormalStep(
	ctx context.Context,
	userMessage string,
	userMsgAppended bool,
) (nextMode ExecutionMode, msgAppended bool, err error) {
	a.snapshotMu.Lock()
	defer a.snapshotMu.Unlock()

	msgAppended = userMsgAppended

	// If user message not yet appended, append it now
	if !msgAppended {
		userMsg := agentctx.AgentMessage{
			Role:         "user",
			Content:      []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: userMessage}},
			Timestamp:    time.Now().Unix(),
			AgentVisible: true,
			UserVisible:  true,
		}

		a.snapshot.RecentMessages = append(a.snapshot.RecentMessages, userMsg)
		traceevent.Log(ctx, traceevent.CategoryEvent, "message_start",
			traceevent.Field{Key: "role", Value: "user"},
			traceevent.Field{Key: "chars", Value: len(userMessage)},
		)

		// Emit message_start/message_end for user message so win UI can display it
		if a.eventEmitter != nil {
			a.eventEmitter.Emit(NewMessageStartEvent(userMsg))
		}

		traceevent.Log(ctx, traceevent.CategoryEvent, "message_end",
			traceevent.Field{Key: "role", Value: "user"},
			traceevent.Field{Key: "chars", Value: len(userMessage)},
		)

		if a.eventEmitter != nil {
			a.eventEmitter.Emit(NewMessageEndEvent(userMsg))
		}

		traceevent.Log(ctx, traceevent.CategoryEvent, "user_message_appended",
			traceevent.Field{Key: "recent_messages_count", Value: len(a.snapshot.RecentMessages)},
		)

		if err := a.journal.AppendMessage(userMsg); err != nil {
			return ModeError, false, fmt.Errorf("failed to append message: %w", err)
		}
		msgAppended = true
	}

	// Execute the conversation loop (LLM → tools → LLM → ...)
	// This may return ModeContextMgmt if token usage grows too large
	nextMode, err = a.executeConversationLoop(ctx)

	// Update turn count
	a.snapshot.AgentState.TotalTurns++
	a.snapshot.AgentState.TurnsSinceLastTrigger++
	a.snapshot.AgentState.UpdatedAt = time.Now()

	// If the agent produced a text response (ModeDone), it made progress.
	// Reset the runaway counter so context management can work normally next time.
	if nextMode == ModeDone {
		a.snapshot.AgentState.ConsecutiveContextMgmtTriggers = 0
		// Also reset loop detector since the agent made meaningful progress
		a.loopDetector.reset()
	}

	return nextMode, msgAppended, err
}

// executeContextMgmtStep executes one step of context management mode.
// Returns the next mode (usually ModeNormal to continue processing, or ModeDone if complete).
func (a *AgentNew) executeContextMgmtStep(ctx context.Context) (nextMode ExecutionMode, err error) {
	a.snapshotMu.Lock()
	defer a.snapshotMu.Unlock()

	// Check trigger conditions to determine urgency
	shouldTrigger, urgency, reason := a.triggerChecker.ShouldTrigger(a.snapshot)

	if !shouldTrigger || urgency == agentctx.UrgencySkip {
		// Context is healthy, no management needed
		slog.Info("[AgentNew] Context management skipped",
			"reason", reason,
		)
		traceevent.Log(ctx, traceevent.CategoryEvent, "context_management_skipped",
			traceevent.Field{Key: "reason", Value: reason},
		)
		return ModeNormal, nil
	}

	// Runaway detection: if context management has been triggered too many times
	// consecutively without progress, force compact instead of letting the LLM choose.
	const maxConsecutiveTriggers = 3
	a.snapshot.AgentState.ConsecutiveContextMgmtTriggers++

	slog.Info("[AgentNew] Executing context management",
		"urgency", urgency,
		"reason", reason,
		"consecutive_triggers", a.snapshot.AgentState.ConsecutiveContextMgmtTriggers,
	)

	traceevent.Log(ctx, traceevent.CategoryEvent, "context_management_decision",
		traceevent.Field{Key: "action", Value: "execute"},
		traceevent.Field{Key: "urgency", Value: urgency},
		traceevent.Field{Key: "reason", Value: reason},
		traceevent.Field{Key: "consecutive_triggers", Value: a.snapshot.AgentState.ConsecutiveContextMgmtTriggers},
	)

	// Execute context management
	mgmtSpan := traceevent.StartSpan(ctx, "context_management", traceevent.CategoryEvent,
		traceevent.Field{Key: "urgency", Value: urgency},
		traceevent.Field{Key: "turn", Value: a.snapshot.AgentState.TotalTurns},
		traceevent.Field{Key: "consecutive_triggers", Value: a.snapshot.AgentState.ConsecutiveContextMgmtTriggers},
	)
	ctx = mgmtSpan.Context()

	if a.snapshot.AgentState.ConsecutiveContextMgmtTriggers >= maxConsecutiveTriggers {
		// Force compact: the LLM's truncate choices are not making progress
		slog.Warn("[AgentNew] Runaway context management detected, forcing compact",
			"consecutive_triggers", a.snapshot.AgentState.ConsecutiveContextMgmtTriggers,
			"max", maxConsecutiveTriggers,
		)
		traceevent.Log(ctx, traceevent.CategoryEvent, "context_management_decision",
			traceevent.Field{Key: "action", Value: "force_compact"},
			traceevent.Field{Key: "reason", Value: "runaway_detection"},
			traceevent.Field{Key: "consecutive_triggers", Value: a.snapshot.AgentState.ConsecutiveContextMgmtTriggers},
		)

		if err := a.performCompaction(ctx); err != nil {
			mgmtSpan.AddField("error", true)
			mgmtSpan.AddField("error_message", err.Error())
			mgmtSpan.End()
			return ModeError, fmt.Errorf("forced compaction failed: %w", err)
		}
	} else {
		if err := a.executeContextMgmtTools(ctx, urgency); err != nil {
			mgmtSpan.AddField("error", true)
			mgmtSpan.AddField("error_message", err.Error())
			mgmtSpan.End()
			return ModeError, fmt.Errorf("context management failed: %w", err)
		}
	}

	mgmtSpan.AddField("action_taken", true)
	mgmtSpan.End()

	// After context management, update trigger tracking
	a.snapshot.AgentState.LastTriggerTurn = a.snapshot.AgentState.TotalTurns
	a.snapshot.AgentState.TurnsSinceLastTrigger = 0
	a.snapshot.AgentState.ToolCallsSinceLastTrigger = 0
	a.snapshot.AgentState.UpdatedAt = time.Now()

	slog.Info("[AgentNew] Context management completed",
		"urgency", urgency,
		"consecutive_triggers", a.snapshot.AgentState.ConsecutiveContextMgmtTriggers,
	)

	// Return to Normal mode to continue processing
	return ModeNormal, nil
}

// checkTriggerConditions checks if context management should be triggered.
func (a *AgentNew) checkTriggerConditions(ctx context.Context) (shouldTrigger bool, urgency string, reason string) {
	a.snapshotMu.Lock()
	defer a.snapshotMu.Unlock()

	agentctx.LogSnapshotEvaluated(ctx, a.snapshot)
	shouldTrigger, urgency, reason = a.triggerChecker.ShouldTrigger(a.snapshot)
	agentctx.LogTriggerChecked(ctx, shouldTrigger, urgency, reason, a.snapshot)

	return shouldTrigger, urgency, reason
}

// ExecuteNormalMode is a compatibility wrapper for tests.
// It calls ExecuteTurn with the given user message.
func (a *AgentNew) ExecuteNormalMode(ctx context.Context, userMessage string) error {
	return a.ExecuteTurn(ctx, userMessage)
}

