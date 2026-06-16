package agent

import (
	"fmt"
	"log/slog"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/session"
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
	// Handoff-mode sessions use a separate checkpoint/resume path.
	if session.IsHandoffSession(sessionDir) {
		msgs, agentState, herr := LoadHandoffResumeState(sessionDir)
		return msgs, "", agentState, herr
	}

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

// LoadHandoffResumeState loads the conversation state for a handoff-mode
// session. It reads current.txt to find the active checkpoint, then loads
// messages from that checkpoint's messages.jsonl.
//
// After loading the checkpoint messages, it replays entries from the ROOT
// messages.jsonl that were written AFTER the checkpoint was created (using the
// checkpoint header's timestamp as the cutoff). This ensures post-handoff
// conversation is preserved on resume (P0-1).
//
// It also loads the checkpoint's agent_state.json to restore AgentState
// (CWD, token counts, etc.) if present (P1-3).
//
// If sessionDir is empty or is not a handoff session, returns (nil, nil, nil).
func LoadHandoffResumeState(sessionDir string) (
	messages []agentctx.AgentMessage,
	agentState *agentctx.AgentState,
	err error,
) {
	if sessionDir == "" || !session.IsHandoffSession(sessionDir) {
		return nil, nil, nil
	}

	checkpointName, err := session.GetCurrentCheckpoint(sessionDir)
	if err != nil {
		return nil, nil, fmt.Errorf("read current checkpoint: %w", err)
	}
	if checkpointName == "" {
		return nil, nil, nil
	}

	msgs, err := session.LoadHandoffCheckpointMessages(sessionDir, checkpointName)
	if err != nil {
		return nil, nil, fmt.Errorf("load checkpoint %s messages: %w", checkpointName, err)
	}

	// P0-1: Replay root messages.jsonl entries written AFTER the checkpoint
	// was created. The session writer writes ALL messages to the root
	// messages.jsonl, so post-handoff conversation lives there and must be
	// picked up on resume.
	if header, herr := session.ReadHandoffCheckpointHeader(sessionDir, checkpointName); herr != nil {
		slog.Warn("[Handoff] Failed to read checkpoint header, skipping root journal replay",
			"checkpoint", checkpointName, "error", herr)
	} else {
		rootMsgs, rerr := session.ReadRootMessagesAfter(sessionDir, header.Timestamp)
		if rerr != nil {
			slog.Warn("[Handoff] Failed to read root journal for replay", "error", rerr)
		} else if len(rootMsgs) > 0 {
			msgs = append(msgs, rootMsgs...)
			slog.Info("[Handoff] Replayed root journal messages after checkpoint",
				"checkpoint", checkpointName,
				"checkpoint_messages", len(msgs)-len(rootMsgs),
				"replayed", len(rootMsgs))
		}
	}

	// P1-3: Load AgentState from the checkpoint if it exists.
	agentState, _ = session.LoadHandoffCheckpointAgentState(sessionDir, checkpointName)
	if agentState != nil {
		slog.Info("[Handoff] Restored agent state from checkpoint",
			"checkpoint", checkpointName,
			"turns", agentState.TotalTurns,
			"tokens", agentState.TokensUsed,
			"cwd", agentState.CurrentWorkingDir)
	}

	return msgs, agentState, nil
}
