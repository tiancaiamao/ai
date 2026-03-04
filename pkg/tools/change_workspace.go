package tools

import (
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"context"
	"fmt"
	"path/filepath"
)

// ChangeWorkspaceTool changes the current workspace directory.
// This is primarily used for scenarios like git worktree where the agent
// needs to work in a different directory while maintaining the same session.
type ChangeWorkspaceTool struct {
	workspace *Workspace
}

// NewChangeWorkspaceTool creates a new ChangeWorkspace tool.
func NewChangeWorkspaceTool(ws *Workspace) *ChangeWorkspaceTool {
	return &ChangeWorkspaceTool{workspace: ws}
}

// Name returns the tool name.
func (t *ChangeWorkspaceTool) Name() string {
	return "change_workspace"
}

// Description returns the tool description.
func (t *ChangeWorkspaceTool) Description() string {
	return "Change the current workspace directory. Use this when working with git worktrees or when you need to operate in a different directory. All subsequent file operations (read, write, grep, edit, bash) will be relative to the new workspace."
}

// Parameters returns the JSON Schema for the tool parameters.
func (t *ChangeWorkspaceTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Path to the new workspace directory (relative or absolute)",
			},
		},
		"required": []string{"path"},
	}
}

// Execute changes the workspace directory.
func (t *ChangeWorkspaceTool) Execute(ctx context.Context, args map[string]any) ([]agentctx.ContentBlock, error) {
	path, ok := args["path"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid path argument")
	}

	// Get current workspace directory
	cwd := t.workspace.GetCWD()

	// Resolve the new path
	var newPath string
	if filepath.IsAbs(path) {
		newPath = path
	} else {
		newPath = filepath.Join(cwd, path)
	}

	// Clean the path
	newPath = filepath.Clean(newPath)

	// Try to resolve symlinks
	if resolved, err := filepath.EvalSymlinks(newPath); err == nil {
		newPath = resolved
	}

	// Update the workspace
	if err := t.workspace.SetCWD(newPath); err != nil {
		return []agentctx.ContentBlock{
			agentctx.TextContent{
				Type: "text",
				Text: fmt.Sprintf("Failed to change workspace: %s", err.Error()),
			},
		}, nil
	}

	// Get git root for context
	gitRoot := t.workspace.GetGitRoot()
	gitInfo := ""
	if gitRoot != "" && gitRoot != newPath {
		relPath, _ := filepath.Rel(gitRoot, newPath)
		gitInfo = fmt.Sprintf(" (git root: %s, relative path: %s)", gitRoot, relPath)
	} else if gitRoot != "" {
		gitInfo = fmt.Sprintf(" (git root: %s)", gitRoot)
	}

	return []agentctx.ContentBlock{
		agentctx.TextContent{
			Type: "text",
			Text: fmt.Sprintf("Workspace changed to: %s%s\nAll subsequent file operations will be relative to this directory.", newPath, gitInfo),
		},
	}, nil
}
