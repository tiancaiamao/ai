package tools

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/traceevent"
)

// LLMContextDecisionTool allows LLM to declare context management decisions.
type LLMContextDecisionTool struct {
	compactor agentctx.Compactor
}

// NewLLMContextDecisionTool creates a new llm_context_decision tool.
func NewLLMContextDecisionTool(compactor agentctx.Compactor) *LLMContextDecisionTool {
	return &LLMContextDecisionTool{
		compactor: compactor,
	}
}

// Name returns the tool name.
func (t *LLMContextDecisionTool) Name() string {
	return "llm_context_decision"
}

// Description returns the tool description.
func (t *LLMContextDecisionTool) Description() string {
	return `Declare your context management decision. Call this tool when you need to manage context, or when runtime_state indicates action_required.

IMPORTANT: This tool is for CONTEXT MANAGEMENT only. Use it to:
- TRUNCATE: Remove old/large tool outputs to free up space
- COMPACT: Summarize conversation history to reduce context size
- SKIP: Defer context management for a specified number of turns

USAGE:
When runtime_state shows context_management.action_required is not "none", you MUST call this tool BEFORE answering the user.

DECISION OPTIONS:
- "truncate": Remove specific tool outputs (provide truncate_ids)
- "compact": Run compaction to summarize history (provide compact_confidence)
- "both": Do both truncate and compact
- "skip": Defer for skip_turns (1-30, higher = you承诺 to be proactive)

PARAMETERS:
- decision (required): "truncate", "compact", "both", or "skip"
- reasoning (required): Explain WHY you made this decision
- skip_turns (optional, for decision="skip"): How many turns to skip (1-30, default=10)
- truncate_ids (optional, for decision="truncate"/"both"): Tool call IDs to truncate
- compact_confidence (optional, for decision="compact"/"both"): Confidence 0-100

EXAMPLES:
# Truncate old outputs
decision: "truncate"
reasoning: "70 stale outputs from earlier task, no longer needed"
truncate_ids: ["call_abc123", "call_def456"]

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
func (t *LLMContextDecisionTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"decision": map[string]any{
				"type":        "string",
				"enum":        []string{"truncate", "compact", "both", "skip"},
				"description": "What action to take",
			},
			"reasoning": map[string]any{
				"type":        "string",
				"description": "Explain WHY you made this decision",
			},
			"skip_turns": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"maximum":     30,
				"default":     10,
				"description": "If decision='skip', how many turns to skip before reminding again (higher = you承诺 to be proactive)",
			},
			"truncate_ids": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Tool call IDs to truncate (if decision='truncate' or 'both')",
			},
			"compact_confidence": map[string]any{
				"type":        "integer",
				"minimum":     0,
				"maximum":     100,
				"description": "Confidence for COMPACT (0-100), if decision='compact' or 'both'",
			},
		},
		"required": []string{"decision", "reasoning"},
	}
}

// Execute runs the tool.
func (t *LLMContextDecisionTool) Execute(ctx context.Context, params map[string]any) ([]agentctx.ContentBlock, error) {
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

	// Parse reasoning
	reasoning, ok := params["reasoning"].(string)
	if !ok || reasoning == "" {
		return nil, fmt.Errorf("reasoning parameter is required")
	}

	// Get or create ContextMgmtState
	if agentCtx.ContextMgmtState == nil {
		agentCtx.ContextMgmtState = agentctx.DefaultContextMgmtState()
	}

	// Get current turn from context (updated every loop iteration)
	turn := agentCtx.ContextMgmtState.CurrentTurn
	wasReminded := (turn - agentCtx.ContextMgmtState.LastReminderTurn) < agentCtx.ContextMgmtState.ReminderFrequency

	var result strings.Builder
	result.WriteString(fmt.Sprintf("**Context Management Decision: %s**\n\n", strings.ToUpper(decision)))
	result.WriteString(fmt.Sprintf("Reasoning: %s\n\n", reasoning))

	switch decision {
	case "skip":
		skipTurns := 10 // default
		if st, ok := params["skip_turns"].(float64); ok {
			skipTurns = int(st)
		}
		if skipTurns < 1 {
			skipTurns = 1
		}
		if skipTurns > 30 {
			skipTurns = 30
		}

		agentCtx.ContextMgmtState.SetSkipUntil(turn, skipTurns, wasReminded)
		result.WriteString(fmt.Sprintf("Deferred for %d turns.\n", skipTurns))
		result.WriteString(fmt.Sprintf("Next reminder at turn %d.\n", turn+skipTurns))

		traceevent.Log(ctx, traceevent.CategoryTool, "context_decision_skip",
			traceevent.Field{Key: "skip_turns", Value: skipTurns},
			traceevent.Field{Key: "skip_until_turn", Value: turn + skipTurns},
			traceevent.Field{Key: "was_reminded", Value: wasReminded},
		)

	case "truncate", "both":
		truncatedCount := 0
		if ids, ok := params["truncate_ids"].([]any); ok && len(ids) > 0 {
			idsToTruncate := make([]string, 0, len(ids))
			for _, id := range ids {
				if idStr, ok := id.(string); ok {
					idsToTruncate = append(idsToTruncate, idStr)
				}
			}

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

		if decision == "both" {
			// Fall through to compact
		} else {
			// Record decision
			agentCtx.ContextMgmtState.RecordDecision(turn, "truncate", wasReminded)
			traceevent.Log(ctx, traceevent.CategoryTool, "context_decision_truncate",
				traceevent.Field{Key: "truncated_count", Value: truncatedCount},
				traceevent.Field{Key: "was_reminded", Value: wasReminded},
			)
		}

		if decision != "both" {
			break
		}
		fallthrough

	case "compact":
		if t.compactor == nil {
			result.WriteString("Compactor not available, skipped compaction.\n")
			agentCtx.ContextMgmtState.RecordDecision(turn, "compact", wasReminded)
			break
		}

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

		// Record decision
		agentCtx.ContextMgmtState.RecordDecision(turn, "compact", wasReminded)

	default:
		return nil, fmt.Errorf("invalid decision: %s", decision)
	}

	// Add stats summary
	if decision != "skip" {
		result.WriteString(fmt.Sprintf("\n**Stats:** proactive=%d, reminded=%d, frequency=%d turns, score=%s\n",
			agentCtx.ContextMgmtState.ProactiveDecisions,
			agentCtx.ContextMgmtState.ReminderNeeded,
			agentCtx.ContextMgmtState.ReminderFrequency,
			agentCtx.ContextMgmtState.GetScore()))
	}

	return []agentctx.ContentBlock{
		agentctx.TextContent{
			Type: "text",
			Text: result.String(),
		},
	}, nil
}

// processTruncate truncates the specified tool outputs.
func (t *LLMContextDecisionTool) processTruncate(ctx context.Context, agentCtx *agentctx.AgentContext, idsToTruncate []string) int {
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

		// Replace with truncated tag
		agentCtx.Messages[i] = agentctx.NewToolResultMessage(
			msg.ToolCallID,
			msg.ToolName,
			[]agentctx.ContentBlock{
				agentctx.TextContent{
					Type: "text",
					Text: fmt.Sprintf(
						`<agent:tool id="%s" name="%s" chars="%d" truncated="true" />`,
						msg.ToolCallID,
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
func (t *LLMContextDecisionTool) shouldTruncate(toolCallID string, idsToTruncate []string) bool {
	for _, id := range idsToTruncate {
		if strings.EqualFold(toolCallID, id) {
			return true
		}
	}
	return false
}
