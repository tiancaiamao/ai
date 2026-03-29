package tools

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/traceevent"
)

// ContextManagementTool allows LLM to declare context management decisions.
type ContextManagementTool struct {
	compactor agentctx.Compactor
}

// NewContextManagementTool creates a new context_management tool.
func NewContextManagementTool(compactor agentctx.Compactor) *ContextManagementTool {
	return &ContextManagementTool{
		compactor: compactor,
	}
}

// Name returns the tool name.
func (t *ContextManagementTool) Name() string {
	return "context_management"
}

// Description returns tool description.
func (t *ContextManagementTool) Description() string {
	return `Declare your context management decision. Call this tool when you need to manage context.

IMPORTANT: This tool is for CONTEXT MANAGEMENT only. Use it to:
- TRUNCATE: Remove old/large tool outputs to free up space
- COMPACT: Summarize conversation history to reduce context size
- SKIP: Defer context management for a specified number of turns

USAGE:
When runtime_state shows high context pressure (tokens_percent >= 30% with stale outputs, or >= 50% overall), you SHOULD call this tool proactively.

DECISION OPTIONS:
- "truncate": Remove specific tool outputs (provide truncate_ids)
- "compact": Run compaction to summarize history (provide compact_confidence)
- "skip": Defer for skip_turns (1-30, higher = you promise to be proactive)

PARAMETERS:
- decision (required): "truncate", "compact", or "skip"
- reasoning (required): Explain WHY you made this decision (also accepts "reason")
- skip_turns (optional, for decision="skip"): How many turns to skip (1-30, default=10)
- truncate_ids (optional, for decision="truncate"): Tool call IDs to truncate
- compact_confidence (optional, for decision="compact"): Confidence 0-100

EXAMPLES:
# Truncate old outputs
decision: "truncate"
reasoning: "70 stale outputs from earlier task, no longer needed"
truncate_ids: "call_abc123, call_def456"

# Compact with confidence
decision: "compact"
reasoning: "Context at 65%, recent task completed"
compact_confidence: 80

# Skip for 15 turns
decision: "skip"
reasoning: "Context at 25%, plenty of space, will check back later"
skip_turns: 15

Returns: Confirmation of action taken, with details on what was done.`
}

// Parameters returns the tool parameter schema.
func (t *ContextManagementTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"decision": map[string]any{
				"type":        "string",
				"enum":        []string{"truncate", "compact", "skip"},
				"description": "What action to take",
			},
			"reasoning": map[string]any{
				"type":        "string",
				"description": "Explain WHY you made this decision (also accepts 'reason')",
			},
			"reason": map[string]any{
				"type":        "string",
				"description": "Alias for 'reasoning' - Explain WHY you made this decision",
			},
			"skip_turns": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"maximum":     30,
				"default":     10,
				"description": "If decision='skip', how many turns to skip before reminding again (higher = you promise to be proactive)",
			},
			"truncate_ids": map[string]any{
				"type":        "string",
				"description": "Comma-separated tool call IDs to truncate (e.g., 'call_abc123, call_def456')",
			},
			"compact_confidence": map[string]any{
				"type":        "integer",
				"minimum":     0,
				"maximum":     100,
				"description": "Confidence for COMPACT (0-100), if decision='compact'",
			},
		},
		"required": []string{"decision", "reasoning"},
	}
}

// Execute runs the tool.
func (t *ContextManagementTool) Execute(ctx context.Context, params map[string]any) ([]agentctx.ContentBlock, error) {
	// Get agent context from context
	agentCtx := agentctx.ToolExecutionAgentContext(ctx)
	if agentCtx == nil {
		return nil, fmt.Errorf("agent context not available")
	}

	// Parse decision
	decision, ok := params["decision"].(string)
	if !ok || decision == "" {
		return nil, fmt.Errorf("decision parameter is required")
	}

	// Parse reasoning (accept both "reasoning" and "reason" for tolerance)
	reasoning, ok := params["reasoning"].(string)
	if !ok || reasoning == "" {
		// Fallback to "reason" for tolerance
		reasoning, ok = params["reason"].(string)
	}
	if !ok || reasoning == "" {
		return nil, fmt.Errorf("reasoning (or reason) parameter is required")
	}

	// context_management may be called multiple times in one assistant message.
	// Serialize state/message mutations to avoid races and lost updates.
	agentCtx.LockContextManagement()
	defer agentCtx.UnlockContextManagement()

	// Get or create ContextMgmtState
	if agentCtx.ContextMgmtState == nil {
		agentCtx.ContextMgmtState = agentctx.DefaultContextMgmtState()
	}

	// Mark that LLM made a decision this turn (compliance tracking)
	// Capture snapshot BEFORE MarkDecisionMade for accurate ratio calculation
	// (MarkDecisionMade will increment ProactiveDecisions, affecting the ratio)
	snapshot := agentCtx.ContextMgmtState.Snapshot()
	agentCtx.ContextMgmtState.MarkDecisionMade()

	turn, wasReminded := agentCtx.ContextMgmtState.GetTurnAndReminderStatus()

	var result strings.Builder
	result.WriteString(fmt.Sprintf("**Context Management Decision: %s**\n\n", strings.ToUpper(decision)))
	result.WriteString(fmt.Sprintf("Reasoning: %s\n\n", reasoning))

	switch decision {
	case "skip":
		skipTurns := 10 // default
		if st, ok := params["skip_turns"].(float64); ok {
			skipTurns = int(st)
		} else if st, ok := params["skip_turns"].(int); ok {
			skipTurns = st
		}
		if skipTurns < 1 {
			skipTurns = 1
		}
		if skipTurns > 30 {
			skipTurns = 30
		}
		originalSkipTurns := skipTurns

		// Calculate max skip based on proactive ratio (from pre-decision snapshot)
		// maxSkip = proactiveDecisions - reminderNeeded, capped at 30, floored at 0
		ratio := snapshot.ProactiveDecisions - snapshot.ReminderNeeded
		maxSkip := ratio
		if maxSkip > 30 {
			maxSkip = 30
		}
		if maxSkip < 0 {
			maxSkip = 0
		}

		if ratio <= 0 {
			// Case 1: Deny skip when ratio <= 0
			nextReminderTurn := agentCtx.ContextMgmtState.LastReminderTurn + snapshot.ReminderFrequency
			if nextReminderTurn <= turn {
				nextReminderTurn = turn + snapshot.ReminderFrequency
			}
			result.WriteString("⚠️ skip request denied\n\n")
			result.WriteString(fmt.Sprintf("Since you are not proactive enough (ratio=%d), you are not allowed to skip %d turns.\n\n", ratio, skipTurns))
			result.WriteString(fmt.Sprintf("You will still receive a remind within %d turns (turn %d).\n", snapshot.ReminderFrequency, nextReminderTurn))
			result.WriteString("You must be more proactive before you receive the next remind.\n\n")
			result.WriteString(fmt.Sprintf("Next reminder at: turn %d\n", nextReminderTurn))
			result.WriteString(fmt.Sprintf("Current stats: proactive=%d, reminded=%d, ratio=%d, frequency=%d turns\n",
				snapshot.ProactiveDecisions, snapshot.ReminderNeeded, ratio, snapshot.ReminderFrequency))

			// Don't call SetSkipUntil, skip is denied
			traceevent.Log(ctx, traceevent.CategoryTool, "context_decision_skip_denied",
				traceevent.Field{Key: "requested_skip_turns", Value: skipTurns},
				traceevent.Field{Key: "ratio", Value: ratio},
				traceevent.Field{Key: "was_reminded", Value: wasReminded},
			)
		} else if skipTurns > maxSkip {
			// Case 2: Reduce skipTurns when over limit
			skipTurns = maxSkip
			agentCtx.ContextMgmtState.SetSkipUntil(turn, skipTurns, wasReminded)
			result.WriteString(fmt.Sprintf("⚠️ skip_turns reduced from %d to %d\n\n", originalSkipTurns, skipTurns))
			result.WriteString(fmt.Sprintf("Reason: Your proactive ratio is %d (max skip allowed: %d)\n", ratio, maxSkip))
			result.WriteString("To skip more turns, make more proactive context management decisions.\n\n")
			result.WriteString(fmt.Sprintf("Next reminder at: turn %d\n", turn+skipTurns))

			// Record the skip decision so LastDecisionTurn is updated
			agentCtx.ContextMgmtState.RecordDecision(turn, "skip", wasReminded)

			traceevent.Log(ctx, traceevent.CategoryTool, "context_decision_skip_reduced",
				traceevent.Field{Key: "requested_skip_turns", Value: originalSkipTurns},
				traceevent.Field{Key: "actual_skip_turns", Value: skipTurns},
				traceevent.Field{Key: "max_skip", Value: maxSkip},
				traceevent.Field{Key: "ratio", Value: ratio},
				traceevent.Field{Key: "skip_until_turn", Value: turn + skipTurns},
				traceevent.Field{Key: "was_reminded", Value: wasReminded},
			)
		} else {
			// Case 3: Allow skip within limit
			agentCtx.ContextMgmtState.SetSkipUntil(turn, skipTurns, wasReminded)
			result.WriteString(fmt.Sprintf("Skipping reminders for %d turns. Next reminder at turn %d.\n", skipTurns, turn+skipTurns))

			// Record the skip decision so LastDecisionTurn is updated
			agentCtx.ContextMgmtState.RecordDecision(turn, "skip", wasReminded)

			traceevent.Log(ctx, traceevent.CategoryTool, "context_decision_skip",
				traceevent.Field{Key: "skip_turns", Value: skipTurns},
				traceevent.Field{Key: "skip_until_turn", Value: turn + skipTurns},
				traceevent.Field{Key: "was_reminded", Value: wasReminded},
			)
		}

	case "truncate":
		truncatedCount := 0
		// Support both string (comma-separated) and array formats for backwards compatibility
		var idsToTruncate []string
		if ids, ok := params["truncate_ids"].(string); ok && ids != "" {
			// Parse comma-separated string
			for _, id := range strings.Split(ids, ",") {
				id = strings.TrimSpace(id)
				if id != "" {
					idsToTruncate = append(idsToTruncate, id)
				}
			}
		} else if ids, ok := params["truncate_ids"].([]any); ok && len(ids) > 0 {
			// Fallback to array format
			for _, id := range ids {
				if idStr, ok := id.(string); ok {
					idsToTruncate = append(idsToTruncate, idStr)
				}
			}
		}

		if len(idsToTruncate) > 0 {
			// Filter out already truncated IDs to avoid redundant operations
			// Pass the original raw IDs (string or []any) to filterAlreadyTruncated
			idsToTruncate = t.filterAlreadyTruncated(ctx, agentCtx, params["truncate_ids"])

			truncatedCount = t.processTruncate(ctx, agentCtx, idsToTruncate)
			result.WriteString(fmt.Sprintf("Truncated %d tool output(s).\n", truncatedCount))

			if truncatedCount > 0 {
				result.WriteString(fmt.Sprintf("Freed up approximately %d message slots.\n", truncatedCount))
			} else {
				result.WriteString("No outputs were truncated (already truncated or IDs not found).\n")
			}
		} else {
			result.WriteString("No truncate_ids provided, skipped truncation.\n")
		}

		// Clean up truncate_ids from the assistant message that called this tool
		t.cleanupContextManagementInput(agentCtx, agentctx.ToolExecutionCallID(ctx))

		// Record decision
		agentCtx.ContextMgmtState.RecordDecision(turn, "truncate", wasReminded)
		traceevent.Log(ctx, traceevent.CategoryTool, "context_decision_truncate",
			traceevent.Field{Key: "truncated_count", Value: truncatedCount},
			traceevent.Field{Key: "was_reminded", Value: wasReminded},
		)

	case "compact":
		if t.compactor == nil {
			result.WriteString("Compactor not available, skipped compaction.\n")
			agentCtx.ContextMgmtState.RecordDecision(turn, "compact", wasReminded)
			break
		}

		// LLM decided compact, we trust its decision and proceed
		// No token threshold check - if LLM wants compact, we do compact
		confidence := 80 // default
		if c, ok := params["compact_confidence"].(float64); ok {
			confidence = int(c)
		}

		before := len(agentCtx.Messages)
		compacted, err := t.compactor.Compact(agentCtx.Messages, agentCtx.LastCompactionSummary)
		if err != nil {
			slog.Warn("[LLMContextDecision] Compaction failed", "error", err)
			result.WriteString(fmt.Sprintf("Compaction failed: %v\n", err))
		} else if compacted != nil {
			agentCtx.Messages = compacted.Messages
			agentCtx.LastCompactionSummary = compacted.Summary
			after := len(compacted.Messages)

			// Set flag to inject overview.md for recovery on next request
			agentCtx.PostCompactRecovery = true

			// Persist changes to session storage
			if agentCtx.OnMessagesChanged != nil {
				if persistErr := agentCtx.OnMessagesChanged(); persistErr != nil {
					slog.Warn("[LLMContextDecision] Failed to persist compacted messages", "error", persistErr)
				}
			}

			result.WriteString(fmt.Sprintf("Compacted messages: %d → %d (%d removed).\n", before, after, before-after))
			result.WriteString(fmt.Sprintf("Confidence: %d%%\n", confidence))

			traceevent.Log(ctx, traceevent.CategoryTool, "context_decision_compact",
				traceevent.Field{Key: "before_messages", Value: before},
				traceevent.Field{Key: "after_messages", Value: after},
				traceevent.Field{Key: "confidence", Value: confidence},
				traceevent.Field{Key: "was_reminded", Value: wasReminded},
			)
		} else {
			result.WriteString("Compaction returned no changes.\n")
		}

		agentCtx.ContextMgmtState.RecordDecision(turn, "compact", wasReminded)

	default:
		return nil, fmt.Errorf("invalid decision: %s", decision)
	}

	// Add stats summary
	if decision != "skip" {
		stats := agentCtx.ContextMgmtState.Snapshot()
		result.WriteString(fmt.Sprintf("\n**Stats:** proactive=%d, reminded=%d, frequency=%d turns, score=%s\n",
			stats.ProactiveDecisions,
			stats.ReminderNeeded,
			stats.ReminderFrequency,
			stats.Score))
	}

	return []agentctx.ContentBlock{
		agentctx.TextContent{
			Type: "text",
			Text: result.String(),
		},
	}, nil
}

// processTruncate truncates the specified tool outputs.
func (t *ContextManagementTool) processTruncate(ctx context.Context, agentCtx *agentctx.AgentContext, idsToTruncate []string) int {
	truncatedCount := 0

	for i := range agentCtx.Messages {
		msg := agentCtx.Messages[i]
		if msg.Role != "toolResult" {
			continue
		}

		// Check if this ID should be truncated
		if !t.shouldTruncate(msg.ToolCallID, idsToTruncate) {
			continue
		}

		// Skip if already truncated
		if agentctx.IsTruncatedAgentToolTag(msg.ExtractText()) {
			continue
		}

		// Get original size
		originalSize := len(msg.ExtractText())
		if n, ok := agentctx.ParseCharsFromAgentToolTag(msg.ExtractText()); ok {
			originalSize = n
		}

		// Replace with truncated tag (no id to prevent reuse)
		agentCtx.Messages[i] = agentctx.NewToolResultMessage(
			msg.ToolCallID,
			msg.ToolName,
			[]agentctx.ContentBlock{
				agentctx.TextContent{
					Type: "text",
					Text: fmt.Sprintf(
						`<agent:tool name="%s" chars="%d" truncated="true" />`,
						msg.ToolName,
						originalSize,
					),
				},
			},
			msg.IsError,
		)

		truncatedCount++
		traceevent.Log(ctx, traceevent.CategoryTool, "tool_output_truncated_via_decision",
			traceevent.Field{Key: "tool_call_id", Value: msg.ToolCallID},
			traceevent.Field{Key: "tool_name", Value: msg.ToolName},
			traceevent.Field{Key: "original_chars", Value: originalSize},
		)
	}

	return truncatedCount
}

// shouldTruncate checks whether a tool_call_id is in the list.
func (t *ContextManagementTool) shouldTruncate(toolCallID string, idsToTruncate []string) bool {
	for _, id := range idsToTruncate {
		if strings.EqualFold(toolCallID, id) {
			return true
		}
	}
	return false
}

// filterAlreadyTruncated filters out already truncated tool call IDs from the provided IDs.
// This prevents LLM from trying to truncate the same tool output multiple times.
// Also protects the latest task_tracking from being truncated.
func (t *ContextManagementTool) filterAlreadyTruncated(ctx context.Context, agentCtx *agentctx.AgentContext, rawIDs any) []string {
	if rawIDs == nil {
		return nil
	}

	// Parse IDs from the input
	var idsToFilter []string

	// Handle string (comma-separated)
	if idsStr, ok := rawIDs.(string); ok && idsStr != "" {
		parts := strings.Split(idsStr, ",")
		for _, part := range parts {
			id := strings.TrimSpace(part)
			if id != "" {
				idsToFilter = append(idsToFilter, id)
			}
		}
	}

	// Handle array
	if idsArray, ok := rawIDs.([]any); ok && len(idsArray) > 0 {
		for _, id := range idsArray {
			if idStr, ok := id.(string); ok && idStr != "" {
				idsToFilter = append(idsToFilter, idStr)
			}
		}
	}

	if len(idsToFilter) == 0 {
		return nil
	}

	// Find the latest task_tracking tool call ID to protect
	protectedID := findLatestToolCall(agentCtx.Messages, "task_tracking")
	if protectedID != "" {
		slog.Debug("[ContextManagement] Protecting latest task_tracking from truncate",
			"tool_call_id", protectedID)
	}

	// Filter out IDs that are already truncated or protected
	var filteredIDs []string
	for _, id := range idsToFilter {
		// Check if this is the protected task_tracking
		if protectedID != "" && strings.EqualFold(id, protectedID) {
			slog.Debug("[ContextManagement] Skipping protected task_tracking ID",
				"tool_call_id", id)
			continue
		}

		// Check if this tool output exists and is already truncated
		alreadyTruncated := false
		for _, msg := range agentCtx.Messages {
			if msg.Role != "toolResult" {
				continue
			}
			if strings.EqualFold(msg.ToolCallID, id) {
				// Found the tool output, check if it's already truncated
				if agentctx.IsTruncatedAgentToolTag(msg.ExtractText()) {
					alreadyTruncated = true
					slog.Debug("[ContextManagement] Skipping already truncated ID",
						"tool_call_id", id)
				}
				break
			}
		}

		if !alreadyTruncated {
			filteredIDs = append(filteredIDs, id)
		}
	}

	return filteredIDs
}

// CleanupContextManagementInput removes the truncate_ids parameter from the assistant message
// that called the context_management tool.
//
// This public method is exported for testing purposes. It's called automatically during
// Execute() when a truncate operation is performed.
//
// The cleanup prevents these IDs from wasting context space after truncation has been
// executed, and avoids misleading the LLM into thinking they're still usable.
func (t *ContextManagementTool) CleanupContextManagementInput(agentCtx *agentctx.AgentContext) {
	if agentCtx == nil {
		return
	}
	agentCtx.LockContextManagement()
	defer agentCtx.UnlockContextManagement()
	t.cleanupContextManagementInput(agentCtx, "")
}

// cleanupContextManagementInput removes truncate_ids parameter from the assistant message
// that called context_management tool. This prevents these IDs from wasting context space
// after the truncation has been executed, and avoids misleading LLM into thinking they're still usable.
func (t *ContextManagementTool) cleanupContextManagementInput(agentCtx *agentctx.AgentContext, currentCallID string) {
	if agentCtx == nil {
		return
	}

	// Find the assistant message that made this tool call.
	// If currentCallID is empty, fallback to cleaning the latest context_management call.
	for i := len(agentCtx.Messages) - 1; i >= 0; i-- {
		msg := agentCtx.Messages[i]
		if msg.Role != "assistant" {
			continue
		}

		toolCalls := msg.ExtractToolCalls()
		found := false
		for _, tc := range toolCalls {
			if tc.Name != "context_management" {
				continue
			}
			if currentCallID != "" && tc.ID != currentCallID {
				continue
			}
			if _, hasTruncateIDs := tc.Arguments["truncate_ids"]; !hasTruncateIDs {
				continue
			}
			if currentCallID != "" {
				found = true
				break
			}
			// Fallback mode: clean the latest context_management call that still has truncate_ids.
			found = true
			break
		}

		if found {
			// Found the assistant message with the target context_management call.
			// Remove truncate_ids from its arguments.
			cleanedContent := make([]agentctx.ContentBlock, 0, len(msg.Content))
			updated := false

			for _, block := range msg.Content {
				tc, ok := block.(agentctx.ToolCallContent)
				if !ok || tc.Name != "context_management" {
					cleanedContent = append(cleanedContent, block)
					continue
				}
				if currentCallID != "" && tc.ID != currentCallID {
					cleanedContent = append(cleanedContent, block)
					continue
				}
				if _, hasTruncateIDs := tc.Arguments["truncate_ids"]; !hasTruncateIDs {
					cleanedContent = append(cleanedContent, block)
					continue
				}

				newArgs := make(map[string]any, len(tc.Arguments))
				for k, v := range tc.Arguments {
					if k != "truncate_ids" {
						newArgs[k] = v
					}
				}
				tc.Arguments = newArgs
				cleanedContent = append(cleanedContent, tc)
				updated = true
				slog.Debug("[ContextManagement] Removed truncate_ids from tool call input",
					"tool_call_id", tc.ID)

				// In fallback mode we only clean the latest matching call once.
				if currentCallID == "" {
					cleanedContent = append(cleanedContent, msg.Content[len(cleanedContent):]...)
					break
				}
			}

			if updated {
				agentCtx.Messages[i].Content = cleanedContent
				return
			}

			// Matching call exists but no update occurred; continue searching older messages.
		}
	}

	if currentCallID == "" {
		slog.Warn("[ContextManagement] cleanupContextManagementInput: no target context_management call found")
		return
	}

	slog.Warn("[ContextManagement] cleanupContextManagementInput: assistant message not found",
		"tool_call_id", currentCallID)
}

// findLatestToolCall finds the most recent tool call ID for a given tool name.
// Returns empty string if not found.
func findLatestToolCall(messages []agentctx.AgentMessage, toolName string) string {
	// Iterate in reverse to find the most recent
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
