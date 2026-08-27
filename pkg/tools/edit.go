package tools

import (
	"context"
	"fmt"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/tiancaiamao/ai/pkg/truncate"
)

// EditTool edits a file by replacing old text with new text with dynamic workspace support.
type EditTool struct {
	workspace *Workspace
}

// NewEditTool creates a new Edit tool with dynamic workspace support.
func NewEditTool(ws *Workspace) *EditTool {
	return &EditTool{workspace: ws}
}

// Name returns the tool name.
func (t *EditTool) Name() string {
	return "edit"
}

// Description returns the tool description.
func (t *EditTool) Description() string {
	return "Edit a file by replacing text. Requires exact match or normalized match (trailing whitespace + Unicode chars)."
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
				"description": "Text to search for and replace. Must match exactly or with normalized trailing whitespace and Unicode characters.",
			},
			"newText": map[string]any{
				"type":        "string",
				"description": "New text to replace the old text with.",
			},
		},
		"required": []string{"path", "oldText", "newText"},
	}
}

// Execute executes the Edit tool.
func (t *EditTool) Execute(ctx context.Context, args map[string]any) ([]agentctx.ContentBlock, error) {
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

	// Find match using progressive strategy
	match, err := findMatch(fileContent, oldText)
	if err != nil {
		return nil, err
	}

	// Validate that the edit actually changes something
	if fileContent[match.start:match.end] == newText {
		return nil, fmt.Errorf("oldText and newText are identical. No change needed.")
	}

	// Generate diff
	diff := generateDiff(fileContent, match.start, match.end, newText)

	// Replace text
	editedContent := fileContent[:match.start] + newText + fileContent[match.end:]

	// Structural sentinel: reject edits that break parens/indentation syntax.
	// This gives the model an immediate, localized error instead of a distant,
	// misleading interpreter failure later (which pushes it toward whole-file
	// rewrites that destroy unrelated code).
	if err := structCheck(fullPath, fileContent, editedContent); err != nil {
		return nil, err
	}

	// Write back
	if err := os.WriteFile(fullPath, []byte(editedContent), 0644); err != nil {
		return nil, fmt.Errorf("failed to write file: %w", err)
	}

	// Return success with diff
	result := fmt.Sprintf("Edited %s\n\nDiff:\n%s", path, diff)

	return []agentctx.ContentBlock{agentctx.TextContent{
		Type: "text",
		Text: result,
	}}, nil
}

// resolvePath resolves a path relative to the current working directory.
func (t *EditTool) resolvePath(path string) string {
	// Expand ~ to home directory
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, path[2:])
	}
	if filepath.IsAbs(path) {
		return path
	}
	return t.workspace.ResolvePath(path)
}

// matchResult represents a match result with strategy information.
type matchResult struct {
	start    int
	end      int
	strategy string // which matching strategy succeeded
}

// findMatch finds the text using progressive matching strategies.
// Strategies are tried in order from strict to permissive.
func findMatch(content, oldText string) (*matchResult, error) {
	if oldText == "" {
		return nil, fmt.Errorf("oldText cannot be empty")
	}

	// Strategy 1: Exact match (strictest)
	if idx := strings.Index(content, oldText); idx >= 0 {
		return &matchResult{start: idx, end: idx + len(oldText), strategy: "exact"}, nil
	}

	// Strategy 2: Match with normalized trailing whitespace and Unicode
	// This preserves leading whitespace (indentation) which is critical for
	// indentation-sensitive languages like Python, YAML, Lisp
	normalizedContent := normalizeForMatch(content)
	normalizedOldText := normalizeForMatch(oldText)

	if idx := strings.Index(normalizedContent, normalizedOldText); idx >= 0 {
		// Find the corresponding position in the original content
		// We look for a substring that matches the oldText pattern when normalized
		startPos := findOriginalPosition(content, normalizedContent, idx, len(normalizedOldText))
		if startPos >= 0 {
			// Find the end position in the original content
			endPos := findEndPosition(content, normalizedContent, idx, len(normalizedOldText))
			if endPos >= startPos {
				return &matchResult{start: startPos, end: endPos, strategy: "normalized"}, nil
			}
		}
	}

	// No match found
	return nil, fmt.Errorf("could not find text to replace. Provide more context or check for typos. Searched for: %q", truncate.TruncateString(oldText, 100))
}

// normalizeForMatch normalizes text for fuzzy matching.
// This is inspired by pi's approach: it preserves leading whitespace (indentation)
// but normalizes trailing whitespace and Unicode characters.
//
// IMPORTANT: Leading whitespace is NOT removed. This prevents indentation errors
// in Python, YAML, Lisp, and other indentation-sensitive languages.
func normalizeForMatch(text string) string {
	// Split into lines, normalize each line, then rejoin
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		// Find leading whitespace (indentation)
		leadingWs := leadingWhitespace(line)
		restOfLine := line[len(leadingWs):]

		// Normalize trailing whitespace and Unicode in the rest
		lines[i] = leadingWs + normalizeUnicode(strings.TrimRight(restOfLine, " \t"))
	}
	return strings.Join(lines, "\n")
}

// leadingWhitespace returns the leading whitespace prefix of a string.
func leadingWhitespace(s string) string {
	var count int
	for _, r := range s {
		if r == ' ' || r == '\t' {
			count++
		} else {
			break
		}
	}
	return s[:count]
}

// normalizeUnicode normalizes Unicode characters to their ASCII equivalents.
// This handles smart quotes, dashes, and other typographic characters.
func normalizeUnicode(s string) string {
	var result strings.Builder
	for _, r := range s {
		switch r {
		// Smart quotes
		case '\u2018', '\u2019', '\u201A', '\u201B': // single quotes
			result.WriteRune('\'')
		case '\u201C', '\u201D', '\u201E', '\u201F': // double quotes
			result.WriteRune('"')
		// Dashes
		case '\u2010', '\u2011', '\u2012', '\u2013': // hyphens/en-dashes
			result.WriteRune('-')
		case '\u2014', '\u2015', '\u2212': // em-dashes/minus
			result.WriteRune('-')
		// Spaces
		case '\u00A0', '\u2002', '\u2003', '\u2004', '\u2005': // NBSP, en/em spaces
			result.WriteRune(' ')
		case '\u2006', '\u2007', '\u2008', '\u2009', '\u200A': // various spaces
			result.WriteRune(' ')
		case '\u202F', '\u205F', '\u3000': // narrow no-break, medium math, ideographic
			result.WriteRune(' ')
		// Ellipsis
		case '\u2026':
			result.WriteString("...")
		default:
			result.WriteRune(r)
		}
	}
	return result.String()
}

// findOriginalPosition finds the position in the original content that corresponds
// to a position in the normalized content.
func findOriginalPosition(original, normalized string, normStart, normLength int) int {
	// Extract the normalized pattern
	normPattern := normalized[normStart : normStart+normLength]

	// Split into lines
	origLines := strings.Split(original, "\n")
	normPatternLines := strings.Split(normPattern, "\n")

	// Trim trailing empty lines from pattern (they likely come from trailing newlines in oldText)
	// This handles cases where oldText ends with \n but content has more text
	trimmedPatternLines := normPatternLines
	for len(trimmedPatternLines) > 0 && trimmedPatternLines[len(trimmedPatternLines)-1] == "" {
		trimmedPatternLines = trimmedPatternLines[:len(trimmedPatternLines)-1]
	}

	patternLines := trimmedPatternLines

	// Try to find the pattern in original by checking each line
	for i := 0; i <= len(origLines)-len(patternLines); i++ {
		// Check if this window matches when normalized
		window := origLines[i : i+len(patternLines)]
		matched := true

		for j, origLine := range window {
			normOrigLine := normalizeForMatch(origLine)
			patternLine := patternLines[j]

			// Check if this is a prefix match (pattern is shorter than the line)
			if len(patternLine) < len(normOrigLine) {
				// For prefix match, the pattern must match the start of the line exactly
				// AND the matched portion must end at a word boundary or the pattern end
				if !strings.HasPrefix(normOrigLine, patternLine) {
					matched = false
					break
				}
				// If the pattern ends in the middle of the original line, make sure
				// we're not cutting in the middle of a word (simple heuristic)
				if len(patternLine) < len(normOrigLine) {
					nextChar := rune(normOrigLine[len(patternLine)])
					if isAlphanumeric(nextChar) {
						// Pattern ends in the middle of a word, reject this match
						matched = false
						break
					}
				}
			} else if len(patternLine) > len(normOrigLine) {
				// Pattern is longer than line, this can't be a prefix match
				matched = false
				break
			} else {
				// Same length, require exact match
				if normOrigLine != patternLine {
					matched = false
					break
				}
			}
		}

		if matched {
			// Found it! Calculate the start position
			startPos := 0
			for j := 0; j < i; j++ {
				startPos += len(origLines[j]) + 1 // +1 for newline
			}
			return startPos
		}
	}

	return -1
}

// isAlphanumeric checks if a character is a letter or digit.
func isAlphanumeric(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_'
}

// findEndPosition finds the end position in the original content that corresponds
// to a match found in the normalized content.
func findEndPosition(original, normalized string, normStart, normLength int) int {
	normPattern := normalized[normStart : normStart+normLength]

	origLines := strings.Split(original, "\n")
	normPatternLines := strings.Split(normPattern, "\n")

	// Trim trailing empty lines from pattern
	trimmedPatternLines := normPatternLines
	for len(trimmedPatternLines) > 0 && trimmedPatternLines[len(trimmedPatternLines)-1] == "" {
		trimmedPatternLines = trimmedPatternLines[:len(trimmedPatternLines)-1]
	}

	patternLines := trimmedPatternLines

	// Check if the pattern ends at a line boundary (complete lines)
	patternEndsAtLineBoundary := len(normPattern) == 0 || normPattern[len(normPattern)-1] == '\n'

	for i := 0; i <= len(origLines)-len(patternLines); i++ {
		window := origLines[i : i+len(patternLines)]
		matched := true

		for j, origLine := range window {
			normOrigLine := normalizeForMatch(origLine)
			patternLine := patternLines[j]

			// Check if this is a prefix match (pattern is shorter than the line)
			if len(patternLine) < len(normOrigLine) {
				// For prefix match, the pattern must match the start of the line exactly
				if !strings.HasPrefix(normOrigLine, patternLine) {
					matched = false
					break
				}
				// If the pattern ends in the middle of the original line, make sure
				// we're not cutting in the middle of a word
				if len(patternLine) < len(normOrigLine) {
					nextChar := rune(normOrigLine[len(patternLine)])
					if isAlphanumeric(nextChar) {
						matched = false
						break
					}
				}
			} else if len(patternLine) > len(normOrigLine) {
				// Pattern is longer than line, this can't be a prefix match
				matched = false
				break
			} else {
				// Same length, require exact match
				if normOrigLine != patternLine {
					matched = false
					break
				}
			}
		}

		if matched {
			endPos := 0
			for j := 0; j < i; j++ {
				endPos += len(origLines[j]) + 1
			}

			// Handle the last line specially
			if len(patternLines) == 1 && !patternEndsAtLineBoundary {
				// Single line, partial match: use character count mapping
				origLine := origLines[i]
				patternLine := patternLines[0]

				// Map character positions from normalized to original
				origRunes := []rune(origLine)
				normOrigRunes := []rune(normalizeForMatch(origLine))
				patternRunes := []rune(patternLine)

				// Find how many runes from origLine correspond to patternRunes
				matchedRunes := 0
				for k := 0; k < len(patternRunes) && k < len(normOrigRunes); k++ {
					if patternRunes[k] == normOrigRunes[k] {
						matchedRunes++
					} else {
						break
					}
				}

				// Convert rune count to byte count
				byteCount := 0
				for k := 0; k < matchedRunes && k < len(origRunes); k++ {
					byteCount += len(string(origRunes[k]))
				}

				endPos += byteCount
			} else {
				// Multi-line or complete line match: include entire lines
				for j := 0; j < len(patternLines); j++ {
					endPos += len(origLines[i+j]) + 1
				}
				endPos-- // -1 because the last newline is not part of the match
			}

			return endPos
		}
	}

	return -1
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

// Helper functions for unicode/whitespace

// isSpace reports whether r is a space character.
func isSpace(r rune) bool {
	return unicode.IsSpace(r)
}
