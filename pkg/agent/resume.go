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
//   - messages: reconstructed RecentMessages (checkpoint + replayed post-checkpoint entries)
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

	snapshot, err := agentctx.LoadCheckpoint(sessionDir, cpInfo)
	if err != nil {
		return fallbackMessages, "", nil, fmt.Errorf("load checkpoint: %w", err)
	}

	// TEMPORARY (buggy) implementation — matches current rpc_app.createBaseContext
	// behavior. Will be replaced by proper Reconstruct() in the fix.
	if len(snapshot.RecentMessages) > 0 {
		return snapshot.RecentMessages, snapshot.LLMContext, snapshot.AgentState, nil
	}

	// No RecentMessages in checkpoint — use fallback.
	return fallbackMessages, "", snapshot.AgentState, nil
}