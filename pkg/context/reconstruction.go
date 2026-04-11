package context

import (
	"fmt"
	"log/slog"
)

// ReconstructSnapshotWithCheckpoint builds a ContextSnapshot from a checkpoint and journal entries.
// It loads the checkpoint (which includes LLMContext, AgentState, RecentMessages), then:
//   - If checkpoint has RecentMessages: replay journal entries AFTER checkpoint.MessageIndex
//   - If checkpoint has no RecentMessages: replay ALL journal entries from the beginning
// After replaying messages, it recalculates runtime counters (tool calls, turns) from the replayed messages
// to ensure accurate state after resume operations.
func ReconstructSnapshotWithCheckpoint(sessionDir string, checkpoint *CheckpointInfo, journalEntries []JournalEntry) (*ContextSnapshot, error) {
	snapshot, err := LoadCheckpoint(sessionDir, checkpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to load checkpoint: %w", err)
	}

	var startIndex int
	if len(snapshot.RecentMessages) > 0 {
		startIndex = checkpoint.MessageIndex
	} else {
		startIndex = 0
	}

	if startIndex > len(journalEntries) {
		startIndex = len(journalEntries)
	}

	// Track counters for messages replayed after checkpoint
	toolCallsAfterCheckpoint := 0
	turnsAfterCheckpoint := 0
	lastTurn := snapshot.AgentState.TotalTurns
	lastLLMContextUpdate := snapshot.AgentState.LastLLMContextUpdate

	for i := startIndex; i < len(journalEntries); i++ {
		entry := journalEntries[i]
		if entry.Type == "message" && entry.Message != nil {
			msg := entry.Message
			snapshot.RecentMessages = append(snapshot.RecentMessages, *msg)

			// Count tool calls for trigger counters
			if msg.Role == "toolResult" {
				toolCallsAfterCheckpoint++
			}

			// Count turns (each assistant message is a new turn)
			if msg.Role == "assistant" {
				turnsAfterCheckpoint++
				lastTurn++
			}

			// Track last LLM context update (from update_llm_context tool)
			if msg.Role == "toolResult" && msg.ToolName == "update_llm_context" {
				lastLLMContextUpdate = lastTurn
			}
		} else if entry.Type == "truncate" && entry.Truncate != nil {
			if err := ApplyTruncateToSnapshot(snapshot, *entry.Truncate); err != nil {
				slog.Debug("[Reconstruction] Truncate target not found, skipping",
					"tool_call_id", entry.Truncate.ToolCallID,
					"turn", entry.Truncate.Turn,
					"reason", err.Error(),
				)
				continue
			}
		} else if entry.Type == "compact" && entry.Compact != nil {
			slog.Debug("[Reconstruction] Processing compact event",
				"turn", entry.Compact.Turn,
				"kept_messages", entry.Compact.KeptMessageCount,
				"summary_chars", len(entry.Compact.Summary),
			)
			snapshot.LLMContext = entry.Compact.Summary
			snapshot.RecentMessages = []AgentMessage{}
			// Reset counters on compact (same behavior as during normal execution)
			toolCallsAfterCheckpoint = 0
			turnsAfterCheckpoint = 0
			lastTurn = entry.Compact.Turn
			lastLLMContextUpdate = entry.Compact.Turn
		}
	}

	// Update agent state with recalculated counters from replayed messages
	if toolCallsAfterCheckpoint > 0 || turnsAfterCheckpoint > 0 || lastTurn != snapshot.AgentState.TotalTurns {
		snapshot.AgentState.ToolCallsSinceLastTrigger = toolCallsAfterCheckpoint
		snapshot.AgentState.TurnsSinceLastTrigger = turnsAfterCheckpoint
		snapshot.AgentState.TotalTurns = lastTurn
		snapshot.AgentState.LastLLMContextUpdate = lastLLMContextUpdate

		slog.Debug("[Reconstruction] Updated agent state from replayed messages",
			"tool_calls_since", toolCallsAfterCheckpoint,
			"turns_since", turnsAfterCheckpoint,
			"total_turns", lastTurn,
			"last_llm_update", lastLLMContextUpdate,
		)
	}

	return snapshot, nil
}

// ApplyTruncateToSnapshot marks a message as truncated in the snapshot.
func ApplyTruncateToSnapshot(snapshot *ContextSnapshot, truncateEvent TruncateEvent) error {
	for i := range snapshot.RecentMessages {
		if snapshot.RecentMessages[i].ToolCallID == truncateEvent.ToolCallID {
			msg := &snapshot.RecentMessages[i]
			originalText := msg.ExtractText()

			msg.Truncated = true
			msg.TruncatedAt = truncateEvent.Turn
			if msg.OriginalSize == 0 {
				msg.OriginalSize = len(originalText)
			}

			msg.Content = []ContentBlock{
				TextContent{
					Type: "text",
					Text: TruncateWithHeadTail(originalText),
				},
			}

			return nil
		}
	}

	return fmt.Errorf("message with tool_call_id %s not found", truncateEvent.ToolCallID)
}

// TruncateWithHeadTail preserves the first and last portion of text,
// replacing the middle with a truncation marker.
func TruncateWithHeadTail(text string) string {
	const (
		headKeep      = 400
		tailKeep      = 400
		minSize       = 1200
		smallThreshold = 500
	)

	if len(text) <= smallThreshold {
		return fmt.Sprintf(".. [output truncated, %d chars total] ..", len(text))
	}

	if len(text) <= minSize {
		// For medium-length outputs, preserve the head which typically contains
		// the most important information (grep matches, file headers, error details).
		mediumHead := 300
		if len(text) < mediumHead {
			mediumHead = len(text)
		}
		return fmt.Sprintf("%s\n.. [%d chars truncated] ..", text[:mediumHead], len(text)-mediumHead)
	}

	head := text[:headKeep]
	tail := text[len(text)-tailKeep:]
	truncatedChars := len(text) - headKeep - tailKeep
	return fmt.Sprintf("%s\n.. [%d chars truncated] ..\n%s", head, truncatedChars, tail)
}

// ReconstructSnapshotMessages rebuilds RecentMessages from journal entries starting at startIndex.
func ReconstructSnapshotMessages(snapshot *ContextSnapshot, journalEntries []JournalEntry, startIndex int) {
	for i := startIndex; i < len(journalEntries); i++ {
		entry := journalEntries[i]

		if entry.Type == "message" && entry.Message != nil {
			snapshot.RecentMessages = append(snapshot.RecentMessages, *entry.Message)
		} else if entry.Type == "truncate" && entry.Truncate != nil {
			ApplyTruncateToSnapshot(snapshot, *entry.Truncate)
		} else if entry.Type == "compact" && entry.Compact != nil {
			snapshot.LLMContext = entry.Compact.Summary
			snapshot.RecentMessages = []AgentMessage{}
		}
	}
}