package tools

import (
	"context"
	"fmt"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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
	return "Search file contents for a pattern. Returns matching lines with file paths and line numbers. Respects .gitignore. Supports context lines (-C), ignore case (-i), literal mode, and match limit."
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
			"ignoreCase": map[string]any{
				"type":        "boolean",
				"description": "Case-insensitive search (default: false)",
			},
			"literal": map[string]any{
				"type":        "boolean",
				"description": "Treat pattern as literal string instead of regex (default: false)",
			},
			"context": map[string]any{
				"type":        "integer",
				"description": "Number of context lines to show before and after each match (default: 0)",
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Maximum number of matches to return (default: 100)",
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

	// Parse optional parameters
	ignoreCase := getBoolArg(args, "ignoreCase")
	literal := getBoolArg(args, "literal")
	contextLines := getIntArg(args, "context")
	limit := getIntArgDefault(args, "limit", 100)
	if limit <= 0 {
		limit = 100
	}

	// Build command
	var cmd *exec.Cmd
	if t.commandExists("rg") {
		cmdArgs := []string{"--no-heading", "--line-number", "--color=never"}

		if ignoreCase {
			cmdArgs = append(cmdArgs, "-i")
		}
		if literal {
			cmdArgs = append(cmdArgs, "-F")
		}
		if contextLines > 0 {
			cmdArgs = append(cmdArgs, "-C", strconv.Itoa(contextLines))
		}
		// Pass -m to rg so it stops early instead of producing unlimited output.
		// Post-processing limitMatches() is still applied as a safety net.
		cmdArgs = append(cmdArgs, "-m", strconv.Itoa(limit))

		if filePattern, ok := args["filePattern"].(string); ok && filePattern != "" {
			cmdArgs = append(cmdArgs, "--glob", filePattern)
		}

		cmdArgs = append(cmdArgs, pattern, searchPath)
		cmd = exec.CommandContext(ctx, "rg", cmdArgs...)
	} else {
		// Fall back to grep
		cmdArgs := []string{"-rn"}

		if ignoreCase {
			cmdArgs = append(cmdArgs, "-i")
		}
		if literal {
			cmdArgs = append(cmdArgs, "-F")
		}
		if contextLines > 0 {
			cmdArgs = append(cmdArgs, "-C", strconv.Itoa(contextLines))
		}
		// grep doesn't have -m with context lines (it counts context too),
		// so we rely on post-processing limitMatches() for the fallback path.

		if filePattern, ok := args["filePattern"].(string); ok && filePattern != "" {
			cmdArgs = append(cmdArgs, "--include", filePattern)
		}

		cmdArgs = append(cmdArgs, pattern, searchPath)
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

	// Apply match limit
	result = limitMatches(result, limit)

	return []agentctx.ContentBlock{
		agentctx.TextContent{
			Type: "text",
			Text: result,
		},
	}, nil
}

// limitMatches truncates output to at most maxMatches match lines.
// Context lines (lines starting with '-') attached to a match are kept together.
func limitMatches(output string, maxMatches int) string {
	lines := strings.Split(output, "\n")
	var result []string
	matchCount := 0

	for i := 0; i < len(lines); i++ {
		line := lines[i]

		// Context lines (rg format: "file-line_num- text" with '-' separator)
		// are not match lines, so they don't count toward the limit.
		// Match lines have ":" separator after line number: "file:line_num:text"
		// Empty/separator lines are also passed through.
		if isContextLine(line) {
			result = append(result, line)
			continue
		}

		matchCount++
		if matchCount > maxMatches {
			// Skip remaining lines but count truncated matches
			remaining := 0
			for j := i; j < len(lines); j++ {
				if !isContextLine(lines[j]) {
					remaining++
				}
			}
			if remaining > 0 {
				result = append(result, fmt.Sprintf("\n[%d matches truncated. Use a higher limit or refine your pattern.]", remaining))
			}
			break
		}
		result = append(result, line)
	}

	return strings.Join(result, "\n")
}

// isContextLine checks if a line is a context line (not a match line).
// In rg/grep output with context:
//   - match lines:    "filepath:123:content"
//   - context lines:  "filepath-123-content"
//
// Strategy: find the rightmost occurrence of :\d+: or -\d+- in the line.
// The line number field is always the last such pattern before the content,
// so the rightmost match determines the line type. This correctly handles
// filenames containing dash-number patterns like "report-2024-01.txt".
func isContextLine(line string) bool {
	// Empty lines and group separators are context
	if line == "" || line == "--" {
		return true
	}

	// Track the rightmost position of each separator type
	lastMatchPos := -1   // position of :\d+: pattern
	lastContextPos := -1 // position of -\d+- pattern

	for i := 0; i < len(line); i++ {
		ch := line[i]
		if ch != ':' && ch != '-' {
			continue
		}
		// Check if followed by digits then same separator
		j := i + 1
		if j >= len(line) || line[j] < '0' || line[j] > '9' {
			continue
		}
		// Scan the number
		k := j
		for k < len(line) && line[k] >= '0' && line[k] <= '9' {
			k++
		}
		// After the number, the separator must match the one we started with
		if k < len(line) && line[k] == ch {
			if ch == ':' {
				lastMatchPos = i
			} else {
				lastContextPos = i
			}
		}
	}

	// The rightmost separator pattern determines the type.
	// If a match separator (:) appears after any context separator, it's a match.
	if lastMatchPos >= 0 && lastMatchPos > lastContextPos {
		return false
	}
	if lastContextPos >= 0 {
		return true
	}
	// No recognized pattern — treat as match line (not context)
	return false
}

// commandExists checks if a command exists in PATH.
func (t *GrepTool) commandExists(cmd string) bool {
	_, err := exec.LookPath(cmd)
	return err == nil
}

// getBoolArg extracts a boolean argument from the args map.
func getBoolArg(args map[string]any, key string) bool {
	if v, ok := args[key]; ok {
		switch val := v.(type) {
		case bool:
			return val
		case string:
			return val == "true"
		}
	}
	return false
}

// getIntArg extracts an integer argument from the args map.
func getIntArg(args map[string]any, key string) int {
	if v, ok := args[key]; ok {
		switch val := v.(type) {
		case float64:
			return int(val)
		case int:
			return val
		case string:
			if i, err := strconv.Atoi(val); err == nil {
				return i
			}
		}
	}
	return 0
}

// getIntArgDefault extracts an integer argument with a default value.
func getIntArgDefault(args map[string]any, key string, defaultVal int) int {
	if v := getIntArg(args, key); v > 0 {
		return v
	}
	return defaultVal
}
