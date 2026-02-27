package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tiancaiamao/ai/pkg/agent"
	"github.com/tiancaiamao/ai/pkg/compact"
	"github.com/tiancaiamao/ai/pkg/llm"
	agentprompt "github.com/tiancaiamao/ai/pkg/prompt"
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

const (
	toolCompactionSummaryThinkingLevel   = "low"
	toolCompactionSummaryPerItemMaxRunes = 3000
	toolCompactionSummaryInputMaxRunes   = 24000
	toolCompactionSummaryOutputMaxRunes  = 2200
	toolCompactionFallbackSummaryMaxRows = 24
	toolCompactionSummaryRequestTimeout  = 30 * time.Second
)

const toolCompactionSummarySystemPrompt = `You summarize tool execution outputs for continuation context in a coding agent.
Return concise factual notes only.
Do not invent information.
Do not add instructions or policy text.`

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

func (t *CompactHistoryTool) getExecutionAgentContext(ctx context.Context) *agent.AgentContext {
	if current := agent.ToolExecutionAgentContext(ctx); current != nil {
		return current
	}
	return t.getAgentContext()
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
- strategy_selected/strategy_reason: which strategy was used and why
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
	strategyReason := "auto_default"
	if s, ok := args["strategy"].(string); ok {
		strategy = s
		strategyProvided = true
		strategyReason = "caller_provided"
	} else {
		strategy, strategyReason = t.defaultStrategyWithReason(target)
	}
	if strategy != "summarize" && strategy != "archive" {
		return nil, fmt.Errorf("invalid strategy '%s': must be 'summarize' or 'archive'", strategy)
	}

	keepRecent := 5
	rawKeepRecent, hasKeepRecent := args["keep_recent"]
	if !hasKeepRecent {
		rawKeepRecent = args["keepRecent"]
	}
	switch k := rawKeepRecent.(type) {
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
	case string:
		if parsed, err := strconv.Atoi(strings.TrimSpace(k)); err == nil {
			keepRecent = parsed
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
	result.StrategySelected = strategy
	result.StrategyReason = strategyReason
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
	StrategySelected   string         `json:"strategy_selected,omitempty"`
	StrategyReason     string         `json:"strategy_reason,omitempty"`
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
	agentCtx := t.getExecutionAgentContext(ctx)
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
		compacted, summary := t.compactConversation(ctx, agentCtx, messages, keepRecent, strategy)
		result.Compacted["conversation"] = compacted
		if summary != "" {
			result.Summary = summary
		}

	case "tools":
		compacted, summary := t.compactToolOutputs(ctx, agentCtx, messages, keepRecent)
		result.Compacted["tools"] = compacted
		if summary != "" {
			result.Summary = summary
		}

	case "all":
		compactedConv, summary := t.compactConversation(ctx, agentCtx, messages, keepRecent, strategy)
		if len(agentCtx.Messages) > 0 {
			messages = agentCtx.Messages
		}
		compactedTools, toolSummary := t.compactToolOutputs(ctx, agentCtx, messages, keepRecent)
		result.Compacted["conversation"] = compactedConv
		result.Compacted["tools"] = compactedTools
		switch {
		case summary != "" && toolSummary != "":
			result.Summary = strings.TrimSpace(summary + "\n\n" + toolSummary)
		case summary != "":
			result.Summary = summary
		case toolSummary != "":
			result.Summary = toolSummary
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
			if indexPath, err := t.updateDetailIndex(archivedPath, result, agentCtx); err != nil {
				result.Summary = strings.TrimSpace(result.Summary + "\n- Detail index update failed: " + err.Error())
			} else if strings.TrimSpace(indexPath) != "" {
				result.DetailRefs = append(result.DetailRefs, indexPath)
				result.Summary = strings.TrimSpace(result.Summary + "\n- Detail index updated: " + indexPath)
			}
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
func (t *CompactHistoryTool) compactConversation(ctx context.Context, agentCtx *agent.AgentContext, messages []agent.AgentMessage, keepRecent int, strategy string) (int, string) {
	if len(messages) <= keepRecent {
		return 0, ""
	}
	splitIndex := len(messages) - keepRecent
	if splitIndex <= 0 {
		return 0, ""
	}
	oldMessages := messages[:splitIndex]
	recentMessages := append([]agent.AgentMessage(nil), messages[splitIndex:]...)
	compactedConversation := countConversationMessages(oldMessages)

	// Build a dedicated compactor instance that respects keepRecent exactly.
	// The default compactor keeps a large recent-token window, which can make
	// explicit compact_history calls appear ineffective for short histories.
	conversationConfig := compact.DefaultConfig()
	conversationConfig.KeepRecent = keepRecent
	conversationConfig.KeepRecentTokens = 0
	conversationConfig.ReserveTokens = t.compactor.ReserveTokens()
	conversationCompactor := compact.NewCompactor(
		conversationConfig,
		t.model,
		t.apiKey,
		t.systemPrompt,
		t.compactor.ContextWindow(),
	)

	lastSummary := ""
	if agentCtx != nil {
		lastSummary = agentCtx.LastCompactionSummary
	}
	summary, err := conversationCompactor.GenerateSummaryWithPrevious(oldMessages, lastSummary)
	if err != nil || strings.TrimSpace(summary) == "" {
		summary = fallbackConversationSummary(oldMessages, err)
	}

	// Ensure compacted conversation remains protocol-safe if keep_recent starts
	// in the middle of tool call/result pairs.
	for len(recentMessages) > 0 && (recentMessages[0].Role == "toolResult" || recentMessages[0].Role == "tool") {
		recentMessages = recentMessages[1:]
	}

	newMessages := []agent.AgentMessage{
		agent.NewUserMessage(fmt.Sprintf("[Previous conversation summary]\n\n%s", summary)),
	}
	newMessages = append(newMessages, recentMessages...)

	if agentCtx != nil {
		agentCtx.Messages = newMessages
		agentCtx.LastCompactionSummary = summary
	}

	return compactedConversation, summary
}

// compactToolOutputs compacts tool outputs by summarizing old tool result messages
func (t *CompactHistoryTool) compactToolOutputs(ctx context.Context, agentCtx *agent.AgentContext, messages []agent.AgentMessage, keepRecent int) (int, string) {
	if len(messages) <= keepRecent {
		return 0, ""
	}

	indices := make([]int, 0, 8)
	results := make([]agent.AgentMessage, 0, 8)
	compactedToolCallIDs := make(map[string]struct{}, 8)
	for i := 0; i < len(messages)-keepRecent; i++ {
		msg := messages[i]
		if msg.Role != "toolResult" || !msg.IsAgentVisible() {
			continue
		}
		indices = append(indices, i)
		results = append(results, msg)
		callID := strings.TrimSpace(msg.ToolCallID)
		if callID != "" {
			compactedToolCallIDs[callID] = struct{}{}
		}
	}
	if len(indices) == 0 {
		return 0, ""
	}

	summary, fallbackUsed := t.summarizeToolOutputs(ctx, results)
	for _, idx := range indices {
		messages[idx] = archiveToolResult(messages[idx])
	}
	if len(compactedToolCallIDs) > 0 {
		for i := 0; i < len(messages)-keepRecent; i++ {
			msg := messages[i]
			if msg.Role != "assistant" || !msg.IsAgentVisible() {
				continue
			}
			updated, removed := stripCompactedToolCalls(msg, compactedToolCallIDs)
			if removed == 0 {
				continue
			}
			if len(updated.Content) == 0 {
				updated = updated.WithVisibility(false, updated.IsUserVisible()).WithKind("assistant_tool_calls_archived")
			}
			messages[i] = updated
		}
	}
	messages = append(messages, newToolSummaryContextMessage(summary))

	if agentCtx != nil {
		agentCtx.Messages = messages
	}

	header := fmt.Sprintf(
		"Tool-output context compacted: archived %d tool result(s), kept %d recent message(s).",
		len(indices),
		keepRecent,
	)
	if fallbackUsed {
		header += " (fallback summarizer used)"
	}
	return len(indices), strings.TrimSpace(header + "\n\n" + summary)
}

func (t *CompactHistoryTool) summarizeToolOutputs(ctx context.Context, results []agent.AgentMessage) (string, bool) {
	if len(results) == 0 {
		return "(no tool outputs)", false
	}

	if strings.TrimSpace(t.apiKey) == "" {
		return fallbackToolOutputsSummary(results), true
	}

	summary, err := t.summarizeToolOutputsWithLLM(ctx, results)
	if err != nil || strings.TrimSpace(summary) == "" {
		return fallbackToolOutputsSummary(results), true
	}
	return summary, false
}

func (t *CompactHistoryTool) summarizeToolOutputsWithLLM(ctx context.Context, results []agent.AgentMessage) (string, error) {
	if len(results) == 0 {
		return "", errors.New("no tool outputs to summarize")
	}
	if strings.TrimSpace(t.apiKey) == "" {
		return "", errors.New("empty API key")
	}

	prompt := buildToolOutputsSummaryPrompt(results)
	summaryCtx, cancel := context.WithTimeout(ctx, toolCompactionSummaryRequestTimeout)
	defer cancel()

	llmCtx := llm.LLMContext{
		SystemPrompt: toolCompactionSummarySystemPrompt + "\n" + agentprompt.ThinkingInstruction(toolCompactionSummaryThinkingLevel),
		Messages: []llm.LLMMessage{
			{Role: "user", Content: prompt},
		},
	}

	stream := llm.StreamLLM(summaryCtx, t.model, llmCtx, t.apiKey)
	var b strings.Builder
	for event := range stream.Iterator(summaryCtx) {
		if event.Done {
			break
		}
		switch e := event.Value.(type) {
		case llm.LLMTextDeltaEvent:
			b.WriteString(e.Delta)
		case llm.LLMErrorEvent:
			return "", e.Error
		}
	}

	out := strings.TrimSpace(b.String())
	if out == "" {
		return "", errors.New("empty summary")
	}
	return trimRunes(out, toolCompactionSummaryOutputMaxRunes), nil
}

func buildToolOutputsSummaryPrompt(results []agent.AgentMessage) string {
	var b strings.Builder
	b.WriteString("Summarize these tool execution outputs for future turns.\n\n")
	b.WriteString("Requirements:\n")
	b.WriteString("- One bullet per tool call.\n")
	b.WriteString("- Include call ID and tool name in each bullet.\n")
	b.WriteString("- Include status (ok/error) and key facts.\n")
	b.WriteString("- Mention important artifacts (files, symbols, commands, errors).\n\n")
	b.WriteString("Tool outputs:\n")

	for _, result := range results {
		status := "ok"
		if result.IsError {
			status = "error"
		}
		toolName := strings.TrimSpace(result.ToolName)
		if toolName == "" {
			toolName = "unknown"
		}
		callID := strings.TrimSpace(result.ToolCallID)
		if callID == "" {
			callID = "n/a"
		}
		output := strings.TrimSpace(result.ExtractText())
		if output == "" {
			output = "(empty output)"
		}
		output = trimRunes(output, toolCompactionSummaryPerItemMaxRunes)

		b.WriteString(fmt.Sprintf("Tool: %s\nCall ID: %s\nStatus: %s\nOutput:\n%s\n\n", toolName, callID, status, output))
		if utf8Len(b.String()) > toolCompactionSummaryInputMaxRunes {
			break
		}
	}

	return trimRunes(strings.TrimSpace(b.String()), toolCompactionSummaryInputMaxRunes)
}

func fallbackToolOutputsSummary(results []agent.AgentMessage) string {
	lines := make([]string, 0, len(results))
	for _, result := range results {
		status := "ok"
		if result.IsError {
			status = "error"
		}
		toolName := strings.TrimSpace(result.ToolName)
		if toolName == "" {
			toolName = "unknown"
		}
		callID := strings.TrimSpace(result.ToolCallID)
		if callID == "" {
			callID = "n/a"
		}
		output := strings.TrimSpace(result.ExtractText())
		if output == "" {
			output = "(empty output)"
		}
		output = strings.ReplaceAll(output, "\n", " ")
		output = trimRunes(output, 220)
		lines = append(lines, fmt.Sprintf("- [%s] %s (%s): %s", callID, toolName, status, output))
	}
	if len(lines) == 0 {
		return "(no tool outputs)"
	}
	if len(lines) > toolCompactionFallbackSummaryMaxRows {
		omitted := len(lines) - toolCompactionFallbackSummaryMaxRows
		lines = append(lines[:toolCompactionFallbackSummaryMaxRows], fmt.Sprintf("- ... omitted %d older tool output(s)", omitted))
	}
	return trimRunes(strings.Join(lines, "\n"), toolCompactionSummaryOutputMaxRunes)
}

func fallbackConversationSummary(messages []agent.AgentMessage, cause error) string {
	lines := make([]string, 0, 12)
	for _, msg := range messages {
		if !msg.IsAgentVisible() {
			continue
		}
		if msg.Role != "user" && msg.Role != "assistant" {
			continue
		}
		text := strings.TrimSpace(msg.ExtractText())
		if text == "" {
			continue
		}
		text = strings.ReplaceAll(text, "\n", " ")
		text = trimRunes(text, 160)
		lines = append(lines, fmt.Sprintf("- %s: %s", msg.Role, text))
		if len(lines) >= 10 {
			break
		}
	}
	if len(lines) == 0 {
		lines = append(lines, "- (no user/assistant text available)")
	}

	header := "Conversation compacted with fallback summary (LLM summarizer unavailable)."
	if cause != nil {
		header = header + " Cause: " + trimRunes(strings.TrimSpace(cause.Error()), 160)
	}
	return trimRunes(header+"\n"+strings.Join(lines, "\n"), toolCompactionSummaryOutputMaxRunes)
}

func countConversationMessages(messages []agent.AgentMessage) int {
	count := 0
	for _, msg := range messages {
		if msg.Role == "user" || msg.Role == "assistant" {
			count++
		}
	}
	return count
}

func stripCompactedToolCalls(msg agent.AgentMessage, compactedToolCallIDs map[string]struct{}) (agent.AgentMessage, int) {
	if msg.Role != "assistant" || len(compactedToolCallIDs) == 0 {
		return msg, 0
	}

	filtered := make([]agent.ContentBlock, 0, len(msg.Content))
	removed := 0
	for _, block := range msg.Content {
		toolCall, ok := block.(agent.ToolCallContent)
		if !ok {
			filtered = append(filtered, block)
			continue
		}
		callID := strings.TrimSpace(toolCall.ID)
		if callID == "" {
			filtered = append(filtered, block)
			continue
		}
		if _, exists := compactedToolCallIDs[callID]; exists {
			removed++
			continue
		}
		filtered = append(filtered, block)
	}
	if removed == 0 {
		return msg, 0
	}

	msg.Content = filtered
	return msg, removed
}

func newToolSummaryContextMessage(text string) agent.AgentMessage {
	msg := agent.NewAssistantMessage()
	msg.Content = []agent.ContentBlock{
		agent.TextContent{Type: "text", Text: strings.TrimSpace(text)},
	}
	return msg.WithVisibility(true, false).WithKind("tool_summary")
}

func archiveToolResult(msg agent.AgentMessage) agent.AgentMessage {
	return msg.WithVisibility(false, msg.IsUserVisible()).WithKind("tool_result_archived")
}

func trimRunes(input string, limit int) string {
	if limit <= 0 {
		return input
	}
	runes := []rune(input)
	if len(runes) <= limit {
		return input
	}
	return string(runes[:limit])
}

func utf8Len(input string) int {
	return len([]rune(input))
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
	strategy, _ := t.defaultStrategyWithReason(target)
	return strategy
}

func (t *CompactHistoryTool) defaultStrategyWithReason(target string) (string, string) {
	agentCtx := t.getAgentContext()
	if agentCtx == nil || agentCtx.WorkingMemory == nil {
		return "summarize", "working_memory_unavailable"
	}
	if target == "conversation" || target == "all" {
		return "archive", "working_memory_available_for_long_term_storage"
	}
	return "summarize", "tools_target_prefers_inline_summary"
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
		result.PostActions = append(result.PostActions, "refresh_detail_index")
	}
}

type detailIndex struct {
	Version int                `json:"version"`
	Entries []detailIndexEntry `json:"entries"`
}

type detailIndexEntry struct {
	CreatedAt             string `json:"created_at"`
	FilePath              string `json:"file_path"`
	Target                string `json:"target"`
	CompactedConversation int    `json:"compacted_conversation"`
	CompactedTools        int    `json:"compacted_tools"`
	SummaryPreview        string `json:"summary_preview"`
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

func (t *CompactHistoryTool) updateDetailIndex(archivePath string, result *CompactResult, agentCtx *agent.AgentContext) (string, error) {
	if agentCtx == nil || agentCtx.WorkingMemory == nil {
		return "", nil
	}

	detailDir := strings.TrimSpace(agentCtx.WorkingMemory.GetDetailDir())
	if detailDir == "" {
		return "", nil
	}
	indexPath := filepath.Join(detailDir, "index.json")

	idx := detailIndex{Version: 1, Entries: make([]detailIndexEntry, 0, 16)}
	if raw, err := os.ReadFile(indexPath); err == nil && len(raw) > 0 {
		_ = json.Unmarshal(raw, &idx)
		if idx.Version == 0 {
			idx.Version = 1
		}
		if idx.Entries == nil {
			idx.Entries = make([]detailIndexEntry, 0, 16)
		}
	}

	preview := strings.TrimSpace(result.Summary)
	if len(preview) > 180 {
		preview = preview[:180] + "..."
	}

	idx.Entries = append(idx.Entries, detailIndexEntry{
		CreatedAt:             time.Now().UTC().Format(time.RFC3339),
		FilePath:              archivePath,
		Target:                result.Target,
		CompactedConversation: result.Compacted["conversation"],
		CompactedTools:        result.Compacted["tools"],
		SummaryPreview:        preview,
	})
	if len(idx.Entries) > 200 {
		idx.Entries = idx.Entries[len(idx.Entries)-200:]
	}

	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal detail index: %w", err)
	}
	if err := os.WriteFile(indexPath, data, 0644); err != nil {
		return "", fmt.Errorf("write detail index: %w", err)
	}

	return indexPath, nil
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
