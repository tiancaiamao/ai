package agent

import (
	"strconv"
	"strings"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// injectRuntimeMeta computes runtime telemetry from the current agent state,
// updates agentCtx.AgentState with token/path info, refreshes the meta snapshot
// if needed, and returns the combined runtime appendix string (LLM context +
// runtime_state YAML). It is read-only with respect to loop state — it only
// writes to agentCtx metadata fields.
func injectRuntimeMeta(agentCtx *agentctx.AgentContext, config *LoopConfig) string {
	// runtime_state telemetry — always, from turn 1.
	const defaultContextWindow = 200000 // matches internal/winai/interpreter.go default
	tokensMax := defaultContextWindow
	if config.ContextWindow > 0 {
		tokensMax = config.ContextWindow
	}
	tokensUsedApprox := EstimateConversationTokens(agentCtx.RecentMessages)

	// Update AgentState with token usage info
	agentCtx.AgentState.TokensUsed = tokensUsedApprox
	agentCtx.AgentState.TokensLimit = tokensMax
	agentCtx.AgentState.TotalTurns = len(agentCtx.RecentMessages)

	// Update CWD in AgentState so checkpoints preserve it for session restore
	if config.GetWorkingDir != nil {
		agentCtx.AgentState.CurrentWorkingDir = config.GetWorkingDir()
	}
	if config.GetStartupPath != nil {
		agentCtx.AgentState.WorkspaceRoot = config.GetStartupPath()
	}

	currentWorkdir := agentCtx.AgentState.CurrentWorkingDir
	startupPath := ""
	if config.GetStartupPath != nil {
		startupPath = config.GetStartupPath()
	}

	// Build meta for runtime snapshot from AgentState
	metaTokensUsed := agentCtx.AgentState.TokensUsed
	if metaTokensUsed == 0 {
		metaTokensUsed = tokensUsedApprox
	}
	metaTokensMax := agentCtx.AgentState.TokensLimit
	if metaTokensMax == 0 {
		metaTokensMax = tokensMax
	}
	metaTokensPercent := float64(0)
	if metaTokensMax > 0 {
		metaTokensPercent = float64(metaTokensUsed) / float64(metaTokensMax) * 100
	}

	meta := ContextMeta{
		TokensUsed:        metaTokensUsed,
		TokensMax:         metaTokensMax,
		TokensPercent:     metaTokensPercent,
		MessagesInHistory: len(agentCtx.RecentMessages),
	}

	runtimeMetaSnapshot := updateRuntimeMetaSnapshot(agentCtx, meta, defaultRuntimeMetaHeartbeatTurns, currentWorkdir, startupPath, config.RunID, config.Role)
	return buildRuntimeUserAppendix(runtimeMetaSnapshot)
}

// EstimateConversationTokens estimates token count for messages.
// EstimateConversationTokens estimates total tokens for a slice of messages.
func EstimateConversationTokens(messages []agentctx.AgentMessage) int {
	total := 0
	for _, msg := range messages {
		total += agentctx.EstimateMessageTokens(msg)
	}
	return total
}

// runtimeStateKind marks persisted runtime_state messages in message
// metadata. Display layers (TUI, ACP replay) skip them; the loop uses them
// to detect whether the frozen snapshot needs refreshing.
const runtimeStateKind = "runtimeState"

// runtimeStateTurnMessage computes the runtime_state snapshot for this turn
// and returns it as a new persisted user message when it differs from the
// last frozen snapshot already in history.
//
// Unlike the old ephemeral re-injection (a fresh message inserted before the
// last user message on every LLM call), the snapshot is frozen into the
// conversation at the turn boundary: state changes append new messages
// instead of rewriting history, so every request stays a pure tail append of
// the previous one and provider prefix caches keep hitting (see
// llm_prefix_cache.go).
func runtimeStateTurnMessage(agentCtx *agentctx.AgentContext, config *LoopConfig) *agentctx.AgentMessage {
	appendix := injectRuntimeMeta(agentCtx, config)
	if strings.TrimSpace(appendix) == "" {
		return nil
	}
	if lastRuntimeStateMessage(agentCtx) == appendix {
		return nil
	}
	msg := agentctx.NewUserMessage(appendix).WithKind(runtimeStateKind)
	return &msg
}

// lastRuntimeStateMessage returns the text of the most recent persisted
// runtime_state message in history, or "" when none exists (first turn, or
// the snapshot was dropped by compaction).
func lastRuntimeStateMessage(agentCtx *agentctx.AgentContext) string {
	if agentCtx == nil {
		return ""
	}
	for i := len(agentCtx.RecentMessages) - 1; i >= 0; i-- {
		msg := &agentCtx.RecentMessages[i]
		if msg.Metadata != nil && msg.Metadata.Kind == runtimeStateKind {
			return msg.ExtractText()
		}
	}
	return ""
}

func runtimeYAMLString(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		trimmed = "unknown"
	}
	return strconv.Quote(trimmed)
}

// ContextMeta holds telemetry values for runtime state snapshot.
type ContextMeta struct {
	TokensUsed        int     `json:"tokens_used"`
	TokensMax         int     `json:"tokens_max"`
	TokensPercent     float64 `json:"tokens_percent"`
	MessagesInHistory int     `json:"messages_in_history"`
}

func runtimeTokenBand(percent float64) string {
	switch {
	case percent < 20:
		return "0-20"
	case percent < 40:
		return "20-40"
	case percent < 60:
		return "40-60"
	case percent < 75:
		return "60-75"
	default:
		return "75+"
	}
}
