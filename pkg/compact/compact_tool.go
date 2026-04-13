package compact

import (
	"context"
	"fmt"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/traceevent"
)

// CompactTool performs full context compaction with configurable strategy.
// This allows LLM to decide when to compact based on context analysis.
type CompactTool struct {
	agentCtx  *agentctx.AgentContext
	compactor *Compactor
}

// NewCompactTool creates a new CompactTool.
func NewCompactTool(agentCtx *agentctx.AgentContext, compactor *Compactor) *CompactTool {
	return &CompactTool{
		agentCtx:  agentCtx,
		compactor: compactor,
	}
}

// Name returns the tool name.
func (t *CompactTool) Name() string {
	return "compact"
}

// Description returns the tool description.
func (t *CompactTool) Description() string {
	return `Perform full context compaction by summarizing and removing old messages.

Use this when:
- Many truncations have occurred and context is still under pressure (>40%)
- A topic shift or task phase has been completed
- Historical context is no longer relevant to current work

This is more aggressive than truncate_messages and should be used judiciously.
`
}

// Parameters returns the JSON schema for parameters.
func (t *CompactTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"strategy": map[string]any{
				"type":        "string",
				"enum":        []string{"conservative", "balanced", "aggressive"},
				"description": `Compaction strategy:
  - "conservative": Keep more recent history (50% more tokens), minimal deletion
  - "balanced": Standard compaction (default)
  - "aggressive": Maximum compression, keep only essential recent history`,
			},
			"keep_recent_tokens": map[string]any{
				"type":        "integer",
				"description": "Optional token budget to keep from recent messages. If not provided, uses strategy-based defaults.",
			},
			"reason": map[string]any{
				"type":        "string",
				"description": "Explanation for why compaction is needed now (topic shift, phase completed, truncations exhausted, etc.)",
			},
		},
		"required": []string{"reason"},
	}
}

// Execute performs the compaction.
// It appends a compact event to messages.jsonl (immutable log),
// then applies the compaction to the session.
func (t *CompactTool) Execute(ctx context.Context, params map[string]any) ([]agentctx.ContentBlock, error) {
	reason, ok := params["reason"].(string)
	if !ok || reason == "" {
		return nil, fmt.Errorf("reason is required and must be non-empty")
	}

	// Parse strategy
	strategy := "balanced"
	if s, ok := params["strategy"].(string); ok {
		strategy = s
	}

	// Parse optional keep_recent_tokens
	var keepRecentTokens int
	if k, ok := params["keep_recent_tokens"].(float64); ok {
		keepRecentTokens = int(k)
	} else if k, ok := params["keep_recent_tokens"].(int); ok {
		keepRecentTokens = k
	}

	// Append compact event to messages.jsonl (immutable, append-only)
	if t.agentCtx.OnCompactEvent != nil {
		detail := &agentctx.CompactEventDetail{
			Action: agentctx.CompactActionCompact,
		}
		if err := t.agentCtx.OnCompactEvent(detail); err != nil {
			return nil, fmt.Errorf("failed to persist compact event: %w", err)
		}
	}

	// Adjust compactor config based on strategy
	config := t.compactor.GetConfig()
	adjustedConfig := *config // Copy config to avoid mutating shared state
	if keepRecentTokens > 0 {
		adjustedConfig.KeepRecentTokens = keepRecentTokens
	} else {
		// Strategy-based defaults
		switch strategy {
		case "conservative":
			adjustedConfig.KeepRecentTokens = int(float64(adjustedConfig.KeepRecentTokens) * 1.5)
		case "aggressive":
			adjustedConfig.KeepRecentTokens = int(float64(adjustedConfig.KeepRecentTokens) * 0.5)
		default:
			// Use default KeepRecentTokens for balanced
		}
	}

	// Execute compaction with adjusted config
	// Temporarily set the adjusted config on compactor
	originalConfig := config
	t.compactor.config = &adjustedConfig
	result, err := t.compactor.Compact(t.agentCtx)
	t.compactor.config = originalConfig // Restore original config
	if err != nil {
		return nil, fmt.Errorf("compaction failed: %w", err)
	}

	// Update AgentState statistics
	t.agentCtx.AgentState.TotalCompactions++
	t.agentCtx.AgentState.LastCompactTurn = t.agentCtx.AgentState.TotalTurns

	traceevent.Log(ctx, traceevent.CategoryEvent, "context_mgmt_compact_executed",
		traceevent.Field{Key: "strategy", Value: strategy},
		traceevent.Field{Key: "keep_recent_tokens", Value: config.KeepRecentTokens},
		traceevent.Field{Key: "reason", Value: reason},
		traceevent.Field{Key: "messages_before", Value: result.MessagesBefore},
		traceevent.Field{Key: "messages_after", Value: result.MessagesAfter},
		traceevent.Field{Key: "tokens_before", Value: result.TokensBefore},
		traceevent.Field{Key: "tokens_after", Value: result.TokensAfter},
		traceevent.Field{Key: "saved", Value: result.TokensBefore - result.TokensAfter},
		traceevent.Field{Key: "total_compactions", Value: t.agentCtx.AgentState.TotalCompactions},
	)

	return []agentctx.ContentBlock{
		agentctx.TextContent{
			Type: "text",
			Text: fmt.Sprintf("Compacted context (strategy=%s). Messages: %d→%d, Tokens: %d→%d (saved %d)",
				strategy, result.MessagesBefore, result.MessagesAfter,
				result.TokensBefore, result.TokensAfter, result.TokensBefore-result.TokensAfter),
		},
	}, nil
}