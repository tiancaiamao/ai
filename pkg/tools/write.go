package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tiancaiamao/ai/pkg/agent"
)

// WriteTool writes content to a file.
type WriteTool struct {
	cwd string
}

// NewWriteTool creates a new Write tool.
func NewWriteTool(cwd string) *WriteTool {
	return &WriteTool{cwd: cwd}
}

// Name returns the tool name.
func (t *WriteTool) Name() string {
	return "write"
}

// Description returns the tool description.
func (t *WriteTool) Description() string {
	return "Write content to a file. Creates the file if it doesn't exist, overwrites if it does."
}

// Parameters returns the JSON Schema for the tool parameters.
func (t *WriteTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Path to the file to write",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "Content to write to the file",
			},
		},
		"required": []string{"path", "content"},
	}
}

// Execute writes content to the file.
func (t *WriteTool) Execute(ctx context.Context, args map[string]any) ([]agent.ContentBlock, error) {
	path, ok1 := args["path"].(string)
	content, ok2 := args["content"].(string)
	if !ok1 || !ok2 {
		return nil, fmt.Errorf("invalid arguments")
	}

	// Resolve path
	if !filepath.IsAbs(path) {
		path = filepath.Join(t.cwd, path)
	}

	// Create parent directory if needed
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
	}

	// Write file
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return nil, fmt.Errorf("failed to write file %s: %w", path, err)
	}

	return []agent.ContentBlock{
		agent.TextContent{
			Type: "text",
			Text: fmt.Sprintf("Successfully wrote %d bytes to %s", len(content), path),
		},
	}, nil
}
