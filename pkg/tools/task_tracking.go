package tools

import (
	"context"
	"fmt"
	"strings"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/traceevent"
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
	// Get agent context from context
	agentCtx := agentctx.ToolExecutionAgentContext(ctx)
	if agentCtx == nil {
		return nil, fmt.Errorf("agent context not available")
	}

	// Parse skip parameter
	skip, err := parseSkipParameter(params["skip"])
	if err != nil {
		return nil, err
	}

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

		traceevent.Log(ctx, traceevent.CategoryTool, "task_tracking_skip",
			traceevent.Field{Key: "reasoning", Value: reasoning},
		)

		return []agentctx.ContentBlock{
			agentctx.TextContent{
				Type: "text",
				Text: fmt.Sprintf("Task tracking skipped. Reason: %s", reasoning),
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
			traceevent.Log(ctx, traceevent.CategoryTool, "task_tracking_failed",
				traceevent.Field{Key: "error", Value: err.Error()},
				traceevent.Field{Key: "content_len", Value: len(content)},
			)
			return nil, fmt.Errorf("failed to write task tracking: %w", err)
		}
	}

	// Mark updated to reset roundsSinceUpdate counter (stops reminder loop)
	if agentCtx.TaskTrackingState != nil {
		agentCtx.TaskTrackingState.MarkUpdated()
	}

	// Log successful update
	traceevent.Log(ctx, traceevent.CategoryTool, "task_tracking",
		traceevent.Field{Key: "content_len", Value: len(content)},
	)

	// Return simple confirmation (tool output stays in context window)
	return []agentctx.ContentBlock{
		agentctx.TextContent{
			Type: "text",
			Text: "Task tracking updated.",
		},
	}, nil
}

func parseSkipParameter(raw any) (bool, error) {
	if raw == nil {
		return false, nil
	}

	switch v := raw.(type) {
	case bool:
		return v, nil
	case string:
		normalized := strings.ToLower(strings.TrimSpace(v))
		switch normalized {
		case "", "false", "0", "no", "n", "off":
			return false, nil
		case "true", "1", "yes", "y", "on":
			return true, nil
		default:
			return false, fmt.Errorf("skip parameter must be boolean-like (true/false)")
		}
	case float64:
		if v == 0 {
			return false, nil
		}
		if v == 1 {
			return true, nil
		}
		return false, fmt.Errorf("skip parameter must be 0 or 1 when numeric")
	case int:
		if v == 0 {
			return false, nil
		}
		if v == 1 {
			return true, nil
		}
		return false, fmt.Errorf("skip parameter must be 0 or 1 when numeric")
	case int64:
		if v == 0 {
			return false, nil
		}
		if v == 1 {
			return true, nil
		}
		return false, fmt.Errorf("skip parameter must be 0 or 1 when numeric")
	default:
		return false, fmt.Errorf("skip parameter must be boolean")
	}
}
