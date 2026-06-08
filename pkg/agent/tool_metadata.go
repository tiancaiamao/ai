package agent

import (
	"strings"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

const (
	recentToolResultsNoMetadata = 10
	toolOutputSummaryTypeLimit  = 30
)

func hasAgentToolMetadataTag(text string) bool {
	return strings.Contains(text, "<agent:tool")
}

func isTruncatedAgentToolTag(text string) bool {
	return hasAgentToolMetadataTag(text) && strings.Contains(text, `truncated="true"`)
}

func findLastVisibleUserIndex(messages []agentctx.AgentMessage) int {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if !msg.IsAgentVisible() {
			continue
		}
		if strings.EqualFold(msg.Role, "user") {
			return i
		}
	}
	return -1
}

func protectedRecentToolResultIndexes(messages []agentctx.AgentMessage, keepRecent int) map[int]struct{} {
	if keepRecent <= 0 {
		return nil
	}
	protected := make(map[int]struct{}, keepRecent)
	count := 0
	for i := len(messages) - 1; i >= 0 && count < keepRecent; i-- {
		msg := messages[i]
		if !msg.IsAgentVisible() || msg.Role != "toolResult" {
			continue
		}
		protected[i] = struct{}{}
		count++
	}
	return protected
}

func collectStaleToolOutputStats(messages []agentctx.AgentMessage, keepRecent int) (int, map[string]int) {
	lastUserIndex := findLastVisibleUserIndex(messages)
	if lastUserIndex < 0 {
		return 0, map[string]int{}
	}

	protected := protectedRecentToolResultIndexes(messages, keepRecent)

	// Also protect the latest task_tracking from being marked as stale
	// This ensures stale count matches what can actually be truncated
	latestLLMContextUpdate := findLatestToolCallID(messages, "task_tracking")

	byTool := make(map[string]int)
	staleCount := 0

	for i, msg := range messages {
		if !msg.IsAgentVisible() || msg.Role != "toolResult" {
			continue
		}
		if i >= lastUserIndex {
			continue
		}
		if _, ok := protected[i]; ok {
			continue
		}
		if isTruncatedAgentToolTag(msg.ExtractText()) {
			continue
		}
		// Skip the latest task_tracking - it's needed for progress tracking
		if latestLLMContextUpdate != "" && msg.ToolCallID == latestLLMContextUpdate {
			continue
		}

		name := strings.TrimSpace(msg.ToolName)
		if name == "" {
			name = "unknown"
		}
		byTool[name]++
		staleCount++
	}

	return staleCount, byTool
}

// findLatestToolCallID finds the most recent tool call ID for a given tool name.
// Returns empty string if not found.
func findLatestToolCallID(messages []agentctx.AgentMessage, toolName string) string {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role != "toolResult" {
			continue
		}
		if msg.ToolName == toolName {
			return msg.ToolCallID
		}
	}
	return ""
}
