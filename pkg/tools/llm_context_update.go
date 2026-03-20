package tools

import (
	"context"
	"fmt"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/traceevent"
)

// LLMContextUpdateTool allows LLM to update its persistent context.
type LLMContextUpdateTool struct{}

// NewLLMContextUpdateTool creates a new llm_context_update tool.
func NewLLMContextUpdateTool() *LLMContextUpdateTool {
	return &LLMContextUpdateTool{}
}

// Name returns the tool name.
func (t *LLMContextUpdateTool) Name() string {
	return "llm_context_update"
}

// Description returns the tool description.
func (t *LLMContextUpdateTool) Description() string {
	return `A tool to record your current operational state. Call it when task state changes.

Provide markdown content with your current context (task, decisions, known info, pending).

The tool output stays in context window and persists for recovery after compact.

Do not repeat the full contents after calling — the tool already displays it.

SKIP MODE:
When you don't need to update content but want to report activity (prevents reminder penalty):
- Set skip=true
- Provide reasoning (required)
- This resets the reminder counter without writing to overview.md

EXAMPLES:
# Normal update (task changed)
content: "## 当前任务\n- Implementing feature X\n- Progress: 50%"

# Skip update (no significant change)
skip: true
reasoning: "No state change, just answering user question"`
}

// Parameters returns the tool parameter schema.
func (t *LLMContextUpdateTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"content": map[string]any{
				"type":        "string",
				"description": "Markdown content to record (task, decisions, known info, pending)",
			},
			"skip": map[string]any{
				"type":        "boolean",
				"default":     false,
				"description": "Set to true when you want to skip update but still report activity (prevents reminder penalty)",
			},
			"reasoning": map[string]any{
				"type":        "string",
				"description": "Required when skip=true. Explain why you're skipping the update.",
			},
		},
		"required": []string{}, // content is optional when skip=true
	}
}

// Execute runs the tool.
func (t *LLMContextUpdateTool) Execute(ctx context.Context, params map[string]any) ([]agentctx.ContentBlock, error) {
	// Get agent context from context
	agentCtx := agentctx.ToolExecutionAgentContext(ctx)
	if agentCtx == nil {
		return nil, fmt.Errorf("agent context not available")
	}

	// Parse skip parameter
	skip, _ := params["skip"].(bool)

	if skip {
		// Handle skip mode - LLM is reporting activity but not updating content
		reasoning, _ := params["reasoning"].(string)
		if reasoning == "" {
			return nil, fmt.Errorf("reasoning parameter is required when skip=true")
		}

		// Mark that LLM is still active (resets roundsSinceUpdate counter)
		if agentCtx.TaskTrackingState != nil {
			agentCtx.TaskTrackingState.MarkSkipped(reasoning)
		}

		traceevent.Log(ctx, traceevent.CategoryTool, "llm_context_update_skip",
			traceevent.Field{Key: "reasoning", Value: reasoning},
		)

		return []agentctx.ContentBlock{
			agentctx.TextContent{
				Type: "text",
				Text: fmt.Sprintf("Context update skipped. Reason: %s", reasoning),
			},
		}, nil
	}

	// Normal update mode
	content, ok := params["content"].(string)
	if !ok || content == "" {
		return nil, fmt.Errorf("content parameter is required when not skipping")
	}

	// Dual-write: persist to overview.md file via LLMContext
	if agentCtx.LLMContext != nil {
		if err := agentCtx.LLMContext.WriteContent(content); err != nil {
			traceevent.Log(ctx, traceevent.CategoryTool, "llm_context_update_failed",
				traceevent.Field{Key: "error", Value: err.Error()},
				traceevent.Field{Key: "content_len", Value: len(content)},
			)
			return nil, fmt.Errorf("failed to write context: %w", err)
		}
	}

	// Mark updated to reset roundsSinceUpdate counter (stops reminder loop)
	if agentCtx.TaskTrackingState != nil {
		agentCtx.TaskTrackingState.MarkUpdated()
	}

	// Log successful update
	traceevent.Log(ctx, traceevent.CategoryTool, "llm_context_update",
		traceevent.Field{Key: "content_len", Value: len(content)},
	)

	// Return simple confirmation (tool output stays in context window)
	return []agentctx.ContentBlock{
		agentctx.TextContent{
			Type: "text",
			Text: "Context updated.",
		},
	}, nil
}
