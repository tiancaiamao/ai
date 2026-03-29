package tools

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
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
	agentCtx.ContextMgmtState.MarkDecisionMade()

	// Handle decision types
	switch decision {
	case "skip":
		return t.handleSkip(ctx, agentCtx, params)
	case "truncate":
		return t.handleTruncate(ctx, agentCtx, params)
	case "compact":
		return t.handleCompact(ctx, agentCtx, params)
	default:
		return nil, fmt.Errorf("unknown decision: %s", decision)
	}
}

// handleSkip implements the skip decision with proactive ratio limits.
func (t *ContextManagementTool) handleSkip(ctx context.Context, agentCtx *agentctx.AgentContext, params map[string]any) ([]agentctx.ContentBlock, error) {
	state := agentCtx.ContextMgmtState
	snapshot := state.Snapshot()

	// Get skip_turns parameter
	var requestedSkipTurns int
	if skipTurns, ok := params["skip_turns"].(int); ok && skipTurns > 0 {
		requestedSkipTurns = skipTurns
	} else {
		requestedSkipTurns = 10 // default
	}

	// Calculate max allowed skip based on proactive ratio
	ratio := snapshot.ProactiveDecisions - snapshot.ReminderNeeded
	maxSkip := ratio

	// Hard cap at 30 turns
	if maxSkip > 30 {
		maxSkip = 30
	}

	// Get current turn and calculate next reminder turn
	currentTurn := state.GetCurrentTurn()

	// Case 1: ratio <= 0 (skip DENIED)
	if ratio <= 0 {
		// Calculate when next reminder is due
		remindersUntil := snapshot.ReminderFrequency
		reminderDueTurn := currentTurn + remindersUntil

		errorMsg := fmt.Sprintf(`⚠️ skip request denied

Since you are not proactive enough (ratio=%d), you are not allowed to skip %d turns.

You will still receive a remind within %d turns (turn %d).
You must be more proactive before you receive the next remind.

Next reminder at: turn %d
Current stats: proactive=%d, reminded=%d, ratio=%d, frequency=%d turns`,
			ratio, requestedSkipTurns, remindersUntil, reminderDueTurn,
			reminderDueTurn, snapshot.ProactiveDecisions, snapshot.ReminderNeeded,
			ratio, snapshot.ReminderFrequency)

		slog.Info("[ContextManagement] Skip denied",
			"ratio", ratio,
			"requested_skip", requestedSkipTurns,
			"proactive_decisions", snapshot.ProactiveDecisions,
			"reminder_needed", snapshot.ReminderNeeded,
			"reasoning", params["reasoning"])

		// Do NOT set SkipUntilTurn - reminders will continue normally
		return []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: errorMsg}}, nil
	}

	// Case 2: requested skip exceeds max (REDUCED)
	if requestedSkipTurns > maxSkip {
		// Calculate next reminder with reduced skip
		nextReminderTurn := currentTurn + maxSkip

		warningMsg := fmt.Sprintf(`⚠️ skip_turns reduced from %d to %d

Reason: Your proactive ratio is %d (max skip allowed: %d)
To skip more turns, make more proactive context management decisions.

Next reminder at: turn %d`,
			requestedSkipTurns, maxSkip, ratio, maxSkip, nextReminderTurn)

		slog.Info("[ContextManagement] Skip reduced",
			"requested_skip", requestedSkipTurns,
			"allowed_skip", maxSkip,
			"ratio", ratio,
			"next_reminder_turn", nextReminderTurn)

		// Set skip with reduced value
		agentCtx.ContextMgmtState.SkipUntilTurn = nextReminderTurn

		return []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: warningMsg}}, nil
	}

	// Case 3: normal skip within limit (SUCCESS)
	nextReminderTurn := currentTurn + requestedSkipTurns

	successMsg := fmt.Sprintf(`Skipping reminders for %d turns. Next reminder at turn %d.`,
		requestedSkipTurns, nextReminderTurn)

	slog.Info("[ContextManagement] Skip allowed",
		"skip_turns", requestedSkipTurns,
		"next_reminder_turn", nextReminderTurn,
		"ratio", ratio)

	// Set skip normally
	agentCtx.ContextMgmtState.SkipUntilTurn = nextReminderTurn

	return []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: successMsg}}, nil
}

// handleTruncate implements the truncate decision.
func (t *ContextManagementTool) handleTruncate(ctx context.Context, agentCtx *agentctx.AgentContext, params map[string]any) ([]agentctx.ContentBlock, error) {
	// Get truncate_ids parameter
	truncateIDsParam, ok := params["truncate_ids"].(string)
	if !ok || truncateIDsParam == "" {
		return nil, fmt.Errorf("truncate_ids parameter is required for decision=truncate")
	}

	// Parse tool call IDs
	idsToTruncate := t.filterTruncateIDs(agentCtx, truncateIDsParam)

	if len(idsToTruncate) == 0 {
		return []agentctx.ContentBlock{
			agentctx.TextContent{
				Type: "text",
				Text: fmt.Sprintf("No tool outputs truncated (all specified IDs were already truncated, protected, or not found)."),
			},
		}, nil
	}

	// Execute truncation
	truncatedCount := 0
	for _, id := range idsToTruncate {
		for i, msg := range agentCtx.Messages {
			if msg.Role != "toolResult" {
				continue
			}
			if strings.EqualFold(msg.ToolCallID, id) {
				// Truncate the tool output by modifying the message directly in the slice
				agentCtx.Messages[i].Content = []agentctx.ContentBlock{
					agentctx.TextContent{
						Type: "text",
						Text: fmt.Sprintf(`<agent:tool name="%s" chars="%d" truncated="true" />`,
							msg.ToolName, len(msg.ExtractText())),
					},
				}
				truncatedCount++
				break
			}
		}
	}

	// Cleanup: remove truncate_ids from the assistant message that called this tool
	currentCallID := agentctx.ToolExecutionCallID(ctx)
	t.cleanupContextManagementInput(agentCtx, currentCallID)

	resultMsg := fmt.Sprintf(`Truncated %d tool output(s).

IDs truncated: %s

Freed context space by replacing large tool outputs with truncation markers.`,
		truncatedCount, strings.Join(idsToTruncate, ", "))

	slog.Info("[ContextManagement] Truncate completed",
		"count", truncatedCount,
		"ids", strings.Join(idsToTruncate, ", "),
		"reasoning", params["reasoning"])

	return []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: resultMsg}}, nil
}

// handleCompact implements the compact decision.
func (t *ContextManagementTool) handleCompact(ctx context.Context, agentCtx *agentctx.AgentContext, params map[string]any) ([]agentctx.ContentBlock, error) {
	// Get compact_confidence parameter
	confidence := 50 // default
	if conf, ok := params["compact_confidence"].(int); ok && conf >= 0 && conf <= 100 {
		confidence = conf
	}

	if t.compactor == nil {
		return nil, fmt.Errorf("compactor not configured")
	}

	// Execute compaction
	slog.Info("[ContextManagement] Running compaction",
		"confidence", confidence,
		"reasoning", params["reasoning"])

	previousSummary := agentCtx.LastCompactionSummary
	summary, err := t.compactor.Compact(agentCtx.Messages, previousSummary)
	if err != nil {
		slog.Error("[ContextManagement] Compaction failed", "error", err)
		return nil, fmt.Errorf("compaction failed: %w", err)
	}

	// Update messages with compacted version
	agentCtx.Messages = summary.Messages
	agentCtx.LastCompactionSummary = summary.Summary

	resultMsg := fmt.Sprintf(`Compacted conversation history to reduce context size.

Before: %d tokens
After: %d tokens
Reduction: %d tokens (%.1f%%)

Summary: %s`,
		summary.TokensBefore, summary.TokensAfter,
		summary.TokensBefore-summary.TokensAfter,
		float64(summary.TokensBefore-summary.TokensAfter)/float64(summary.TokensBefore)*100,
		summary.Summary)

	slog.Info("[ContextManagement] Compact completed",
		"tokens_before", summary.TokensBefore,
		"tokens_after", summary.TokensAfter,
		"reduction", summary.TokensBefore-summary.TokensAfter)

	return []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: resultMsg}}, nil
}

// filterTruncateIDs filters and validates truncate_ids parameter.
// It removes already-truncated or protected tool outputs.
func (t *ContextManagementTool) filterTruncateIDs(agentCtx *agentctx.AgentContext, truncateIDsParam string) []string {
	// Find the protected task_tracking tool call ID
	protectedID := ""
	for _, msg := range agentCtx.Messages {
		if msg.Role != "assistant" {
			continue
		}
		toolCalls := msg.ExtractToolCalls()
		for _, tc := range toolCalls {
			if tc.Name == "task_tracking" {
				protectedID = tc.ID
				break
			}
		}
		if protectedID != "" {
			break
		}
	}

	// Parse comma-separated IDs
	ids := strings.Split(truncateIDsParam, ",")

	// Filter out already truncated or protected
	var filteredIDs []string
	for _, id := range ids {
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