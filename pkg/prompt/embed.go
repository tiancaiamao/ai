package prompt

import _ "embed"

//go:embed "subagent.md"
var DefaultSubagentPrompt string

// CompactorBasePrompt returns the baseline prompt used by compactor requests.
func CompactorBasePrompt() string {
	return "You are a helpful coding assistant."
}

//go:embed "base.md"
var basePrompt string

// RPCBasePrompt returns the base system prompt for interactive RPC mode.
func RPCBasePrompt() string {
	return basePrompt
}

//go:embed "subagent_base.md"
var subagentBasePrompt string

//go:embed "headless_base.md"
var headlessBasePrompt string

//go:embed "task_tracking.md"
var taskTrackingPrompt string

//go:embed "context_management.md"
var contextManagementPrompt string

//go:embed "compact_system.md"
var compactSystemPrompt string

//go:embed "compact_summarize.md"
var compactSummarizePrompt string

//go:embed "compact_update.md"
var compactUpdatePrompt string

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

// HeadlessBasePrompt returns the base system prompt for headless mode.
func HeadlessBasePrompt(isSubagent bool) string {
	if isSubagent {
		return subagentBasePrompt
	}

	return headlessBasePrompt
}

// JSONModeBasePrompt returns the base system prompt for JSON mode.
func JSONModeBasePrompt() string {
	return HeadlessBasePrompt(false)
}
