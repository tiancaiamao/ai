package agent

import (
	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// LoadResumeState loads the agent state from agent_state.json in the session directory.
//
// Messages are the sole responsibility of sess.GetMessages() (which handles
// compaction snapshot refs internally). This function only restores AgentState.
//
// Parameters:
//   - sessionDir: session directory containing agent_state.json.
//     Empty string disables checkpoint-based resume.
//   - fallbackMessages: messages already loaded from sess.GetMessages().
//     Returned unchanged — they are the authoritative message list.
//
// Returns:
//   - messages: the same fallbackMessages (session is source of truth)
//   - agentState: agent state from agent_state.json (nil if none)
//   - err: any I/O or parsing error
func LoadResumeState(sessionDir string, fallbackMessages []agentctx.AgentMessage) (
	messages []agentctx.AgentMessage,
	agentState *agentctx.AgentState,
	err error,
) {
	if sessionDir == "" {
		return fallbackMessages, nil, nil
	}

	agentState, err = agentctx.LoadAgentState(sessionDir)
	if err != nil {
		return fallbackMessages, nil, nil
	}

	return fallbackMessages, agentState, nil
}
