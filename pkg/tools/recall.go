package tools

import (
	"context"
	"fmt"
	"strings"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// RecallMemoryTool searches working memory for relevant information
type RecallMemoryTool struct {
	manager *agentctx.MemoryManager
}

// NewRecallMemoryTool creates a new recall_memory tool
func NewRecallMemoryTool(manager *agentctx.MemoryManager) *RecallMemoryTool {
	return &RecallMemoryTool{
		manager: manager,
	}
}

// Name returns the tool name
func (t *RecallMemoryTool) Name() string {
	return "recall_memory"
}

// Description returns the tool description
func (t *RecallMemoryTool) Description() string {
	return `Search external memory for relevant information from past conversations, notes, and summaries.

Use this tool when you need to:
- Find information from earlier in the conversation
- Recall specific decisions or discussions
- Look up notes you've written to detail/

HOW TO USE:
Call this tool directly with the following parameters:
  - query (required): The search term or phrase to look for
  - scope (optional): "all" (default), "detail", or "messages"
    - "detail": Search only compaction summaries and notes in working-memory/detail/
    - "messages": Search raw conversation history in messages.jsonl
    - "all": Search both sources
  - limit (optional): Maximum number of results (default: 5)

EXAMPLE USAGE:
  - Query: "auto compact 机制"
  - Query: "role assistant 落盘" with scope="messages"
  - Query: "bug diagnosis" with scope="detail", limit=3

Returns: Top matching memory entries with citations for source lookup.`
}

// Parameters returns the tool parameter schema
func (t *RecallMemoryTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Search query",
			},
			"scope": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"all", "detail", "messages"},
				"default":     "all",
				"description": "Which sources to search",
			},
			"limit": map[string]interface{}{
				"type":        "integer",
				"default":     5,
				"description": "Maximum results",
			},
		},
		"required": []string{"query"},
	}
}

// Execute runs the tool
func (t *RecallMemoryTool) Execute(ctx context.Context, params map[string]any) ([]agentctx.ContentBlock, error) {
	query, ok := params["query"].(string)
	if !ok || query == "" {
		return nil, fmt.Errorf("query parameter is required")
	}

	scope := "all"
	if s, ok := params["scope"].(string); ok {
		scope = s
	}

	limit := 5
	if l, ok := params["limit"].(float64); ok {
		limit = int(l)
	}

	// Build search options
	opts := agentctx.DefaultSearchOptions()
	opts.Query = query
	opts.Limit = limit

	// Set sources based on scope
	switch scope {
	case "detail":
		opts.Sources = []agentctx.MemorySource{agentctx.MemorySourceDetail}
	case "messages":
		opts.Sources = []agentctx.MemorySource{agentctx.MemorySourceMessages}
	}

	// Execute search
	results, err := t.manager.Search(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("memory search failed: %w", err)
	}

	// Format results
	return []agentctx.ContentBlock{
		agentctx.TextContent{
			Type: "text",
			Text: formatSearchResults(query, results),
		},
	}, nil
}

// formatSearchResults formats search results for display
func formatSearchResults(query string, results []*agentctx.SearchResult) string {
	if len(results) == 0 {
		return fmt.Sprintf("No results found for: %s", query)
	}

	var output strings.Builder
	output.WriteString(fmt.Sprintf("Found %d result(s) for: %s\n\n", len(results), query))

	for i, r := range results {
		output.WriteString(fmt.Sprintf("\n[%d] Source: %s\n", i+1, r.Source))
		if r.FilePath != "" {
			output.WriteString(fmt.Sprintf("    File: %s\n", r.FilePath))
		}
		if r.LineNumber > 0 {
			output.WriteString(fmt.Sprintf("    Line: %d\n", r.LineNumber))
		}
		output.WriteString(fmt.Sprintf("    Content: %s\n", r.Text))
		output.WriteString(fmt.Sprintf("    Citation: %s\n", r.Citation))
	}

	return output.String()
}
