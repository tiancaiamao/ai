package tools

import (
	"context"
	"fmt"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// TaskTrackingTool allows LLM to track its current task state.
type TaskTrackingTool struct{}

// NewTaskTrackingTool creates a new task_tracking tool.
func NewTaskTrackingTool() *TaskTrackingTool {
	return &TaskTrackingTool{}
}

// Name returns the tool name.
func (t *TaskTrackingTool) Name() string {
	return "task_tracking"
}

// Description returns the tool description.
func (t *TaskTrackingTool) Description() string {
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
content: "## Current Task\n- Implementing feature X\n- Progress: 50%"

# Skip update (no significant change)
skip: true
reasoning: "No state change, just answering user question"`
}

// Parameters returns the tool parameter schema.
// Conditional required: content required when skip=false, reasoning required when skip=true
func (t *TaskTrackingTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"content": map[string]any{
				"type":        "string",
				"description": "Markdown content to record. Required when skip=false. Ignored when skip=true.",
			},
			"skip": map[string]any{
				"type":        "boolean",
				"default":     false,
				"description": "Set to true when you don't need to update content but want to report activity (prevents reminder penalty).",
			},
			"reasoning": map[string]any{
				"type":        "string",
				"description": "Required when skip=true. Explain why you're skipping the update. Ignored when skip=false.",
			},
		},
	}
}

// Execute runs the tool.
func (t *TaskTrackingTool) Execute(ctx context.Context, params map[string]any) ([]agentctx.ContentBlock, error) {
	// This tool is deprecated in the new architecture.
	// The new Context Snapshot Architecture has dedicated context management tools:
	// - update_llm_context: Update LLM context
	// - truncate_messages: Truncate tool outputs
	// - no_action: Skip context management
	// These are automatically triggered based on token usage and stale outputs.
	return nil, fmt.Errorf("task_tracking tool is deprecated. Use the context management tools instead (update_llm_context, truncate_messages, no_action)")
}
