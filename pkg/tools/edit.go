package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tiancaiamao/ai/pkg/agent"
)

// EditTool edits a file by replacing old text with new text.
type EditTool struct {
	cwd string
}

// NewEditTool creates a new Edit tool.
func NewEditTool(cwd string) *EditTool {
	return &EditTool{cwd: cwd}
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
func (t *EditTool) Execute(ctx context.Context, args map[string]any) ([]agent.ContentBlock, error) {
	// Extract parameters
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

	// Resolve path
	fullPath := t.resolvePath(path)

	// Read file
	content, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	fileContent := string(content)

	// Find best match using fuzzy matching
	match, err := findBestMatch(fileContent, oldText)
	if err != nil {
		return nil, err
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

	return []agent.ContentBlock{agent.TextContent{
		Type: "text",
		Text: result,
	}}, nil
}

// resolvePath resolves a path relative to the current working directory.
func (t *EditTool) resolvePath(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(t.cwd, path)
}

// matchResult represents a fuzzy match result.
type matchResult struct {
	start int
	end   int
	score int // lower is better
}

// findBestMatch finds the best matching position for oldText in content.
func findBestMatch(content, oldText string) (*matchResult, error) {
	// First try exact match
	idx := strings.Index(content, oldText)
	if idx >= 0 {
		return &matchResult{
			start: idx,
			end:   idx + len(oldText),
			score: 0,
		}, nil
	}

	// No exact match, try fuzzy matching
	// Split content and oldText into lines
	contentLines := strings.Split(content, "\n")
	oldLines := strings.Split(oldText, "\n")

	if len(oldLines) == 0 {
		return nil, fmt.Errorf("oldText is empty")
	}

	// Find the best matching window
	bestMatch := &matchResult{score: 999999}

	// Slide through content to find the best match
	for i := 0; i <= len(contentLines)-len(oldLines); i++ {
		window := contentLines[i : i+len(oldLines)]
		score := computeMatchScore(window, oldLines)

		if score < bestMatch.score {
			// Calculate character positions
			startPos := 0
			for j := 0; j < i; j++ {
				startPos += len(contentLines[j]) + 1 // +1 for newline
			}

			endPos := startPos + len(strings.Join(window, "\n"))

			bestMatch = &matchResult{
				start: startPos,
				end:   endPos,
				score: score,
			}
		}
	}

	if bestMatch.score == 999999 {
		return nil, fmt.Errorf("could not find matching text (tried fuzzy matching)")
	}

	// If score is high (poor match), warn but continue
	if bestMatch.score > 10 {
		// Try to show what we found
		matchedContent := content[bestMatch.start:bestMatch.end]
		return bestMatch, fmt.Errorf("fuzzy match is poor (score %d), found: %q", bestMatch.score, truncateString(matchedContent, 50))
	}

	return bestMatch, nil
}

// computeMatchScore computes the edit distance between two slices of lines.
func computeMatchScore(a, b []string) int {
	// Simple implementation: count character differences
	maxLen := len(a)
	if len(b) > maxLen {
		maxLen = len(b)
	}

	score := 0
	for i := 0; i < maxLen; i++ {
		if i >= len(a) {
			score += len(b[i])
		} else if i >= len(b) {
			score += len(a[i])
		} else {
			score += editDistance(a[i], b[i])
		}
	}

	return score
}

// editDistance computes the Levenshtein distance between two strings.
func editDistance(a, b string) int {
	lenA := len(a)
	lenB := len(b)

	// Optimization: if one string is empty
	if lenA == 0 {
		return lenB
	}
	if lenB == 0 {
		return lenA
	}

	// Use a smaller matrix for optimization
	if lenA < lenB {
		a, b = b, a
		lenA, lenB = lenB, lenA
	}

	// Previous row of distances
	previous := make([]int, lenB+1)
	for i := 0; i <= lenB; i++ {
		previous[i] = i
	}

	// Current row
	current := make([]int, lenB+1)

	for i := 1; i <= lenA; i++ {
		current[0] = i
		for j := 1; j <= lenB; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}

			current[j] = min(
				previous[j]+1,      // deletion
				current[j-1]+1,     // insertion
				previous[j-1]+cost, // substitution
			)
		}

		// Swap rows
		previous, current = current, previous
	}

	return previous[lenB]
}

// min returns the minimum of three integers.
func min(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
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
