package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// MultiEditTool applies several text replacements to one file in a single
// atomic operation. Every oldText is matched against the ORIGINAL file
// content (codex-style), never against the intermediate result, so earlier
// replacements cannot shift or invalidate later matches. The file is written
// exactly once after all ranges are resolved and validated.
type MultiEditTool struct {
	workspace *Workspace
}

// NewMultiEditTool creates a new multi-edit tool bound to a workspace.
func NewMultiEditTool(workspace *Workspace) *MultiEditTool {
	return &MultiEditTool{workspace: workspace}
}

func (t *MultiEditTool) Name() string { return "multi_edit" }

func (t *MultiEditTool) Description() string {
	return "Apply multiple find-and-replace edits to one file atomically. " +
		"Every oldText is matched against the ORIGINAL file content — earlier " +
		"edits cannot affect later ones — and nothing is written unless ALL " +
		"edits succeed and pass structural validation."
}

func (t *MultiEditTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Path to the file to edit",
			},
			"edits": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"oldText": map[string]any{"type": "string"},
						"newText": map[string]any{"type": "string"},
					},
					"required": []string{"oldText", "newText"},
				},
				"description": "Ordered list of {oldText, newText} replacements",
			},
		},
		"required": []string{"path", "edits"},
	}
}

type multiEditItem struct {
	oldText string
	newText string
}

// resolvedRangeT is the byte range [start, end) in the original content
// covered by one edit's oldText.
type resolvedRangeT struct {
	start, end int
}

func (t *MultiEditTool) Execute(ctx context.Context, args map[string]any) ([]agentctx.ContentBlock, error) {
	path, ok := args["path"].(string)
	if !ok || path == "" {
		return nil, fmt.Errorf("path must be a non-empty string")
	}

	rawEdits, ok := args["edits"].([]any)
	if !ok || len(rawEdits) == 0 {
		return nil, fmt.Errorf("edits must be a non-empty array")
	}

	items := make([]multiEditItem, 0, len(rawEdits))
	for i, raw := range rawEdits {
		obj, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("edits[%d] must be an object with oldText/newText", i)
		}
		oldText, ok := obj["oldText"].(string)
		if !ok {
			return nil, fmt.Errorf("edits[%d].oldText must be a string", i)
		}
		newText, ok := obj["newText"].(string)
		if !ok {
			return nil, fmt.Errorf("edits[%d].newText must be a string", i)
		}
		if oldText == "" {
			return nil, fmt.Errorf("edits[%d].oldText cannot be empty", i)
		}
		items = append(items, multiEditItem{oldText: oldText, newText: newText})
	}

	fullPath := t.workspace.ResolvePath(expandHome(path))

	content, err := os.ReadFile(fullPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}
	fileContent := string(content)

	// Resolve every range against the ORIGINAL content.
	ranges := make([]resolvedRangeT, len(items))
	for i, item := range items {
		if positions := exactMatchPositions(fileContent, item.oldText); len(positions) > 1 {
			return nil, fmt.Errorf("edits[%d]: %w", i,
				ambiguousMatchError(fileContent, item.oldText, positions))
		}
		m, err := findMatch(fileContent, item.oldText)
		if err != nil {
			return nil, fmt.Errorf("edits[%d]: %w", i, enrichNoMatchError(fileContent, item.oldText, err))
		}
		ranges[i] = resolvedRangeT{start: m.start, end: m.end}
	}

	// Overlap check + sort by position so replacement can go back-to-front.
	order := make([]int, len(items))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool {
		return ranges[order[a]].start < ranges[order[b]].start
	})
	for j := 1; j < len(order); j++ {
		prev, cur := order[j-1], order[j]
		if ranges[cur].start < ranges[prev].end {
			return nil, fmt.Errorf(
				"edits %d and %d overlap in the file; merge them into one edit or adjust context to disambiguate",
				prev, cur)
		}
	}

	// Apply back-to-front so earlier offsets stay valid.
	editedContent := fileContent
	for j := len(order) - 1; j >= 0; j-- {
		i := order[j]
		r := ranges[i]
		if editedContent[r.start:r.end] == items[i].newText {
			return nil, fmt.Errorf("edits[%d]: oldText and newText are identical", i)
		}
		editedContent = editedContent[:r.start] + items[i].newText + editedContent[r.end:]
	}

	// Structural sentinel on the combined result.
	if err := structCheck(fullPath, fileContent, editedContent); err != nil {
		return nil, fmt.Errorf("after applying all %d edits: %w", len(items), err)
	}

	if err := os.WriteFile(fullPath, []byte(editedContent), 0644); err != nil {
		return nil, fmt.Errorf("failed to write file: %w", err)
	}

	result := fmt.Sprintf("Applied %d edits to %s\n\nDiff:\n%s",
		len(items), path, generateMultiDiff(fileContent, items, ranges, order))

	return []agentctx.ContentBlock{agentctx.TextContent{
		Type: "text",
		Text: result,
	}}, nil
}

// generateMultiDiff renders one combined diff containing every replaced span,
// in file order.
func generateMultiDiff(content string, items []multiEditItem, ranges []resolvedRangeT, order []int) string {
	var sb strings.Builder
	for _, i := range order {
		sb.WriteString(generateDiff(content, ranges[i].start, ranges[i].end, items[i].newText))
		sb.WriteString("\n")
	}
	return sb.String()
}

// expandHome expands a leading "~/" using the user's home directory.
func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, path[2:])
	}
	return path
}
