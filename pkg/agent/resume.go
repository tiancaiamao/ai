package agent

import (
	"path/filepath"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// LoadResumeState loads the agent state from the latest checkpoint.
//
// In the Proposal B design, messages are the sole responsibility of
// sess.GetMessages() (which handles compaction snapshot refs internally).
// This function only restores AgentState from the latest checkpoint.
//
// Parameters:
//   - sessionDir: session directory containing checkpoints/.
//     Empty string disables checkpoint-based resume.
//   - fallbackMessages: messages already loaded from sess.GetMessages().
//     Returned unchanged — they are the authoritative message list.
//
// Returns:
//   - messages: the same fallbackMessages (session is source of truth)
//   - llmContext: "" (no longer restored from checkpoint)
//   - agentState: agent state from the latest checkpoint (nil if none)
//   - err: any I/O or parsing error
func LoadResumeState(sessionDir string, fallbackMessages []agentctx.AgentMessage) (
	messages []agentctx.AgentMessage,
	agentState *agentctx.AgentState,
	err error,
) {
	if sessionDir == "" {
		return fallbackMessages, nil, nil
	}

	cpInfo, err := agentctx.LoadLatestCheckpoint(sessionDir)
	if err != nil || cpInfo == nil {
		return fallbackMessages, nil, nil
	}

	// Load AgentState from checkpoint (for turn count, tokens, CWD, etc.)
	cpPath := filepath.Join(sessionDir, cpInfo.Path)
	agentState, err = agentctx.LoadCheckpointAgentState(cpPath)
	if err != nil {
		return fallbackMessages, nil, nil
	}

	return fallbackMessages, agentState, nil
}
