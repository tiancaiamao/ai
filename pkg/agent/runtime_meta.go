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
	// Block A: LLMContext content injection — whenever non-empty.
	var llmContextContent string
	if agentCtx.LLMContext != "" {
		llmContextContent = agentCtx.LLMContext
	}

	// Block B: runtime_state telemetry — always, from turn 1.
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

	meta := agentctx.ContextMeta{
		TokensUsed:        metaTokensUsed,
		TokensMax:         metaTokensMax,
		TokensPercent:     metaTokensPercent,
		MessagesInHistory: len(agentCtx.RecentMessages),
		LLMContextSize:    len(agentCtx.LLMContext),
	}

	runtimeMetaSnapshot, _ := updateRuntimeMetaSnapshot(agentCtx, meta, defaultRuntimeMetaHeartbeatTurns, currentWorkdir, startupPath)
	return buildRuntimeUserAppendix(llmContextContent, runtimeMetaSnapshot)
}


func extractRecentMessages(messages []agentctx.AgentMessage, tokenBudget int) []agentctx.AgentMessage {
	if len(messages) == 0 {
		return messages
	}

	// First, filter to only agent-visible messages
	visible := make([]agentctx.AgentMessage, 0, len(messages))
	for _, msg := range messages {
		if msg.IsAgentVisible() {
			visible = append(visible, msg)
		}
	}

	if len(visible) == 0 {
		return nil
	}

	// If budget is 0 or negative, return last message only
	if tokenBudget <= 0 {
		return visible[len(visible)-1:]
	}

	// Count tokens from the end, keeping messages within budget
	used := 0
	start := len(visible)

	for i := len(visible) - 1; i >= 0; i-- {
		msgTokens := agentctx.EstimateMessageTokens(visible[i])
		if used+msgTokens > tokenBudget && start != len(visible) {
			break
		}
		used += msgTokens
		start = i
	}

	if start >= len(visible) {
		return visible
	}

	result := visible[start:]

	// Skip leading tool/toolResult messages to ensure valid message sequence.
	// agentctx.Tool messages must follow an assistant message with tool_calls.
	// If we truncated in the middle of a tool call sequence, drop the orphaned tool results.
	for len(result) > 0 && (result[0].Role == "tool" || result[0].Role == "toolResult") {
		result = result[1:]
	}

	return result
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

// extractActiveTurnMessages returns only messages in the active turn window.
// The window starts from the most recent agent-visible user message so prior
// history is excluded while current tool-call protocol context is preserved.
func extractActiveTurnMessages(messages []agentctx.AgentMessage, tokenBudget int) []agentctx.AgentMessage {
	if len(messages) == 0 {
		return nil
	}

	visible := make([]agentctx.AgentMessage, 0, len(messages))
	for _, msg := range messages {
		if msg.IsAgentVisible() {
			visible = append(visible, msg)
		}
	}
	if len(visible) == 0 {
		return nil
	}

	start := len(visible) - 1
	for i := len(visible) - 1; i >= 0; i-- {
		if strings.EqualFold(visible[i].Role, "user") {
			start = i
			break
		}
	}

	active := visible[start:]
	if tokenBudget <= 0 {
		return active
	}
	return extractRecentMessages(active, tokenBudget)
}

func runtimeYAMLString(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		trimmed = "unknown"
	}
	return strconv.Quote(trimmed)
}

type runtimeToolPressure struct {
	StaleCount   int
	LargeCount   int
	LargestChars int
}

func collectRuntimeToolPressure(messages []agentctx.AgentMessage) runtimeToolPressure {
	pressure := runtimeToolPressure{}
	if len(messages) == 0 {
		return pressure
	}

	staleCount, _ := collectStaleToolOutputStats(messages, recentToolResultsNoMetadata)
	pressure.StaleCount = staleCount

	const largeOutputThresholdChars = 2000
	for _, msg := range messages {
		if !msg.IsAgentVisible() || msg.Role != "toolResult" {
			continue
		}

		size := len(msg.ExtractText())
		if size > pressure.LargestChars {
			pressure.LargestChars = size
		}
		if size >= largeOutputThresholdChars {
			pressure.LargeCount++
		}
	}

	return pressure
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

func runtimeMessageBucket(count int) string {
	switch {
	case count <= 0:
		return "0"
	case count <= 10:
		return "1-10"
	case count <= 25:
		return "11-25"
	case count <= 50:
		return "26-50"
	case count <= 100:
		return "51-100"
	default:
		return "100+"
	}
}

func runtimeSizeBucket(size int) string {
	switch {
	case size <= 0:
		return "0"
	case size <= 1024:
		return "0-1KB"
	case size <= 4*1024:
		return "1-4KB"
	case size <= 16*1024:
		return "4-16KB"
	case size <= 64*1024:
		return "16-64KB"
	default:
		return "64KB+"
	}
}

func runtimeCountBucket(count int) string {
	switch {
	case count <= 0:
		return "0"
	case count <= 2:
		return "1-2"
	case count <= 5:
		return "3-5"
	case count <= 10:
		return "6-10"
	default:
		return "10+"
	}
}

func runtimeToolOutputSizeBucket(chars int) string {
	switch {
	case chars <= 0:
		return "0"
	case chars <= 512:
		return "1-512c"
	case chars <= 2048:
		return "513-2Kc"
	case chars <= 8192:
		return "2K-8Kc"
	default:
		return "8Kc+"
	}
}

func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func normalizeApprox(value int) int {
	if value <= 0 {
		return 0
	}
	if value < 1000 {
		return value
	}
	return (value / 1000) * 1000
}
