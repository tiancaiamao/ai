package tools

import (
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EditMode defines the edit mode
type EditMode int

const (
	EditModeReplace EditMode = iota // Default: oldText/newText replacement
	EditModeHashline               // Hashline mode: line-addressed edits using content hashes
)

// PoorMatchError indicates a fuzzy match was found but with poor quality
type PoorMatchError struct {
	Match   *matchResult
	Message string
}

func (e *PoorMatchError) Error() string {
	return e.Message
}

// EditTool edits a file by replacing old text with new text with dynamic workspace support.
type EditTool struct {
	workspace *Workspace
	editMode  EditMode
}

// NewEditTool creates a new Edit tool with dynamic workspace support.
func NewEditTool(ws *Workspace) *EditTool {
	return &EditTool{workspace: ws}
}

// SetEditMode sets the edit mode.
func (t *EditTool) SetEditMode(mode EditMode) {
	t.editMode = mode
}

// Name returns the tool name.
func (t *EditTool) Name() string {
	return "edit"
}

// Description returns the tool description.
func (t *EditTool) Description() string {
	return "Edit a file by replacing text. Supports fuzzy matching to find the text to replace."
}

// Parameters returns the tool parameters.
func (t *EditTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Path to the file to edit (relative or absolute)",
			},
			"oldText": map[string]any{
				"type":        "string",
				"description": "Text to search for and replace",
			},
			"newText": map[string]any{
				"type":        "string",
				"description": "New text to replace the old text with",
			},
		},
		"required": []string{"path", "oldText", "newText"},
	}
}

// Execute executes the Edit tool.
func (t *EditTool) Execute(ctx context.Context, args map[string]any) ([]agentctx.ContentBlock, error) {
	// Check for hashline mode edits
	if edits, ok := args["edits"].([]interface{}); ok && len(edits) > 0 {
		return t.executeHashlineEdits(ctx, args)
	}

	// Extract parameters for replace mode
	path, ok := args["path"].(string)
	if !ok {
		return nil, fmt.Errorf("path must be a string")
	}

	oldText, ok := args["oldText"].(string)
	if !ok {
		return nil, fmt.Errorf("oldText must be a string")
	}

	newText, ok := args["newText"].(string)
	if !ok {
		return nil, fmt.Errorf("newText must be a string")
	}

	// Resolve path using current workspace
	fullPath := t.resolvePath(path)

	// Read file
	content, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	fileContent := string(content)

	// Find best match using layered fuzzy matching
	match, err := findBestMatch(fileContent, oldText)
	if err != nil {
		// Check if it's a PoorMatchError (edit still proceeds)
		if poorErr, ok := err.(*PoorMatchError); ok {
			match = poorErr.Match
			// Edit proceeds with the poor match
		} else {
			// Genuine error (no match found)
			return nil, err
		}
	}

	// Generate diff
	diff := generateDiff(fileContent, match.start, match.end, newText)

	// Replace text
	editedContent := fileContent[:match.start] + newText + fileContent[match.end:]

	// Write back
	if err := os.WriteFile(fullPath, []byte(editedContent), 0644); err != nil {
		return nil, fmt.Errorf("failed to write file: %w", err)
	}

	// Return success with diff
	result := fmt.Sprintf("Edited %s\n\nDiff:\n%s", path, diff)

	// Check if there was a warning and append it
	if poorErr, ok := err.(*PoorMatchError); ok {
		result += fmt.Sprintf("\n\nWarning: %s", poorErr.Message)
	}

	return []agentctx.ContentBlock{agentctx.TextContent{
		Type: "text",
		Text: result,
	}}, nil
}

// executeHashlineEdits handles hashline mode edits
func (t *EditTool) executeHashlineEdits(ctx context.Context, args map[string]any) ([]agentctx.ContentBlock, error) {
	path, ok := args["path"].(string)
	if !ok {
		return nil, fmt.Errorf("path must be a string")
	}

	editsRaw, ok := args["edits"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("edits must be an array")
	}

	// Convert edits to HashlineEdit slice
	edits := make([]HashlineEdit, 0, len(editsRaw))
	for i, e := range editsRaw {
		editMap, ok := e.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("edit %d must be a map", i)
		}
		edit, err := parseHashlineEditFromMap(editMap)
		if err != nil {
			return nil, fmt.Errorf("failed to parse edit %d: %w", i, err)
		}
		edits = append(edits, *edit)
	}

	// Read file content
	fullPath := t.resolvePath(path)
	content, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	// Apply hashline edits
	result, err := ApplyHashlineEdits(edits, string(content))
	if err != nil {
		return nil, err
	}

	// Write back
	if err := os.WriteFile(fullPath, []byte(result.Content), 0644); err != nil {
		return nil, fmt.Errorf("failed to write file: %w", err)
	}

	summary := fmt.Sprintf("Applied %d edits, first changed line: %d", len(edits), result.FirstChangedLine)
	if len(result.Warnings) > 0 {
		summary += "\nWarnings:\n"
		for _, w := range result.Warnings {
			summary += "  - " + w + "\n"
		}
	}

	return []agentctx.ContentBlock{agentctx.TextContent{
		Type: "text",
		Text: summary,
	}}, nil
}

// parseHashlineEditFromMap parses a hashline edit from a map
func parseHashlineEditFromMap(m map[string]interface{}) (*HashlineEdit, error) {
	edit := HashlineEdit{}

	// Parse type
	if t, ok := m["type"].(string); ok {
		switch t {
		case "set_line":
			edit.Type = HashlineEditSetLine
		case "replace_lines":
			edit.Type = HashlineEditReplaceLines
		case "insert_after":
			edit.Type = HashlineEditInsertAfter
		default:
			return nil, fmt.Errorf("unknown edit type: %s", t)
		}
	}

	// Parse anchors
	if anchor, ok := m["anchor"].(string); ok {
		edit.Anchor = anchor
	}
	if startAnchor, ok := m["start_anchor"].(string); ok {
		edit.StartAnchor = startAnchor
	}
	if endAnchor, ok := m["end_anchor"].(string); ok {
		edit.EndAnchor = endAnchor
	}

	// Parse text content
	if newText, ok := m["new_text"].(string); ok {
		edit.NewText = newText
	}
	if text, ok := m["text"].(string); ok {
		edit.Text = text
	}

	return &edit, nil
}

func (t *EditTool) resolvePath(path string) string {
	// If absolute path, use as-is
	if filepath.IsAbs(path) {
		return path
	}

	// If relative to workspace, join with workspace
	if t.workspace != nil {
		return filepath.Join(t.workspace.GetGitRoot(), path)
	}

	// Otherwise use as relative to current dir
	return path
}

// generateDiff generates a unified diff for the edit.
func generateDiff(content string, start, end int, newText string) string {
	var sb strings.Builder

	// Extract old text
	oldText := content[start:end]

	// Split into lines for comparison
	oldLines := strings.Split(oldText, "\n")
	newLines := strings.Split(newText, "\n")

	// Show context (a few lines before and after)
	contextLines := 2

	// Find line numbers
	linesBefore := strings.Count(content[:start], "\n")
	linesAfter := linesBefore + len(oldLines)

	// Find context start
	contextStart := linesBefore - contextLines
	if contextStart < 0 {
		contextStart = 0
	}

	// Find context end
	allLines := strings.Split(content, "\n")
	contextEnd := linesAfter + contextLines
	if contextEnd > len(allLines) {
		contextEnd = len(allLines)
	}

	sb.WriteString(fmt.Sprintf("--- @@ %d,%d +%d,%d @@\n",
		contextStart+1, len(oldLines), contextStart+1, len(newLines)))

	// Show removed lines
	for _, line := range oldLines {
		sb.WriteString(fmt.Sprintf("-%s\n", line))
	}

	// Show added lines
	for _, line := range newLines {
		sb.WriteString(fmt.Sprintf("+%s\n", line))
	}

	return sb.String()
}

// truncateString truncates a string to a maximum length.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
