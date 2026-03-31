package context

import (
	"context"
	"fmt"

	"github.com/tiancaiamao/ai/pkg/traceevent"
)

// LogSnapshotEvaluated logs when a context snapshot is evaluated for triggers.
func LogSnapshotEvaluated(ctx context.Context, snapshot *ContextSnapshot) {
	if ctx == nil || snapshot == nil {
		return
	}

	traceevent.Log(ctx,
		traceevent.CategoryEvent,
		"context_snapshot_evaluated",
		traceevent.Field{Key: "total_turns", Value: snapshot.AgentState.TotalTurns},
		traceevent.Field{Key: "tokens_used", Value: snapshot.EstimateTokens()},
		traceevent.Field{Key: "tokens_limit", Value: snapshot.AgentState.TokensLimit},
		traceevent.Field{Key: "token_percent", Value: fmt.Sprintf("%.1f%%", snapshot.EstimateTokenPercent()*100)},
		traceevent.Field{Key: "stale_outputs", Value: snapshot.CountStaleOutputs(10)},
		traceevent.Field{Key: "messages_count", Value: len(snapshot.RecentMessages)},
		traceevent.Field{Key: "turns_since_last_trigger", Value: snapshot.AgentState.TurnsSinceLastTrigger},
	)
}

// LogTriggerChecked logs the result of a trigger check.
func LogTriggerChecked(ctx context.Context, shouldTrigger bool, urgency, reason string, snapshot *ContextSnapshot) {
	if ctx == nil {
		return
	}

	fields := []traceevent.Field{
		{Key: "should_trigger", Value: shouldTrigger},
		{Key: "urgency", Value: urgency},
		{Key: "reason", Value: reason},
	}

	if snapshot != nil {
		fields = append(fields,
			traceevent.Field{Key: "total_turns", Value: snapshot.AgentState.TotalTurns},
			traceevent.Field{Key: "token_percent", Value: fmt.Sprintf("%.1f%%", snapshot.EstimateTokenPercent()*100)},
			traceevent.Field{Key: "stale_outputs", Value: snapshot.CountStaleOutputs(10)},
		)
	}

	traceevent.Log(ctx, traceevent.CategoryEvent, "context_trigger_checked", fields...)
}

// LogCheckpointCreated logs when a checkpoint is created.
func LogCheckpointCreated(ctx context.Context, checkpoint *CheckpointInfo) {
	if ctx == nil || checkpoint == nil {
		return
	}

	traceevent.Log(ctx,
		traceevent.CategoryEvent,
		"context_checkpoint_created",
		traceevent.Field{Key: "checkpoint_path", Value: checkpoint.Path},
		traceevent.Field{Key: "message_index", Value: checkpoint.MessageIndex},
		traceevent.Field{Key: "turn", Value: checkpoint.Turn},
		traceevent.Field{Key: "llm_context_chars", Value: checkpoint.LLMContextChars},
		traceevent.Field{Key: "recent_messages_count", Value: checkpoint.RecentMessagesCount},
	)
}

// LogCheckpointLoaded logs when a checkpoint is loaded.
func LogCheckpointLoaded(ctx context.Context, checkpointID string, messageIndex int, turn int) {
	if ctx == nil {
		return
	}

	traceevent.Log(ctx,
		traceevent.CategoryEvent,
		"context_checkpoint_loaded",
		traceevent.Field{Key: "checkpoint_id", Value: checkpointID},
		traceevent.Field{Key: "message_index", Value: messageIndex},
		traceevent.Field{Key: "turn", Value: turn},
	)
}

// LogJournalEntryAppended logs when a journal entry is appended.
func LogJournalEntryAppended(ctx context.Context, entryType string, entryIndex int) {
	if ctx == nil {
		return
	}

	traceevent.Log(ctx,
		traceevent.CategoryEvent,
		"context_journal_entry_appended",
		traceevent.Field{Key: "entry_type", Value: entryType},
		traceevent.Field{Key: "entry_index", Value: entryIndex},
	)
}

// LogSnapshotReconstructed logs when a snapshot is reconstructed.
func LogSnapshotReconstructed(ctx context.Context, checkpointID string, journalEntriesCount int, messagesCount int) {
	if ctx == nil {
		return
	}

	traceevent.Log(ctx,
		traceevent.CategoryEvent,
		"context_snapshot_reconstructed",
		traceevent.Field{Key: "checkpoint_id", Value: checkpointID},
		traceevent.Field{Key: "journal_entries_count", Value: journalEntriesCount},
		traceevent.Field{Key: "messages_count", Value: messagesCount},
	)
}

// LogMessageTruncated logs when a message is truncated.
func LogMessageTruncated(ctx context.Context, toolCallID string, originalSize int, truncatedSize int) {
	if ctx == nil {
		return
	}

	traceevent.Log(ctx,
		traceevent.CategoryTool,
		"context_message_truncated",
		traceevent.Field{Key: "tool_call_id", Value: toolCallID},
		traceevent.Field{Key: "original_size", Value: originalSize},
		traceevent.Field{Key: "truncated_size", Value: truncatedSize},
		traceevent.Field{Key: "bytes_saved", Value: originalSize - truncatedSize},
	)
}

// LogTruncateApplied logs when a truncate operation is applied to a snapshot.
func LogTruncateApplied(ctx context.Context, toolCallID string, success bool) {
	if ctx == nil {
		return
	}

	status := "success"
	if !success {
		status = "failed"
	}

	traceevent.Log(ctx,
		traceevent.CategoryTool,
		"context_truncate_applied",
		traceevent.Field{Key: "tool_call_id", Value: toolCallID},
		traceevent.Field{Key: "status", Value: status},
	)
}

// LogContextManagementStart logs the start of a context management operation.
func LogContextManagementStart(ctx context.Context, urgency string, turn int) {
	if ctx == nil {
		return
	}

	span := traceevent.StartSpan(ctx, "context_management", traceevent.CategoryEvent,
		traceevent.Field{Key: "urgency", Value: urgency},
		traceevent.Field{Key: "turn", Value: turn},
	)
	span.End()
}

// LogContextManagementDecision logs the decision made by context management.
func LogContextManagementDecision(ctx context.Context, action string, reason string) {
	if ctx == nil {
		return
	}

	traceevent.Log(ctx,
		traceevent.CategoryEvent,
		"context_management_decision",
		traceevent.Field{Key: "action", Value: action},
		traceevent.Field{Key: "reason", Value: reason},
	)
}

// LogContextManagementSkipped logs when context management is skipped.
func LogContextManagementSkipped(ctx context.Context, reason string) {
	if ctx == nil {
		return
	}

	traceevent.Log(ctx,
		traceevent.CategoryEvent,
		"context_management_skipped",
		traceevent.Field{Key: "reason", Value: reason},
	)
}
