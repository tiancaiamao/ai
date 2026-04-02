package context

import (
	"fmt"
	"log/slog"
)

// ReconstructSnapshot builds a ContextSnapshot from a checkpoint and journal entries
func ReconstructSnapshot(checkpoint *CheckpointInfo, journalEntries []JournalEntry) (*ContextSnapshot, error) {
	// 1. Load checkpoint data (LLMContext, AgentState)
	// Note: This function expects the checkpoint to be loaded separately
	// The caller should first call LoadCheckpoint, then call ReconstructSnapshot with the loaded snapshot

	// For now, we'll reconstruct messages from journal entries
	// This function will be called after LoadCheckpoint, so we need to handle the case
	// where snapshot.RecentMessages is empty

	return nil, fmt.Errorf("ReconstructSnapshot should be called with a pre-loaded snapshot")
}

// ReconstructSnapshotWithCheckpoint builds a ContextSnapshot from a checkpoint and journal entries.
// It loads the checkpoint (which includes LLMContext and AgentState), then:
// - If checkpoint has messages.jsonl with RecentMessages: replay journal entries AFTER checkpoint.MessageIndex
// - If checkpoint has no/empty messages.jsonl: replay ALL journal entries from the beginning
func ReconstructSnapshotWithCheckpoint(sessionDir string, checkpoint *CheckpointInfo, journalEntries []JournalEntry) (*ContextSnapshot, error) {
	// 1. Load checkpoint data (LLMContext, AgentState, RecentMessages)
	snapshot, err := LoadCheckpoint(sessionDir, checkpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to load checkpoint: %w", err)
	}

	// 2. Determine where to start replaying
	// If checkpoint has RecentMessages (from messages.jsonl), replay from checkpoint.MessageIndex
	// Otherwise, replay from the beginning (messageIndex 0)
	var startIndex int
	if len(snapshot.RecentMessages) > 0 {
		// Checkpoint has messages, replay only the增量 after checkpoint
		startIndex = checkpoint.MessageIndex
	} else {
		// Checkpoint has no messages (or old format), replay from beginning
		startIndex = 0
	}

	// Ensure startIndex is within bounds
	if startIndex > len(journalEntries) {
		startIndex = len(journalEntries)
	}

	// 3. Replay journal entries from startIndex
	for i := startIndex; i < len(journalEntries); i++ {
		entry := journalEntries[i]
		if entry.Type == "message" && entry.Message != nil {
			snapshot.RecentMessages = append(snapshot.RecentMessages, *entry.Message)
		} else if entry.Type == "truncate" && entry.Truncate != nil {
			if err := ApplyTruncateToSnapshot(snapshot, *entry.Truncate); err != nil {
				// Message not found - this can happen if:
				// 1. The message was compacted (removed) in a previous compact event
				// 2. The tool_call_id is invalid
				// Log but continue processing - the truncate is no longer applicable
				slog.Debug("[Reconstruction] Truncate target not found, skipping",
					"tool_call_id", entry.Truncate.ToolCallID,
					"turn", entry.Truncate.Turn,
					"reason", err.Error(),
				)
				continue
			}
		} else if entry.Type == "compact" && entry.Compact != nil {
			// Compact event: LLM generated a summary, RecentMessages was cleared
			// Any subsequent truncate events that reference messages before this point
			// will not find their targets (expected behavior)
			slog.Debug("[Reconstruction] Processing compact event",
				"turn", entry.Compact.Turn,
				"kept_messages", entry.Compact.KeptMessageCount,
				"summary_chars", len(entry.Compact.Summary),
			)
			snapshot.LLMContext = entry.Compact.Summary
			snapshot.RecentMessages = []AgentMessage{}
		}
	}

	return snapshot, nil
}

// ApplyTruncateToSnapshot marks a message as truncated in the snapshot
func ApplyTruncateToSnapshot(snapshot *ContextSnapshot, truncateEvent TruncateEvent) error {
	// Find message by ToolCallID and set Truncated=true
	for i := range snapshot.RecentMessages {
		if snapshot.RecentMessages[i].ToolCallID == truncateEvent.ToolCallID {
			snapshot.RecentMessages[i].Truncated = true
			snapshot.RecentMessages[i].TruncatedAt = truncateEvent.Turn
			// OriginalSize should be set when truncate is applied
			if snapshot.RecentMessages[i].OriginalSize == 0 {
				snapshot.RecentMessages[i].OriginalSize = len(snapshot.RecentMessages[i].ExtractText())
			}
			return nil
		}
	}

	return fmt.Errorf("message with tool_call_id %s not found", truncateEvent.ToolCallID)
}

// ReconstructSnapshotMessages rebuilds RecentMessages from journal entries starting at startIndex.
func ReconstructSnapshotMessages(snapshot *ContextSnapshot, journalEntries []JournalEntry, startIndex int) error {
	// Replay journal entries starting from startIndex
	for i := startIndex; i < len(journalEntries); i++ {
		entry := journalEntries[i]

		if entry.Type == "message" && entry.Message != nil {
			snapshot.RecentMessages = append(snapshot.RecentMessages, *entry.Message)
		} else if entry.Type == "truncate" && entry.Truncate != nil {
			if err := ApplyTruncateToSnapshot(snapshot, *entry.Truncate); err != nil {
				continue
			}
		} else if entry.Type == "compact" && entry.Compact != nil {
			snapshot.LLMContext = entry.Compact.Summary
			snapshot.RecentMessages = []AgentMessage{}
		}
	}

	return nil
}
