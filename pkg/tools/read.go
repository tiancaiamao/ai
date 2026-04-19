package tools

import (
	"context"
	"fmt"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	// defaultReadMaxBytes is the default maximum file size to read (50KB).
	defaultReadMaxBytes = 50 * 1024
	// defaultReadLimit is the default maximum number of lines to return.
	defaultReadLimit = 2000
)

// ReadTool reads file contents with dynamic workspace support.
type ReadTool struct {
	workspace *Workspace
	hashLines bool // Whether to output with hashline prefixes
}

// NewReadTool creates a new Read tool with dynamic workspace support.
func NewReadTool(ws *Workspace) *ReadTool {
	return &ReadTool{workspace: ws}
}

// SetHashLines enables or disables hashline output mode.
func (t *ReadTool) SetHashLines(enabled bool) {
	t.hashLines = enabled
}

// Name returns the tool name.
func (t *ReadTool) Name() string {
	return "read"
}

// Description returns the tool description.
func (t *ReadTool) Description() string {
	return "Read the contents of a file. Supports text files. Use offset and limit to read specific line ranges."
}

// Parameters returns the JSON Schema for the tool parameters.
func (t *ReadTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Path to the file to read (relative or absolute)",
			},
			"offset": map[string]any{
				"type":        "number",
				"description": "Line number to start reading from (1-indexed). Defaults to 1.",
			},
			"limit": map[string]any{
				"type":        "number",
				"description": "Maximum number of lines to read. Defaults to 2000.",
			},
		},
		"required": []string{"path"},
	}
}

// Execute reads the file and returns its contents.
func (t *ReadTool) Execute(ctx context.Context, args map[string]any) ([]agentctx.ContentBlock, error) {
	path, ok := args["path"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid path argument")
	}

	// Expand ~ to home directory
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, path[2:])
	}

	// Resolve path using current workspace
	if !filepath.IsAbs(path) {
		path = t.workspace.ResolvePath(path)
	}

	// Read file
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", path, err)
	}

	// Check if it's a text file
	if IsBinary(data) {
		return nil, fmt.Errorf("file %s appears to be binary", path)
	}

	content := string(data)

	// Parse offset and limit parameters early, before size check,
	// so that large files can be read in sections.
	offset, err := parsePositiveIntArg(args, "offset", 1)
	if err != nil {
		return nil, err
	}

	limit, err := parsePositiveIntArg(args, "limit", defaultReadLimit)
	if err != nil {
		return nil, err
	}

	// Apply line range selection
	lines := strings.Split(content, "\n")
	// strings.Split on "a\nb\n" produces ["a","b",""], trim the trailing empty element
	// so that line count matches what editors and users expect.
	totalLines := len(lines)
	if totalLines > 0 && lines[totalLines-1] == "" {
		totalLines--
	}

	// Validate offset
	if totalLines == 0 {
		return []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: ""},
		}, nil
	}
	if offset > totalLines {
		return nil, fmt.Errorf("offset %d exceeds file length (%d lines)", offset, totalLines)
	}

	// Calculate slice bounds (convert to 0-indexed)
	start := offset - 1
	end := start + limit
	if end > totalLines {
		end = totalLines
	}

	selectedLines := lines[start:end]
	output := strings.Join(selectedLines, "\n")

	// Check size of selected content (not entire file).
	// This allows reading sections of large files via offset/limit.
	if len(output) > defaultReadMaxBytes {
		return nil, fmt.Errorf("selected range is too large (%d lines, %d bytes). Use a smaller limit value, e.g. limit=%d",
			len(selectedLines), len(output), defaultReadMaxBytes/80) // rough line-width estimate
	}

	// Add continuation hints when content is truncated
	var header, footer string
	if offset > 1 {
		header = fmt.Sprintf("[%d lines above omitted. Use offset=1, limit=%d to read from start.]\n\n",
			offset-1, offset-1)
	}
	if end < totalLines {
		footer = fmt.Sprintf("\n\n[%d more lines below. Use offset=%d to continue reading.]",
			totalLines-end, end+1)
	}

	output = header + output + footer

	// Apply hashline formatting if enabled (overrides line range output format)
	if t.hashLines {
		output = FormatHashLines(strings.Join(selectedLines, "\n"), offset)
	}

	return []agentctx.ContentBlock{
		agentctx.TextContent{
			Type: "text",
			Text: output,
		},
	}, nil
}

func parsePositiveIntArg(args map[string]any, key string, defaultValue int) (int, error) {
	raw, ok := args[key]
	if !ok {
		return defaultValue, nil
	}

	parseError := fmt.Errorf("%s must be a positive integer", key)
	maxInt := int(^uint(0) >> 1)

	switch v := raw.(type) {
	case float64:
		if v < 1 || v != math.Trunc(v) || v > float64(maxInt) {
			return 0, parseError
		}
		return int(v), nil
	case float32:
		n := float64(v)
		if n < 1 || n != math.Trunc(n) || n > float64(maxInt) {
			return 0, parseError
		}
		return int(n), nil
	case int:
		if v < 1 {
			return 0, parseError
		}
		return v, nil
	case int8:
		if v < 1 {
			return 0, parseError
		}
		return int(v), nil
	case int16:
		if v < 1 {
			return 0, parseError
		}
		return int(v), nil
	case int32:
		if v < 1 {
			return 0, parseError
		}
		return int(v), nil
	case int64:
		if v < 1 || v > int64(maxInt) {
			return 0, parseError
		}
		return int(v), nil
	case uint:
		if v < 1 || v > uint(maxInt) {
			return 0, parseError
		}
		return int(v), nil
	case uint8:
		if v < 1 {
			return 0, parseError
		}
		return int(v), nil
	case uint16:
		if v < 1 {
			return 0, parseError
		}
		return int(v), nil
	case uint32:
		if v < 1 || v > uint32(maxInt) {
			return 0, parseError
		}
		return int(v), nil
	case uint64:
		if v < 1 || v > uint64(maxInt) {
			return 0, parseError
		}
		return int(v), nil
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n < 1 {
			return 0, parseError
		}
		return n, nil
	default:
		return 0, parseError
	}
}

// IsBinary checks if a file is binary by looking for NUL bytes.
func IsBinary(data []byte) bool {
	const maxSize = 8192
	if len(data) > maxSize {
		data = data[:maxSize]
	}

	for _, b := range data {
		if b == 0 {
			return true
		}
	}
	return false
}

// DetectFileType detects the file type based on extension.
func DetectFileType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))

	textExts := map[string]bool{
		".txt":           true,
		".md":            true,
		".json":          true,
		".xml":           true,
		".html":          true,
		".css":           true,
		".js":            true,
		".ts":            true,
		".go":            true,
		".py":            true,
		".rs":            true,
		".c":             true,
		".cpp":           true,
		".h":             true,
		".hpp":           true,
		".java":          true,
		".sh":            true,
		".bash":          true,
		".zsh":           true,
		".fish":          true,
		".yaml":          true,
		".yml":           true,
		".toml":          true,
		".ini":           true,
		".cfg":           true,
		".conf":          true,
		".log":           true,
		".sql":           true,
		".graphql":       true,
		".gql":           true,
		".proto":         true,
		".dockerfile":    true,
		".gitignore":     true,
		".gitattributes": true,
	}

	if textExts[ext] {
		return "text"
	}

	return "binary"
}
