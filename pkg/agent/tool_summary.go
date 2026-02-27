package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tiancaiamao/ai/pkg/llm"
	agentprompt "github.com/tiancaiamao/ai/pkg/prompt"
	traceevent "github.com/tiancaiamao/ai/pkg/traceevent"
)

const (
	toolSummaryInputMaxChars  = 12000
	toolSummaryOutputMaxRunes = 1200

	toolSummaryBatchPerItemMaxChars = 4000
	toolSummaryBatchInputMaxChars   = 24000
	toolSummaryBatchOutputMaxRunes  = 2200
	toolSummaryThinkingLevel        = "low"
)

const toolSummarySystemPrompt = `You summarize tool execution output for continuation context in a coding agent.
Return concise factual notes only.
Do not invent information.
Do not add instructions or policy text.`

const toolSummaryUserPromptTemplate = `Summarize this tool execution result for future turns.

Format:
- Tool: <name>
- Status: <ok|error>
- Key facts: <2-5 bullets>
- Important artifacts: <files, symbols, commands, errors>

Tool: %s
Call ID: %s
Status: %s
Output:
%s`

const toolSummaryBatchSystemPrompt = `You summarize multiple tool execution outputs for continuation context in a coding agent.
Return concise factual notes only.
Do not invent information.
Do not add instructions or policy text.`

const toolSummaryBatchUserPromptTemplate = `Summarize these tool execution results for future turns.

Requirements:
- Produce one bullet per tool call.
- Include call ID and tool name in each bullet.
- Include status (ok/error) and critical facts.
- Mention important artifacts (files, symbols, commands, errors) when present.

Tool results:
%s`

func maybeSummarizeToolResults(
	ctx context.Context,
	agentCtx *AgentContext,
	config *LoopConfig,
) {
	if !shouldAutoSummarizeToolResults(agentCtx, config) {
		return
	}
	strategy := normalizeToolSummaryStrategy(config.ToolSummaryStrategy)
	if strategy == "off" {
		return
	}

	for {
		count := countVisibleToolResults(agentCtx.Messages)
		if count <= config.ToolCallCutoff {
			return
		}

		idx := findOldestVisibleToolResult(agentCtx.Messages)
		if idx < 0 {
			return
		}

		original := agentCtx.Messages[idx]
		summarySpan := traceevent.StartSpan(ctx, "tool_summary", traceevent.CategoryTool,
			traceevent.Field{Key: "mode", Value: "single"},
			traceevent.Field{Key: "strategy", Value: strategy},
			traceevent.Field{Key: "tool", Value: strings.TrimSpace(original.ToolName)},
			traceevent.Field{Key: "tool_call_id", Value: strings.TrimSpace(original.ToolCallID)},
		)
		summary := ""
		fallback := false
		if strategy == "heuristic" {
			summary = fallbackToolSummary(original)
			fallback = true
		} else {
			text, err := summarizeToolResultFn(ctx, config.Model, config.APIKey, original)
			if err != nil {
				summary = fallbackToolSummary(original)
				fallback = true
				summarySpan.AddField("llm_error", err.Error())
			} else {
				summary = text
			}
		}
		summarySpan.AddField("fallback", fallback)
		summarySpan.AddField("summary_chars", len([]rune(summary)))

		agentCtx.Messages[idx] = archiveToolResult(original)
		agentCtx.Messages = append(agentCtx.Messages, newToolSummaryMessage(original, summary))
		summarySpan.End()
	}
}

func normalizeToolSummaryStrategy(strategy string) string {
	switch strings.ToLower(strings.TrimSpace(strategy)) {
	case "", "llm":
		return "llm"
	case "heuristic":
		return "heuristic"
	case "off":
		return "off"
	default:
		return "llm"
	}
}

func normalizeToolSummaryAutomation(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "":
		return "off"
	case "fallback":
		return "fallback"
	case "off":
		return "off"
	case "always":
		return "always"
	default:
		return "off"
	}
}

func shouldAutoSummarizeToolResults(agentCtx *AgentContext, config *LoopConfig) bool {
	if agentCtx == nil || config == nil || config.ToolCallCutoff <= 0 {
		return false
	}
	switch normalizeToolSummaryAutomation(config.ToolSummaryAutomation) {
	case "off":
		return false
	case "fallback":
		if config.Compactor == nil {
			return false
		}
		return config.Compactor.ShouldCompact(agentCtx.Messages)
	default:
		return true
	}
}

func summarizeToolResultWithLLM(
	ctx context.Context,
	model llm.Model,
	apiKey string,
	result AgentMessage,
) (string, error) {
	if strings.TrimSpace(apiKey) == "" {
		return "", errors.New("empty API key")
	}

	status := "ok"
	if result.IsError {
		status = "error"
	}

	raw := strings.TrimSpace(result.ExtractText())
	if raw == "" {
		raw = "(empty output)"
	}
	raw = trimTextWithTail(raw, toolSummaryInputMaxChars)

	prompt := fmt.Sprintf(
		toolSummaryUserPromptTemplate,
		strings.TrimSpace(result.ToolName),
		strings.TrimSpace(result.ToolCallID),
		status,
		raw,
	)

	summaryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	llmCtx := llm.LLMContext{
		SystemPrompt: toolSummarySystemPrompt + "\n" + agentprompt.ThinkingInstruction(toolSummaryThinkingLevel),
		Messages: []llm.LLMMessage{
			{Role: "user", Content: prompt},
		},
	}

	stream := llm.StreamLLM(summaryCtx, model, llmCtx, apiKey)
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
		return "", errors.New("empty tool summary")
	}
	out = trimRunes(out, toolSummaryOutputMaxRunes)
	return out, nil
}

func countVisibleToolResults(messages []AgentMessage) int {
	count := 0
	for _, msg := range messages {
		if msg.Role == "toolResult" && msg.IsAgentVisible() {
			count++
		}
	}
	return count
}

func findOldestVisibleToolResult(messages []AgentMessage) int {
	for i := 0; i < len(messages); i++ {
		msg := messages[i]
		if msg.Role == "toolResult" && msg.IsAgentVisible() {
			return i
		}
	}
	return -1
}

func archiveToolResult(msg AgentMessage) AgentMessage {
	archived := msg.WithVisibility(false, msg.IsUserVisible())
	return archived.WithKind("tool_result_archived")
}

func newToolSummaryMessage(result AgentMessage, summary string) AgentMessage {
	status := "ok"
	if result.IsError {
		status = "error"
	}

	text := fmt.Sprintf(
		"[Tool output summary]\nTool: %s\nCall ID: %s\nStatus: %s\n%s",
		strings.TrimSpace(result.ToolName),
		strings.TrimSpace(result.ToolCallID),
		status,
		strings.TrimSpace(summary),
	)

	return newToolSummaryContextMessage(text)
}

func fallbackToolSummary(result AgentMessage) string {
	status := "ok"
	if result.IsError {
		status = "error"
	}
	text := strings.TrimSpace(result.ExtractText())
	if text == "" {
		text = "(empty output)"
	}
	text = trimTextWithTail(text, 800)

	return fmt.Sprintf(
		"Tool %q finished with status %s. Output excerpt:\n%s",
		strings.TrimSpace(result.ToolName),
		status,
		text,
	)
}

func summarizeToolResultsBatchWithLLM(
	ctx context.Context,
	model llm.Model,
	apiKey string,
	results []AgentMessage,
) (string, error) {
	if len(results) == 0 {
		return "", errors.New("no tool results to summarize")
	}
	if strings.TrimSpace(apiKey) == "" {
		return "", errors.New("empty API key")
	}

	var b strings.Builder
	for i, result := range results {
		status := "ok"
		if result.IsError {
			status = "error"
		}
		raw := strings.TrimSpace(result.ExtractText())
		if raw == "" {
			raw = "(empty output)"
		}
		raw = trimTextWithTail(raw, toolSummaryBatchPerItemMaxChars)

		fmt.Fprintf(
			&b,
			"[%d]\nTool: %s\nCall ID: %s\nStatus: %s\nOutput:\n%s\n\n",
			i+1,
			strings.TrimSpace(result.ToolName),
			strings.TrimSpace(result.ToolCallID),
			status,
			raw,
		)
	}

	payload := trimTextWithTail(strings.TrimSpace(b.String()), toolSummaryBatchInputMaxChars)
	prompt := fmt.Sprintf(toolSummaryBatchUserPromptTemplate, payload)

	summaryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	llmCtx := llm.LLMContext{
		SystemPrompt: toolSummaryBatchSystemPrompt + "\n" + agentprompt.ThinkingInstruction(toolSummaryThinkingLevel),
		Messages: []llm.LLMMessage{
			{Role: "user", Content: prompt},
		},
	}

	stream := llm.StreamLLM(summaryCtx, model, llmCtx, apiKey)
	var out strings.Builder
	for event := range stream.Iterator(summaryCtx) {
		if event.Done {
			break
		}
		switch e := event.Value.(type) {
		case llm.LLMTextDeltaEvent:
			out.WriteString(e.Delta)
		case llm.LLMErrorEvent:
			return "", e.Error
		}
	}

	text := strings.TrimSpace(out.String())
	if text == "" {
		return "", errors.New("empty batch summary")
	}
	return trimRunes(text, toolSummaryBatchOutputMaxRunes), nil
}

func fallbackToolSummaryBatch(results []AgentMessage) string {
	lines := make([]string, 0, len(results))
	for _, result := range results {
		status := "ok"
		if result.IsError {
			status = "error"
		}
		name := strings.TrimSpace(result.ToolName)
		if name == "" {
			name = "unknown"
		}
		callID := strings.TrimSpace(result.ToolCallID)
		if callID == "" {
			callID = "n/a"
		}
		text := strings.TrimSpace(result.ExtractText())
		if text == "" {
			text = "(empty output)"
		}
		text = strings.ReplaceAll(text, "\n", " ")
		text = trimRunes(text, 220)
		lines = append(lines, fmt.Sprintf("- [%s] %s (%s): %s", callID, name, status, text))
	}
	if len(lines) == 0 {
		return "(no tool results)"
	}
	return strings.Join(lines, "\n")
}

func newToolBatchSummaryMessage(_ []AgentMessage, summary string) AgentMessage {
	// Changed format to not look like LLM output
	// This prevents the LLM from learning to imitate this format
	text := fmt.Sprintf("[ARCHIVED_TOOL_CONTEXT: %s]", strings.TrimSpace(summary))
	return newToolSummaryContextMessage(text)
}

func newToolSummaryContextMessage(text string) AgentMessage {
	msg := NewAssistantMessage()
	msg.Content = []ContentBlock{
		TextContent{Type: "text", Text: text},
	}
	return msg.WithVisibility(true, false).WithKind("tool_summary")
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

func trimTextWithTail(input string, maxRunes int) string {
	if maxRunes <= 0 {
		return input
	}
	runes := []rune(input)
	if len(runes) <= maxRunes {
		return input
	}
	head := maxRunes * 2 / 3
	tail := maxRunes - head
	if head < 1 {
		head = 1
	}
	if tail < 1 {
		tail = 1
	}
	return string(runes[:head]) + "\n... (truncated) ...\n" + string(runes[len(runes)-tail:])
}
