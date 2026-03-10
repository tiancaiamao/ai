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

Provide markdown content with your current context (task, decisions, known info, pending items).

The tool output stays in context window and persists for recovery after compact.

Do not repeat the full contents after calling — the tool already displays it.`
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
		},
		"required": []string{"content"},
	}
}

// Execute runs the tool.
func (t *LLMContextUpdateTool) Execute(ctx context.Context, params map[string]any) ([]agentctx.ContentBlock, error) {
	// Parse content
	content, ok := params["content"].(string)
	if !ok || content == "" {
		return nil, fmt.Errorf("content parameter is required")
	}

	// Get agent context from context
	agentCtx := agentctx.ToolExecutionAgentContext(ctx)
	if agentCtx == nil {
		return nil, fmt.Errorf("agent context not available")
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
		// Mark that LLM updated context this turn (for decision reminder tracking)
		agentCtx.LLMContext.SetUpdatedOverview()
		// Mark updated to reset roundsSinceUpdate counter (stops reminder loop)
		agentCtx.LLMContext.MarkUpdatedAfterToolCall(5)
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