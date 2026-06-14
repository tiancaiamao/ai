package compact

import (
	"context"
	"log/slog"
	"strings"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	traceevent "github.com/tiancaiamao/ai/pkg/traceevent"
)

func compactToolResultsInRecent(messages []agentctx.AgentMessage, cutoff int) []agentctx.AgentMessage {
	if cutoff <= 0 || len(messages) == 0 {
		return messages
	}

	visibleToolIndexes := make([]int, 0)
	for i, msg := range messages {
		if msg.Role == "toolResult" && msg.IsAgentVisible() {
			visibleToolIndexes = append(visibleToolIndexes, i)
		}
	}

	excess := len(visibleToolIndexes) - cutoff
	if excess <= 0 {
		return messages
	}
	ctx := context.Background()
	summarySpan := traceevent.StartSpan(ctx, "tool_summary_batch", traceevent.CategoryTool,
		traceevent.Field{Key: "mode", Value: "compaction_digest"},
		traceevent.Field{Key: "visible_tool_results", Value: len(visibleToolIndexes)},
		traceevent.Field{Key: "cutoff", Value: cutoff},
		traceevent.Field{Key: "archived_count", Value: excess},
	)

	compacted := append([]agentctx.AgentMessage{}, messages...)
	archivedToolCallIDs := make(map[string]struct{}, excess)

	// Hide excess tool_results from agent (but keep visible to user).
	// We also remove corresponding tool_calls from assistant messages below,
	// otherwise strict APIs reject unmatched assistant/tool sequences.
	for i := 0; i < excess; i++ {
		idx := visibleToolIndexes[i]
		original := compacted[idx]
		compacted[idx] = original.WithVisibility(false, original.IsUserVisible()).WithKind("tool_result_archived")
		if strings.TrimSpace(original.ToolCallID) != "" {
			archivedToolCallIDs[strings.TrimSpace(original.ToolCallID)] = struct{}{}
		}
	}

	filteredToolCalls := 0
	for i := range compacted {
		if compacted[i].Role != "assistant" || len(archivedToolCallIDs) == 0 {
			continue
		}
		filtered := make([]agentctx.ContentBlock, 0, len(compacted[i].Content))
		removed := false
		for _, block := range compacted[i].Content {
			toolCall, ok := block.(agentctx.ToolCallContent)
			if ok {
				if _, drop := archivedToolCallIDs[strings.TrimSpace(toolCall.ID)]; drop {
					removed = true
					filteredToolCalls++
					continue
				}
			}
			filtered = append(filtered, block)
		}
		if removed {
			compacted[i].Content = filtered
		}
	}

	summarySpan.AddField("filtered_tool_calls", filteredToolCalls)
	summarySpan.End()
	return compacted
}

func trimTextWithTail(input string, maxRunes int) string {
	if maxRunes <= 0 {
		return input
	}
	runes := []rune(input)
	if len(runes) <= maxRunes {
		return input
	}

	head := maxRunes * 2 / 3
	tail := maxRunes - head
	if head < 1 {
		head = 1
	}
	if tail < 1 {
		tail = 1
	}

	return string(runes[:head]) + "\n... (truncated) ...\n" + string(runes[len(runes)-tail:])
}

// ensureToolCallPairing ensures that tool_call and tool_result messages remain paired.
// If a tool_result is in recentMessages but its corresponding tool_call is in oldMessages,
// the tool_result must be hidden (archived) so the API doesn't see a mismatch.
// Similarly, if an assistant message contains tool_calls that are in oldMessages,
// those tool_calls must be removed from the assistant message.
// This prevents "tool call and result not match" errors after compaction.

// ensureToolCallPairing ensures that tool_call and tool_result messages remain paired.
// If a tool_result is in recentMessages but its corresponding tool_call is in oldMessages,
// the tool_result must be hidden (archived) so the API doesn't see a mismatch.
// Similarly, if an assistant message contains tool_calls that are in oldMessages,
// those tool_calls must be removed from the assistant message.
// This prevents "tool call and result not match" errors after compaction.
func ensureToolCallPairing(oldMessages, recentMessages []agentctx.AgentMessage) []agentctx.AgentMessage {
	if len(recentMessages) == 0 {
		return recentMessages
	}

	// Collect all tool_call IDs from oldMessages
	oldToolCallIDs := make(map[string]bool)
	for _, msg := range oldMessages {
		if msg.Role == "assistant" {
			for _, tc := range msg.ExtractToolCalls() {
				oldToolCallIDs[tc.ID] = true
			}
		}
	}

	// If no tool_calls in oldMessages, nothing to fix
	if len(oldToolCallIDs) == 0 {
		return recentMessages
	}

	// Find tool_results in recentMessages whose tool_call is in oldMessages
	// These need to be hidden (archived) because their tool_calls will be summarized
	keptMessages := make([]agentctx.AgentMessage, 0, len(recentMessages))
	archivedToolResultCount := 0
	filteredToolCallCount := 0

	for _, msg := range recentMessages {
		if msg.Role == "toolResult" && msg.ToolCallID != "" {
			if oldToolCallIDs[msg.ToolCallID] {
				// This tool_result's call is in oldMessages - hide it to prevent mismatch
				archivedMsg := msg.WithVisibility(false, msg.IsUserVisible()).WithKind("tool_result_archived")
				keptMessages = append(keptMessages, archivedMsg)
				archivedToolResultCount++
				continue
			}
		}

		if msg.Role == "assistant" {
			// Check if this assistant message contains tool_calls that are in oldMessages
			filteredContent := make([]agentctx.ContentBlock, 0, len(msg.Content))
			hasOldToolCalls := false

			for _, block := range msg.Content {
				if tc, ok := block.(agentctx.ToolCallContent); ok {
					if oldToolCallIDs[tc.ID] {
						// This tool_call is in oldMessages - skip it
						hasOldToolCalls = true
						filteredToolCallCount++
						continue
					}
				}
				filteredContent = append(filteredContent, block)
			}

			if hasOldToolCalls {
				if len(filteredContent) == 0 {
					// Empty shell! Hide the entire assistant message
					keptMessages = append(keptMessages, msg.WithVisibility(false, msg.IsUserVisible()))
					continue
				}
				// Create a new message with filtered content
				filteredMsg := msg
				filteredMsg.Content = filteredContent
				keptMessages = append(keptMessages, filteredMsg)
				continue
			}
		}

		keptMessages = append(keptMessages, msg)
	}

	if archivedToolResultCount > 0 || filteredToolCallCount > 0 {
		slog.Info("[Compact] Fixed tool_call/tool_result pairing",
			"archived_tool_results", archivedToolResultCount,
			"filtered_tool_calls", filteredToolCallCount,
			"kept", len(keptMessages))
	}

	return keptMessages
}

// ensureToolCallPairingWithGrace ensures tool call pairing with grace period protection.
// The grace period protects the N most recent tool results from being archived,
// allowing tool calls that span compaction boundaries to complete.

// ensureToolCallPairingWithGrace ensures tool call pairing with grace period protection.
// The grace period protects the N most recent tool results from being archived,
// allowing tool calls that span compaction boundaries to complete.
func (c *Compactor) ensureToolCallPairingWithGrace(oldMessages, recentMessages []agentctx.AgentMessage) []agentctx.AgentMessage {
	if len(recentMessages) == 0 {
		return recentMessages
	}

	// Collect all tool_call IDs from oldMessages
	oldToolCallIDs := make(map[string]bool)
	for _, msg := range oldMessages {
		if msg.Role == "assistant" {
			for _, tc := range msg.ExtractToolCalls() {
				oldToolCallIDs[tc.ID] = true
			}
		}
	}

	// If no tool_calls in oldMessages, nothing to fix
	if len(oldToolCallIDs) == 0 {
		return recentMessages
	}

	// Collect recent tool result indexes for grace period protection
	gracePeriod := c.config.GracePeriod
	if gracePeriod <= 0 {
		gracePeriod = 1
	}

	// Find tool result indexes (from end to start) within grace period
	gracePeriodIndexes := make(map[int]struct{})
	toolResultCount := 0
	for i := len(recentMessages) - 1; i >= 0; i-- {
		msg := recentMessages[i]
		if msg.Role == "toolResult" && msg.IsAgentVisible() {
			toolResultCount++
			if toolResultCount <= gracePeriod {
				gracePeriodIndexes[i] = struct{}{}
			}
		}
	}

	// Process messages, applying grace period protection
	keptMessages := make([]agentctx.AgentMessage, 0, len(recentMessages))
	archivedToolResultCount := 0
	filteredToolCallCount := 0

	for i, msg := range recentMessages {
		// Check if this tool result is within grace period
		if _, inGracePeriod := gracePeriodIndexes[i]; inGracePeriod {
			// Within grace period - keep it visible (don't archive)
			keptMessages = append(keptMessages, msg)
			continue
		}

		if msg.Role == "toolResult" && msg.ToolCallID != "" {
			if oldToolCallIDs[msg.ToolCallID] {
				// This tool_result's call is in oldMessages - hide it to prevent mismatch
				archivedMsg := msg.WithVisibility(false, msg.IsUserVisible()).WithKind("tool_result_archived")
				keptMessages = append(keptMessages, archivedMsg)
				archivedToolResultCount++
				continue
			}
		}

		if msg.Role == "assistant" {
			// Check if this assistant message contains tool_calls that are in oldMessages
			filteredContent := make([]agentctx.ContentBlock, 0, len(msg.Content))
			hasOldToolCalls := false

			for _, block := range msg.Content {
				if tc, ok := block.(agentctx.ToolCallContent); ok {
					if oldToolCallIDs[tc.ID] {
						// This tool_call is in oldMessages - skip it
						hasOldToolCalls = true
						filteredToolCallCount++
						continue
					}
				}
				filteredContent = append(filteredContent, block)
			}

			if hasOldToolCalls {
				if len(filteredContent) == 0 {
					// Empty shell! Hide the entire assistant message
					keptMessages = append(keptMessages, msg.WithVisibility(false, msg.IsUserVisible()))
					continue
				}
				// Create a new message with filtered content
				filteredMsg := msg
				filteredMsg.Content = filteredContent
				keptMessages = append(keptMessages, filteredMsg)
				continue
			}
		}

		keptMessages = append(keptMessages, msg)
	}

	if archivedToolResultCount > 0 || filteredToolCallCount > 0 {
		slog.Info("[Compact] Fixed tool_call/tool_result pairing",
			"archived_tool_results", archivedToolResultCount,
			"filtered_tool_calls", filteredToolCallCount,
			"grace_period_protected", gracePeriod,
			"kept", len(keptMessages))
	}

	return keptMessages
}

// Compact compacts context by summarizing old messages using AgentContext.
// This method implements the context.Compactor interface.
