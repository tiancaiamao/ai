package tools

import (
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// GrepTool searches for patterns in files using ripgrep or grep with dynamic workspace support.
type GrepTool struct {
	workspace *Workspace
}

// NewGrepTool creates a new Grep tool with dynamic workspace support.
func NewGrepTool(ws *Workspace) *GrepTool {
	return &GrepTool{workspace: ws}
}

// Name returns the tool name.
func (t *GrepTool) Name() string {
	return "grep"
}

// Description returns the tool description.
func (t *GrepTool) Description() string {
	return "Search file contents for patterns (respects .gitignore). Uses ripgrep if available, falls back to grep."
}

// Parameters returns the JSON Schema for the tool parameters.
func (t *GrepTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"pattern": map[string]any{
				"type":        "string",
				"description": "Search pattern (regular expression)",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "Path to search in (default: current directory)",
			},
			"filePattern": map[string]any{
				"type":        "string",
				"description": "File pattern to filter (e.g., '*.go')",
			},
		},
		"required": []string{"pattern"},
	}
}

// Execute executes the grep search.
func (t *GrepTool) Execute(ctx context.Context, args map[string]any) ([]agentctx.ContentBlock, error) {
	pattern, ok := args["pattern"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid pattern argument")
	}

	cwd := t.workspace.GetCWD()
	searchPath := cwd
	if path, ok := args["path"].(string); ok && path != "" {
		// Expand ~ to home directory
		if strings.HasPrefix(path, "~/") {
			home, _ := os.UserHomeDir()
			path = filepath.Join(home, path[2:])
		}
		if !filepath.IsAbs(path) {
			searchPath = filepath.Join(cwd, path)
		} else {
			searchPath = path
		}
	}

	// Build command
	var cmd *exec.Cmd
	if t.commandExists("rg") {
		// Use ripgraph (faster, respects .gitignore)
		cmdArgs := []string{"--no-heading", "--line-number", "--color=never", pattern, searchPath}
		if filePattern, ok := args["filePattern"].(string); ok && filePattern != "" {
			cmdArgs = append([]string{"--glob", filePattern}, cmdArgs...)
		}
		cmd = exec.CommandContext(ctx, "rg", cmdArgs...)
	} else {
		// Fall back to grep
		cmdArgs := []string{"-rn", pattern, searchPath}
		if filePattern, ok := args["filePattern"].(string); ok && filePattern != "" {
			cmdArgs = append([]string{"--include", filePattern}, cmdArgs...)
		}
		cmd = exec.CommandContext(ctx, "grep", cmdArgs...)
	}

	cmd.Dir = cwd

	output, err := cmd.CombinedOutput()
	if err != nil {
		// Check if the working directory was deleted (e.g., git worktree removed)
		if cmd.Dir != "" {
			if _, statErr := os.Stat(cmd.Dir); statErr != nil {
				return []agentctx.ContentBlock{
					agentctx.TextContent{
						Type: "text",
						Text: fmt.Sprintf("Failed to run grep: working directory %q does not exist. Use change_workspace to switch to a valid directory.", cmd.Dir),
					},
				}, nil
			}
		}
		// Grep returns exit code 1 when no matches found, which is not an error
		if len(output) == 0 {
			return []agentctx.ContentBlock{
				agentctx.TextContent{
					Type: "text",
					Text: "No matches found",
				},
			}, nil
		}
		return nil, fmt.Errorf("grep failed: %w", err)
	}

	result := strings.TrimSpace(string(output))
	if result == "" {
		result = "No matches found"
	}

	return []agentctx.ContentBlock{
		agentctx.TextContent{
			Type: "text",
			Text: result,
		},
	}, nil
}

// commandExists checks if a command exists in PATH.
func (t *GrepTool) commandExists(cmd string) bool {
	_, err := exec.LookPath(cmd)
	return err == nil
}
