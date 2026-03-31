package context

import (
	"fmt"
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

// ReconstructSnapshotWithCheckpoint builds a ContextSnapshot from a checkpoint path and journal entries
func ReconstructSnapshotWithCheckpoint(sessionDir string, checkpoint *CheckpointInfo, journalEntries []JournalEntry) (*ContextSnapshot, error) {
	// 1. Load checkpoint data (LLMContext, AgentState)
	snapshot, err := LoadCheckpoint(sessionDir, checkpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to load checkpoint: %w", err)
	}

	// 2. Replay journal entries starting from checkpoint.MessageIndex
	entries := journalEntries
	if len(entries) > 0 && checkpoint.MessageIndex > 0 {
		// Filter entries to only those after the checkpoint
		startIndex := checkpoint.MessageIndex
		if startIndex < len(entries) {
			entries = entries[startIndex:]
		} else {
			// All journal entries are from before the checkpoint
			entries = []JournalEntry{}
		}
	}

	// 3. For each entry:
	//    - type="message": append to RecentMessages
	//    - type="truncate": mark message as truncated
	for _, entry := range entries {
		if entry.Type == "message" && entry.Message != nil {
			// Append message to RecentMessages
			snapshot.RecentMessages = append(snapshot.RecentMessages, *entry.Message)
		} else if entry.Type == "truncate" && entry.Truncate != nil {
			// Mark message as truncated
			if err := ApplyTruncateToSnapshot(snapshot, entry.Truncate.ToolCallID); err != nil {
				// Log error but continue processing
				// The message might have been removed by truncation
				continue
			}
		}
	}

	// 4. Return reconstructed snapshot
	return snapshot, nil
}

// ApplyTruncateToSnapshot marks a message as truncated in the snapshot
func ApplyTruncateToSnapshot(snapshot *ContextSnapshot, toolCallID string) error {
	// Find message by ToolCallID and set Truncated=true
	for i := range snapshot.RecentMessages {
		if snapshot.RecentMessages[i].ToolCallID == toolCallID {
			snapshot.RecentMessages[i].Truncated = true
			snapshot.RecentMessages[i].TruncatedAt = snapshot.AgentState.TotalTurns
			// OriginalSize should be set when truncate is applied
			if snapshot.RecentMessages[i].OriginalSize == 0 {
				snapshot.RecentMessages[i].OriginalSize = len(snapshot.RecentMessages[i].ExtractText())
			}
			return nil
		}
	}

	return fmt.Errorf("message with tool_call_id %s not found", toolCallID)
}

// ReconstructSnapshotMessages rebuilds RecentMessages from journal entries
func ReconstructSnapshotMessages(snapshot *ContextSnapshot, journalEntries []JournalEntry, startIndex int) error {
	// Clear existing messages
	snapshot.RecentMessages = []AgentMessage{}

	// Replay journal entries starting from startIndex
	for i := startIndex; i < len(journalEntries); i++ {
		entry := journalEntries[i]

		if entry.Type == "message" && entry.Message != nil {
			// Append message to RecentMessages
			snapshot.RecentMessages = append(snapshot.RecentMessages, *entry.Message)
		} else if entry.Type == "truncate" && entry.Truncate != nil {
			// Mark message as truncated
			if err := ApplyTruncateToSnapshot(snapshot, entry.Truncate.ToolCallID); err != nil {
				// Log error but continue processing
				continue
			}
		}
	}

	return nil
}
