package prompt

import (
	_ "embed"
	"strings"
)

//go:embed "subagent_base.md"
var subagentBasePrompt string

//go:embed "headless_base.md"
var headlessBasePrompt string

//go:embed "compact_system.md"
var compactSystemPrompt string

//go:embed "compact_summarize.md"
var compactSummarizePrompt string

//go:embed "compact_update.md"
var compactUpdatePrompt string

// Replay note: context management validation — single-line change for replay stress test.
// CompactorBasePrompt returns the baseline prompt used by compactor requests.
func CompactorBasePrompt() string {
	return "You are a helpful coding assistant."
}

// HeadlessBasePrompt returns the base system prompt for headless mode.
func HeadlessBasePrompt(isSubagent bool) string {
	if isSubagent {
		return subagentBasePrompt
	}
	return headlessBasePrompt
}

// RPCBasePrompt returns the base system prompt for RPC mode.
func RPCBasePrompt() string {
	return headlessBasePrompt
}

// JSONModeBasePrompt returns the base system prompt for JSON mode.
func JSONModeBasePrompt() string {
	return HeadlessBasePrompt(false)
}

// CompactSystemPrompt returns the system prompt for compaction.
func CompactSystemPrompt() string {
	return compactSystemPrompt
}

// CompactSummarizePrompt returns the prompt for initial summarization.
func CompactSummarizePrompt() string {
	return compactSummarizePrompt
}

// CompactUpdatePrompt returns the prompt for updating existing summary.
func CompactUpdatePrompt() string {
	return compactUpdatePrompt
}

// ThinkingInstruction returns the thinking instruction for the given level.
func ThinkingInstruction(level string) string {
	level = NormalizeThinkingLevel(level)
	switch level {
	case "off":
		return "Thinking level is off. Do not emit reasoning/thinking content. Respond directly with concise results and tool calls when needed."
	case "minimal":
		return "Thinking level is minimal. Keep reasoning very brief and only include what is strictly necessary."
	case "low":
		return "Thinking level is low. Keep reasoning concise and focused."
	case "medium":
		return "Thinking level is medium. Use balanced reasoning depth."
	case "high":
		return "Thinking level is high. Use thorough reasoning where needed."
	case "xhigh":
		return "Thinking level is xhigh. Use very thorough reasoning before final answers and tool calls."
	default:
		return ""
	}
}

// NormalizeThinkingLevel normalizes the thinking level string.
func NormalizeThinkingLevel(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "off", "minimal", "low", "medium", "high", "xhigh":
		return strings.ToLower(strings.TrimSpace(level))
	case "":
		return "high"
	default:
		return "high"
	}
}
