package context_mgmt

import (
	"context"
	"fmt"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/traceevent"
)

// UpdateLLMContextTool updates the LLM-maintained structured context.
type UpdateLLMContextTool struct {
	agentCtx *agentctx.AgentContext
}

// NewUpdateLLMContextTool creates a new UpdateLLMContextTool.
func NewUpdateLLMContextTool(agentCtx *agentctx.AgentContext) *UpdateLLMContextTool {
	return &UpdateLLMContextTool{agentCtx: agentCtx}
}

// Name returns the tool name.
func (t *UpdateLLMContextTool) Name() string {
	return "update_llm_context"
}

// Description returns the tool description.
func (t *UpdateLLMContextTool) Description() string {
	return "Update the structured LLM context with current task state, decisions, and important information. Always pair with truncate to reflect the cleaned-up state."
}

// Parameters returns the JSON schema for parameters.
func (t *UpdateLLMContextTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"llm_context": map[string]any{
				"type":        "string",
				"description": "The new LLM context in markdown format. Include: current task, files involved, key decisions, what's complete, next steps.",
			},
		},
		"required": []string{"llm_context"},
	}
}

// Execute updates the LLM context.
func (t *UpdateLLMContextTool) Execute(ctx context.Context, params map[string]any) ([]agentctx.ContentBlock, error) {
	llmContext, ok := params["llm_context"].(string)
	if !ok || llmContext == "" {
		return nil, fmt.Errorf("llm_context is required and must be non-empty")
	}

	charsDelta := len(llmContext) - len(t.agentCtx.LLMContext)
	t.agentCtx.LLMContext = llmContext
	t.agentCtx.AgentState.LastLLMContextUpdate = t.agentCtx.AgentState.TotalTurns

	traceevent.Log(ctx, traceevent.CategoryEvent, "context_mgmt_llm_context_updated",
		traceevent.Field{Key: "chars", Value: len(llmContext)},
		traceevent.Field{Key: "chars_delta", Value: charsDelta},
		traceevent.Field{Key: "turn", Value: t.agentCtx.AgentState.TotalTurns},
	)

	return []agentctx.ContentBlock{
		agentctx.TextContent{Type: "text", Text: "LLM Context updated."},
	}, nil
}
