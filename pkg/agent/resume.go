package agent

import (
	"fmt"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// LoadResumeState loads the most up-to-date agent context state from a session
// directory, replaying journal entries written after the latest checkpoint.
//
// This is the single source of truth for resume semantics — used by
// rpcApp.createBaseContext on startup and on session resume.
//
// Parameters:
//   - sessionDir: session directory containing messages.jsonl and checkpoints/.
//     Empty string disables checkpoint-based resume.
//   - fallbackMessages: messages to use if no checkpoint exists (typically
//     sess.GetMessages()).
//
// Returns:
//   - messages: reconstructed RecentMessages (checkpoint snapshot + replayed
//     post-checkpoint journal entries)
//   - llmContext: LLM context from the checkpoint ("" if no checkpoint)
//   - agentState: agent state from the checkpoint (nil if no checkpoint)
//   - err: any I/O or parsing error
//
// If no checkpoint exists, returns (fallbackMessages, "", nil, nil).
func LoadResumeState(sessionDir string, fallbackMessages []agentctx.AgentMessage) (
	messages []agentctx.AgentMessage,
	llmContext string,
	agentState *agentctx.AgentState,
	err error,
) {
	if sessionDir == "" {
		return fallbackMessages, "", nil, nil
	}

	cpInfo, err := agentctx.LoadLatestCheckpoint(sessionDir)
	if err != nil {
		// "no checkpoints found" or missing index file → fallback path.
		// We treat any load error as "no checkpoint available" rather than
		// a fatal error, matching the production resume semantics where a
		// fresh session has no checkpoint yet.
		return fallbackMessages, "", nil, nil
	}
	if cpInfo == nil {
		return fallbackMessages, "", nil, nil
	}

	// Read the journal (session-format messages.jsonl) and replay entries
	// after cpInfo.MessageIndex on top of the checkpoint snapshot. This is
	// the path that was missing in the original rpcApp.createBaseContext
	// implementation: that code used snapshot.RecentMessages directly,
	// silently dropping any messages written after the checkpoint.
	journal, err := agentctx.OpenJournal(sessionDir)
	if err != nil {
		return fallbackMessages, "", nil, fmt.Errorf("open journal: %w", err)
	}
	defer journal.Close()

	entries, err := journal.ReadAll()
	if err != nil {
		return fallbackMessages, "", nil, fmt.Errorf("read journal: %w", err)
	}

	snapshot, err := agentctx.ReconstructSnapshotWithCheckpoint(sessionDir, cpInfo, entries)
	if err != nil {
		return fallbackMessages, "", nil, fmt.Errorf("reconstruct snapshot: %w", err)
	}

	return snapshot.RecentMessages, snapshot.LLMContext, snapshot.AgentState, nil
}
