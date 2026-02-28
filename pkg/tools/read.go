package tools

import (
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

)

// ReadTool reads file contents.
type ReadTool struct {
	cwd string
}

// NewReadTool creates a new Read tool.
func NewReadTool(cwd string) *ReadTool {
	return &ReadTool{cwd: cwd}
}

// Name returns the tool name.
func (t *ReadTool) Name() string {
	return "read"
}

// Description returns the tool description.
func (t *ReadTool) Description() string {
	return "Read the contents of a file. Supports text files."
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

	// Resolve path
	if !filepath.IsAbs(path) {
		path = filepath.Join(t.cwd, path)
	}

	// Read file
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %w", path, err)
	}

	// Check if it's a text file (basic check)
	content := string(data)

	// Truncate if too large (limit to 100KB for now)
	const maxSize = 100 * 1024
	if len(content) > maxSize {
		content = content[:maxSize]
		content += "\n\n... (file truncated, too large)"
	}

	return []agentctx.ContentBlock{
		agentctx.TextContent{
			Type: "text",
			Text: content,
		},
	}, nil
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
