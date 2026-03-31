package context_mgmt

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/traceevent"
)

// UpdateLLMContextTool updates the LLMContext.
type UpdateLLMContextTool struct {
	sessionDir string
}

// NewUpdateLLMContextTool creates a new UpdateLLMContextTool.
func NewUpdateLLMContextTool(sessionDir string) *UpdateLLMContextTool {
	return &UpdateLLMContextTool{
		sessionDir: sessionDir,
	}
}

// Name returns the tool name.
func (t *UpdateLLMContextTool) Name() string {
	return "update_llm_context"
}

// Description returns the tool description.
func (t *UpdateLLMContextTool) Description() string {
	return "Update the LLM-maintained structured context. Use this to record progress, decisions, and important information."
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

	// Save to current checkpoint directory
	currentCheckpointPath := filepath.Join(t.sessionDir, "current")
	llmContextPath := filepath.Join(currentCheckpointPath, "llm_context.txt")

	// Validate checkpoint directory exists
	if _, err := os.Stat(currentCheckpointPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("checkpoint directory does not exist: %s", currentCheckpointPath)
	}

	if err := os.WriteFile(llmContextPath, []byte(llmContext), 0644); err != nil {
		return nil, fmt.Errorf("failed to write llm_context.txt: %w", err)
	}

	traceevent.Log(ctx, traceevent.CategoryEvent, "context_mgmt_llm_context_updated",
		traceevent.Field{Key: "chars", Value: len(llmContext)},
		traceevent.Field{Key: "checkpoint_path", Value: currentCheckpointPath},
	)

	return []agentctx.ContentBlock{
		agentctx.TextContent{Type: "text", Text: "LLM Context updated."},
	}, nil
}
