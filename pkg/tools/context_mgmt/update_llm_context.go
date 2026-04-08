package context_mgmt

import (
	"context"
	"fmt"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/traceevent"
)

// UpdateLLMContextTool updates LLMContext.
type UpdateLLMContextTool struct {
	snapshot *agentctx.ContextSnapshot
}

// NewUpdateLLMContextTool creates a new UpdateLLMContextTool.
func NewUpdateLLMContextTool(snapshot *agentctx.ContextSnapshot) *UpdateLLMContextTool {
	return &UpdateLLMContextTool{
		snapshot: snapshot,
	}
}

// Name returns the tool name.
func (t *UpdateLLMContextTool) Name() string {
	return "update_llm_context"
}

// Description returns the tool description.
func (t *UpdateLLMContextTool) Description() string {
	return "Update LLM-maintained structured context. Use this to record progress, decisions, and important information."
}

// Parameters returns the JSON schema for parameters.
func (t *UpdateLLMContextTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"llm_context": map[string]any{
				"type":        "string",
				"description": "The new LLM context in markdown format",
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
	if t.snapshot == nil {
		return nil, fmt.Errorf("context snapshot is not available")
	}

	charsDelta := len(llmContext) - len(t.snapshot.LLMContext)
	t.snapshot.LLMContext = llmContext
	t.snapshot.AgentState.LastLLMContextUpdate = t.snapshot.AgentState.TotalTurns

	traceevent.Log(ctx, traceevent.CategoryEvent, "context_mgmt_llm_context_updated",
		traceevent.Field{Key: "chars", Value: len(llmContext)},
		traceevent.Field{Key: "chars_delta", Value: charsDelta},
		traceevent.Field{Key: "turn", Value: t.snapshot.AgentState.TotalTurns},
	)

	return []agentctx.ContentBlock{
		agentctx.TextContent{Type: "text", Text: "LLM Context updated."},
	}, nil
}