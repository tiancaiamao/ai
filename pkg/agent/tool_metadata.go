package agent

import (
	"fmt"
	"sort"
	"strconv"
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

func parseCharsFromAgentToolTag(text string) (int, bool) {
	const prefix = `chars="`
	start := strings.Index(text, prefix)
	if start < 0 {
		return 0, false
	}
	start += len(prefix)
	end := strings.Index(text[start:], `"`)
	if end <= 0 {
		return 0, false
	}
	value := text[start : start+end]
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
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
	
	// Also protect the latest llm_context_update from being marked as stale
	// This ensures stale count matches what can actually be truncated
	latestLLMContextUpdate := findLatestToolCallID(messages, "llm_context_update")
	
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
		// Skip the latest llm_context_update - it's needed for progress tracking
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

func buildToolOutputsSummary(messages []agentctx.AgentMessage) string {
	staleCount, byTool := collectStaleToolOutputStats(messages, recentToolResultsNoMetadata)
	if staleCount == 0 || len(byTool) == 0 {
		return "none"
	}

	type pair struct {
		name  string
		count int
	}
	pairs := make([]pair, 0, len(byTool))
	for name, count := range byTool {
		pairs = append(pairs, pair{name: name, count: count})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].count == pairs[j].count {
			return pairs[i].name < pairs[j].name
		}
		return pairs[i].count > pairs[j].count
	})

	limit := toolOutputSummaryTypeLimit
	if len(pairs) < limit {
		limit = len(pairs)
	}
	parts := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		parts = append(parts, fmt.Sprintf("%d %s", pairs[i].count, pairs[i].name))
	}
	if len(pairs) > limit {
		parts = append(parts, "...")
	}

	return fmt.Sprintf("%d stale outputs (%s), consider TRUNCATE", staleCount, strings.Join(parts, ", "))
}

// buildToolOutputsSummaryWithIDs returns summary and list of tool call IDs that can be truncated.
// It excludes already truncated tool outputs and the latest llm_context_update.
func buildToolOutputsSummaryWithIDs(messages []agentctx.AgentMessage) (string, []string) {
	staleCount, byTool := collectStaleToolOutputStats(messages, recentToolResultsNoMetadata)
	if staleCount == 0 || len(byTool) == 0 {
		return "none", nil
	}

	// Collect tool call IDs for each tool type
	toolIDsByTool := make(map[string][]string)
	lastUserIndex := findLastVisibleUserIndex(messages)
	protected := protectedRecentToolResultIndexes(messages, recentToolResultsNoMetadata)
	
	// Also protect the latest llm_context_update
	latestLLMContextUpdate := findLatestToolCallID(messages, "llm_context_update")

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
		// Skip the latest llm_context_update - it's needed for progress tracking
		if latestLLMContextUpdate != "" && msg.ToolCallID == latestLLMContextUpdate {
			continue
		}

		name := strings.TrimSpace(msg.ToolName)
		if name == "" {
			name = "unknown"
		}
		toolIDsByTool[name] = append(toolIDsByTool[name], msg.ToolCallID)
	}

	// Build summary text
	type pair struct {
		name  string
		count int
	}
	pairs := make([]pair, 0, len(byTool))
	for name, count := range byTool {
		pairs = append(pairs, pair{name: name, count: count})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].count == pairs[j].count {
			return pairs[i].name < pairs[j].name
		}
		return pairs[i].count > pairs[j].count
	})

	limit := toolOutputSummaryTypeLimit
	if len(pairs) < limit {
		limit = len(pairs)
	}
	parts := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		parts = append(parts, fmt.Sprintf("%d %s", pairs[i].count, pairs[i].name))
	}
	if len(pairs) > limit {
		parts = append(parts, "...")
	}

	// Flatten all tool call IDs
	var allIDs []string
	for _, ids := range toolIDsByTool {
		allIDs = append(allIDs, ids...)
	}

	summary := fmt.Sprintf("%d stale outputs (%s), consider TRUNCATE", staleCount, strings.Join(parts, ", "))
	return summary, allIDs
}
