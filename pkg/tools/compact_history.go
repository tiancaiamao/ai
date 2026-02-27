package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tiancaiamao/ai/pkg/agent"
	"github.com/tiancaiamao/ai/pkg/compact"
	"github.com/tiancaiamao/ai/pkg/llm"
)

// CompactHistoryTool allows LLM to compact conversation history and tool outputs
type CompactHistoryTool struct {
	mu           sync.RWMutex
	agentCtx     *agent.AgentContext
	compactor    *compact.Compactor
	model        llm.Model
	apiKey       string
	systemPrompt string
}

// NewCompactHistoryTool creates a new CompactHistoryTool
func NewCompactHistoryTool(agentCtx *agent.AgentContext, compactor *compact.Compactor, model llm.Model, apiKey, systemPrompt string) *CompactHistoryTool {
	return &CompactHistoryTool{
		agentCtx:     agentCtx,
		compactor:    compactor,
		model:        model,
		apiKey:       apiKey,
		systemPrompt: systemPrompt,
	}
}

// SetAgentContext updates the context pointer used by the tool.
// This is required when the runtime swaps session contexts.
func (t *CompactHistoryTool) SetAgentContext(agentCtx *agent.AgentContext) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.agentCtx = agentCtx
}

func (t *CompactHistoryTool) getAgentContext() *agent.AgentContext {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.agentCtx
}

// Name returns the tool name
func (t *CompactHistoryTool) Name() string {
	return "compact_history"
}

// Description returns the tool description
func (t *CompactHistoryTool) Description() string {
	return `Compact conversation history and tool outputs to manage context.

Usage:
{
  "target": "conversation" | "tools" | "all",
  "strategy": "summarize" | "archive",
  "keep_recent": 5,
  "archive_to": "working-memory/detail/session-summary.md"
}

Parameters:
- target: what to compact
  - "conversation": compact conversation history (user/assistant messages)
  - "tools": compact tool outputs (often large, lose value over time)
  - "all": compact both
- strategy: "summarize" creates a summary, "archive" moves to detail file
  - if omitted: auto-select (archive for conversation/all when working memory is available; otherwise summarize)
- keep_recent: number of recent items to preserve (default 5)
- archive_to: where to save the summary (optional, defaults to auto-generated name)

When to use:
- context_meta shows tokens > 20%: light compression (remove redundant tool outputs)
- context_meta shows tokens > 40%: medium compression (archive old discussions)
- context_meta shows tokens > 60%: heavy compression (keep only essentials)
- Always preserve: recent 3-5 turns, current task, key decisions

Returns:
- summary of what was compacted and current token status
- memory_sync_required: whether you must update overview.md now
- overview_update_hint/detail_refs/post_actions: follow-up actions for memory sync`
}

// Parameters returns the JSON Schema for the tool parameters
func (t *CompactHistoryTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"target": map[string]any{
				"type":        "string",
				"enum":        []string{"conversation", "tools", "all"},
				"description": "what to compact: conversation, tools, or all",
			},
			"strategy": map[string]any{
				"type":        "string",
				"enum":        []string{"summarize", "archive"},
				"description": "how to compact: summarize or archive to file. If omitted, tool auto-selects based on target and working memory availability",
			},
			"keep_recent": map[string]any{
				"type":        "integer",
				"default":     5,
				"description": "number of recent items to preserve",
			},
			"archive_to": map[string]any{
				"type":        "string",
				"description": "where to save the summary (optional)",
			},
		},
		"required": []string{"target"},
	}
}

// Execute executes the tool with the given arguments
func (t *CompactHistoryTool) Execute(ctx context.Context, args map[string]any) ([]agent.ContentBlock, error) {
	// Parse parameters
	target, ok := args["target"].(string)
	if !ok {
		return nil, fmt.Errorf("target parameter is required and must be a string")
	}

	// Validate target
	if target != "conversation" && target != "tools" && target != "all" {
		return nil, fmt.Errorf("invalid target '%s': must be 'conversation', 'tools', or 'all'", target)
	}

	strategy := "summarize"
	strategyProvided := false
	if s, ok := args["strategy"].(string); ok {
		strategy = s
		strategyProvided = true
	} else {
		strategy = t.defaultStrategy(target)
	}
	if strategy != "summarize" && strategy != "archive" {
		return nil, fmt.Errorf("invalid strategy '%s': must be 'summarize' or 'archive'", strategy)
	}

	keepRecent := 5
	switch k := args["keep_recent"].(type) {
	case float64:
		keepRecent = int(k)
	case int:
		keepRecent = k
	case int64:
		keepRecent = int(k)
	case json.Number:
		if parsed, err := k.Int64(); err == nil {
			keepRecent = int(parsed)
		}
	}
	if keepRecent < 0 {
		return nil, fmt.Errorf("keep_recent must be >= 0")
	}

	archiveTo := ""
	if a, ok := args["archive_to"].(string); ok {
		archiveTo = a
	}

	// Execute compaction
	result := t.compact(ctx, target, strategy, keepRecent, archiveTo)
	if !strategyProvided && strategy == "archive" {
		result.Summary = strings.TrimSpace(result.Summary + "\n- Strategy auto-selected: archive (working memory detected)")
	}

	// Return result as JSON
	resultJSON, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}

	return []agent.ContentBlock{
		agent.TextContent{
			Type: "text",
			Text: string(resultJSON),
		},
	}, nil
}

// CompactResult represents the result of compaction
type CompactResult struct {
	Target             string         `json:"target"`
	Compacted          map[string]int `json:"compacted"`
	KeptRecent         int            `json:"kept_recent"`
	TokenStatus        TokenStatus    `json:"token_status"`
	ArchivedTo         string         `json:"archived_to,omitempty"`
	Summary            string         `json:"summary,omitempty"`
	MemorySyncRequired bool           `json:"memory_sync_required"`
	MemorySyncReason   string         `json:"memory_sync_reason,omitempty"`
	OverviewUpdateHint string         `json:"overview_update_hint,omitempty"`
	DetailRefs         []string       `json:"detail_refs,omitempty"`
	PostActions        []string       `json:"post_actions,omitempty"`
}

// TokenStatus represents token usage status
type TokenStatus struct {
	Before  int     `json:"before"`
	After   int     `json:"after"`
	Percent float64 `json:"percent"`
}

// compact performs the actual compaction
func (t *CompactHistoryTool) compact(ctx context.Context, target, strategy string, keepRecent int, archiveTo string) *CompactResult {
	result := &CompactResult{
		Target:      target,
		Compacted:   make(map[string]int),
		KeptRecent:  keepRecent,
		PostActions: make([]string, 0, 3),
		TokenStatus: TokenStatus{
			Before:  0,
			After:   0,
			Percent: 0.0,
		},
	}

	// Handle nil agent context
	agentCtx := t.getAgentContext()
	if agentCtx == nil {
		result.Summary = "Cannot compact: no agent context available"
		return result
	}

	// Handle nil compactor
	if t.compactor == nil {
		result.Summary = "Cannot compact: no compactor available"
		return result
	}

	result.TokenStatus.Before = t.compactor.EstimateContextTokens(agentCtx.Messages)

	messages := agentCtx.Messages
	if len(messages) <= keepRecent {
		result.Summary = "Not enough messages to compact"
		return result
	}

	switch target {
	case "conversation":
		compacted, summary := t.compactConversation(ctx, messages, keepRecent, strategy)
		result.Compacted["conversation"] = compacted
		if summary != "" {
			result.Summary = summary
		}

	case "tools":
		compacted := t.compactToolOutputs(messages, keepRecent)
		result.Compacted["tools"] = compacted

	case "all":
		compactedConv, summary := t.compactConversation(ctx, messages, keepRecent, strategy)
		compactedTools := t.compactToolOutputs(messages, keepRecent)
		result.Compacted["conversation"] = compactedConv
		result.Compacted["tools"] = compactedTools
		if summary != "" {
			result.Summary = summary
		}
	}

	// Generate default summary if not provided
	if result.Summary == "" {
		result.Summary = t.generateSummary(result)
	}

	// Persist archive when strategy requires it.
	if strategy == "archive" {
		if archivedPath, err := t.archiveResult(result, archiveTo, agentCtx); err != nil {
			result.Summary = strings.TrimSpace(result.Summary + "\n- Archive failed: " + err.Error())
		} else {
			result.ArchivedTo = archivedPath
			result.Summary = strings.TrimSpace(result.Summary + "\n- Archived to: " + archivedPath)
		}
	}

	// Update token status
	result.TokenStatus.After = t.compactor.EstimateContextTokens(agentCtx.Messages)
	contextWindow := t.compactor.ContextWindow()
	if contextWindow <= 0 {
		contextWindow = 128000
	}
	result.TokenStatus.Percent = float64(result.TokenStatus.After) / float64(contextWindow) * 100
	t.populateMemorySyncGuidance(result, strategy)

	return result
}

// compactConversation compacts conversation messages using the Compactor
func (t *CompactHistoryTool) compactConversation(ctx context.Context, messages []agent.AgentMessage, keepRecent int, strategy string) (int, string) {
	if len(messages) <= keepRecent {
		return 0, ""
	}

	// Use the existing Compactor to compact messages
	// The Compactor will handle generating summaries and managing the compaction
	lastSummary := ""
	if currentCtx := t.getAgentContext(); currentCtx != nil {
		lastSummary = currentCtx.LastCompactionSummary
	}
	result, err := t.compactor.Compact(messages, lastSummary)
	if err != nil {
		// Fallback: just count messages that would be compacted
		compacted := 0
		for i := 0; i < len(messages)-keepRecent; i++ {
			if messages[i].Role == "user" || messages[i].Role == "assistant" {
				compacted++
			}
		}
		return compacted, fmt.Sprintf("Compaction attempted but encountered error: %v", err)
	}

	// Update agent context with compacted messages
	if result != nil && len(result.Messages) > 0 {
		if ctx := t.getAgentContext(); ctx != nil {
			ctx.Messages = result.Messages
			if result.Summary != "" {
				ctx.LastCompactionSummary = result.Summary
			}
		}
		return len(messages) - len(result.Messages), result.Summary
	}

	return 0, ""
}

// compactToolOutputs compacts tool outputs by summarizing old tool result messages
func (t *CompactHistoryTool) compactToolOutputs(messages []agent.AgentMessage, keepRecent int) int {
	if len(messages) <= keepRecent {
		return 0
	}

	compacted := 0

	// Process all messages except recent ones
	for i := 0; i < len(messages)-keepRecent; i++ {
		msg := messages[i]

		// Check if this is a tool result message
		if msg.Role == "toolResult" {
			// Look for text content blocks
			for j, block := range msg.Content {
				if textContent, ok := block.(agent.TextContent); ok {
					// Truncate large tool outputs (simple heuristic for now)
					// In a more sophisticated implementation, this would use the LLM to summarize
					if len(textContent.Text) > 2000 {
						// Create a truncated version
						truncated := textContent.Text
						if len(truncated) > 500 {
							truncated = truncated[:500] + "\n\n... [Tool output truncated for context management. Original length: " + fmt.Sprintf("%d", len(textContent.Text)) + " chars]"
						}

						// Update the content
						textContent.Text = truncated
						msg.Content[j] = textContent
						compacted++
					}
				}
			}

			// Update the message in the slice
			messages[i] = msg
		}
	}

	// Update agent context
	if ctx := t.getAgentContext(); ctx != nil {
		ctx.Messages = messages
	}

	return compacted
}

// generateSummary generates a human-readable summary
func (t *CompactHistoryTool) generateSummary(result *CompactResult) string {
	var parts []string

	parts = append(parts, fmt.Sprintf("Compaction complete for target: %s", result.Target))

	if result.Compacted["conversation"] > 0 {
		parts = append(parts, fmt.Sprintf("- Compacted %d conversation messages", result.Compacted["conversation"]))
	}

	if result.Compacted["tools"] > 0 {
		parts = append(parts, fmt.Sprintf("- Compacted %d tool outputs", result.Compacted["tools"]))
	}

	parts = append(parts, fmt.Sprintf("- Kept %d recent items", result.KeptRecent))

	if result.ArchivedTo != "" {
		parts = append(parts, fmt.Sprintf("- Archived to: %s", result.ArchivedTo))
	}

	return strings.Join(parts, "\n")
}

func (t *CompactHistoryTool) defaultStrategy(target string) string {
	agentCtx := t.getAgentContext()
	if agentCtx == nil || agentCtx.WorkingMemory == nil {
		return "summarize"
	}
	if target == "conversation" || target == "all" {
		return "archive"
	}
	return "summarize"
}

func (t *CompactHistoryTool) populateMemorySyncGuidance(result *CompactResult, strategy string) {
	if result == nil {
		return
	}

	compactedConversation := result.Compacted["conversation"]
	compactedTools := result.Compacted["tools"]
	totalCompacted := compactedConversation + compactedTools
	if totalCompacted <= 0 && strings.TrimSpace(result.ArchivedTo) == "" {
		return
	}

	result.MemorySyncRequired = true
	result.MemorySyncReason = "context changed by compaction; synchronize working memory now"

	hintParts := make([]string, 0, 6)
	hintParts = append(hintParts, "Update overview.md in this same turn.")
	if compactedConversation > 0 {
		hintParts = append(hintParts, fmt.Sprintf("Record conversation compaction result (%d item(s)).", compactedConversation))
	}
	if compactedTools > 0 {
		hintParts = append(hintParts, fmt.Sprintf("Record tool-output compaction result (%d item(s)).", compactedTools))
	}
	if strings.TrimSpace(result.ArchivedTo) != "" {
		hintParts = append(hintParts, "Add archive reference so it can be reopened later.")
		result.DetailRefs = append(result.DetailRefs, result.ArchivedTo)
	}
	if strategy == "archive" {
		hintParts = append(hintParts, "Keep overview concise; move details to detail/ and keep only pointers.")
	}
	result.OverviewUpdateHint = strings.Join(hintParts, " ")

	result.PostActions = append(result.PostActions, "update_overview_now")
	if strings.TrimSpace(result.ArchivedTo) != "" {
		result.PostActions = append(result.PostActions, "record_archive_reference")
		result.PostActions = append(result.PostActions, "read_detail_on_demand")
	}
}

func (t *CompactHistoryTool) archiveResult(result *CompactResult, archiveTo string, agentCtx *agent.AgentContext) (string, error) {
	archivePath := t.resolveArchivePath(archiveTo, agentCtx)
	if strings.TrimSpace(archivePath) == "" {
		return "", fmt.Errorf("unable to resolve archive path")
	}

	content := t.buildArchiveContent(result)
	if err := os.MkdirAll(filepath.Dir(archivePath), 0755); err != nil {
		return "", fmt.Errorf("create archive directory: %w", err)
	}
	if err := os.WriteFile(archivePath, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("write archive file: %w", err)
	}
	return archivePath, nil
}

func (t *CompactHistoryTool) resolveArchivePath(archiveTo string, agentCtx *agent.AgentContext) string {
	archiveTo = strings.TrimSpace(archiveTo)
	detailDir := ""
	sessionDir := ""
	if agentCtx != nil && agentCtx.WorkingMemory != nil {
		detailDir = agentCtx.WorkingMemory.GetDetailDir()
		overviewPath := agentCtx.WorkingMemory.GetPath()
		if overviewPath != "" {
			// overviewPath: <session>/working-memory/overview.md
			sessionDir = filepath.Dir(filepath.Dir(overviewPath))
		}
	}

	if archiveTo == "" {
		baseDir := detailDir
		if baseDir == "" {
			baseDir = "."
		}
		filename := fmt.Sprintf("compact-%s.md", time.Now().UTC().Format("20060102-150405"))
		return filepath.Join(baseDir, filename)
	}

	if filepath.IsAbs(archiveTo) {
		return filepath.Clean(archiveTo)
	}

	clean := filepath.Clean(archiveTo)
	if strings.HasPrefix(clean, "working-memory"+string(filepath.Separator)) && sessionDir != "" {
		return filepath.Join(sessionDir, clean)
	}
	if detailDir != "" {
		return filepath.Join(detailDir, clean)
	}
	return clean
}

func (t *CompactHistoryTool) buildArchiveContent(result *CompactResult) string {
	var b strings.Builder
	b.WriteString("# Context Archive\n\n")
	b.WriteString(fmt.Sprintf("- CreatedAt: %s\n", time.Now().UTC().Format(time.RFC3339)))
	b.WriteString(fmt.Sprintf("- Target: %s\n", result.Target))
	b.WriteString(fmt.Sprintf("- KeepRecent: %d\n", result.KeptRecent))
	b.WriteString(fmt.Sprintf("- CompactedConversation: %d\n", result.Compacted["conversation"]))
	b.WriteString(fmt.Sprintf("- CompactedTools: %d\n", result.Compacted["tools"]))
	b.WriteString("\n## Summary\n\n")
	b.WriteString(strings.TrimSpace(result.Summary))
	b.WriteString("\n")
	return b.String()
}
