package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	truncpkg "github.com/tiancaiamao/ai/pkg/truncate"
)

func ParseEvent(line string) *FormattedEvent {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}

	var evt map[string]any
	if err := json.Unmarshal([]byte(line), &evt); err != nil {
		return nil
	}

	eventType, _ := evt["type"].(string)

	switch eventType {
	case "message_start":
		return parseMessageStart(evt)
	case "message_end":
		return parseMessageEnd(evt)
	case "message_update":
		return parseMessageUpdate(evt)
	case "thinking_delta":
		return parseThinkingDelta(evt)
	case "text_delta":
		return parseTextDelta(evt)
	case "tool_execution_start":
		return parseToolExecutionStart(evt)
	case "tool_execution_end":
		return parseToolExecutionEnd(evt)
	case "agent_start":
		return &FormattedEvent{Kind: KindMeta, Role: "ai", Text: "ai: agent started"}
	case "agent_end":
		return parseAgentEnd(evt)
	case "turn_start":
		return nil // silent
	case "turn_end":
		return nil // silent
	case "compaction_start":
		return parseCompactionStart(evt)
	case "compaction_end":
		return parseCompactionEnd(evt)
	case "error":
		errMsg, _ := evt["error"].(string)
		if errMsg == "" {
			errMsg = "unknown error"
		}
		return &FormattedEvent{Kind: KindMeta, Role: "ai", Text: "ai: error: " + errMsg}
	case "response":
		return parseResponseEvent(evt)
	case "session_switch":
		return parseSessionSwitch(evt)
	case "llm_retry":
		return parseLLMRetry(evt)
	case "loop_guard_triggered":
		return parseLoopGuard(evt)
	case "tool_call_recovery":
		return parseToolCallRecovery(evt)
	default:
		return nil
	}
}

// ExtractTextDelta extracts just the text delta from a message_update event.
// Returns empty string if the event has no text content.
func ExtractTextDelta(evt map[string]any) string {
	// Format 1: assistantMessageEvent.delta (actual ai RPC output)
	if ame, ok := evt["assistantMessageEvent"].(map[string]any); ok {
		ameType, _ := ame["type"].(string)
		if ameType == "text_delta" || ameType == "text_start" {
			delta, _ := ame["delta"].(string)
			return delta
		}
	}

	// Format 2: data.text_delta (legacy)
	if data, ok := evt["data"].(map[string]any); ok {
		delta, _ := data["text_delta"].(string)
		return delta
	}

	return ""
}

// ExtractToolName extracts the tool name from a tool_execution_start event.
func ExtractToolName(evt map[string]any) string {
	// Format 1: top-level toolName (actual ai RPC)
	toolName, _ := evt["toolName"].(string)
	if toolName != "" {
		return toolName
	}
	// Format 1b: top-level tool_name (alternate casing)
	toolName, _ = evt["tool_name"].(string)
	if toolName != "" {
		return toolName
	}
	// Format 2: data.tool (legacy)
	data, _ := evt["data"].(map[string]any)
	toolName, _ = data["tool"].(string)
	return toolName
}

func parseMessageUpdate(evt map[string]any) *FormattedEvent {
	// Format 1: assistantMessageEvent (actual ai RPC output)
	if ame, ok := evt["assistantMessageEvent"].(map[string]any); ok {
		ameType, _ := ame["type"].(string)
		delta, _ := ame["delta"].(string)
		switch ameType {
		case "text_delta":
			if delta == "" {
				return nil
			}
			return &FormattedEvent{
				Kind: KindText,
				Role: "assistant",
				Text: delta,
				Raw:  delta,
			}
		case "thinking_delta":
			if strings.TrimSpace(delta) == "" {
				return nil
			}
			return &FormattedEvent{
				Kind: KindThinking,
				Role: "thinking",
				Text: delta,
				Raw:  delta,
			}
		}
	}

	// Format 2: data.text_delta (legacy)
	if data, ok := evt["data"].(map[string]any); ok {
		delta, _ := data["text_delta"].(string)
		if delta == "" {
			return nil
		}
		return &FormattedEvent{
			Kind: KindText,
			Role: "assistant",
			Text: delta,
			Raw:  delta,
		}
	}

	return nil
}

// parseThinkingDelta handles standalone thinking_delta events.
// parseMessageStart handles message_start events.
// User messages are displayed on message_end, not here.
// Assistant/thinking messages will be streamed via message_update/text_delta/thinking_delta.
func parseMessageStart(evt map[string]any) *FormattedEvent {
	return nil // silent — reset stream state
}

// parseMessageEnd handles message_end events.
// For user messages (no prior streaming), render the full content with "user:" prefix.
// For assistant messages (already streamed via deltas), this is silent.
func parseMessageEnd(evt map[string]any) *FormattedEvent {
	msg, _ := evt["message"].(map[string]any)
	if msg == nil {
		return nil
	}

	role, _ := msg["role"].(string)

	// Only render user messages — assistant/thinking/tool already streamed via deltas
	if role != "user" {
		return nil
	}

	// Extract text content
	content, _ := msg["content"].([]any)
	var text string
	for _, item := range content {
		block, _ := item.(map[string]any)
		blockType, _ := block["type"].(string)
		if blockType == "text" {
			t, _ := block["text"].(string)
			text += t
		}
	}

	if text == "" {
		return nil
	}

	return &FormattedEvent{
		Kind: KindText,
		Role: "user",
		Text: text,
	}
}

func parseThinkingDelta(evt map[string]any) *FormattedEvent {
	delta, _ := evt["delta"].(string)
	if strings.TrimSpace(delta) == "" {
		return nil
	}
	return &FormattedEvent{
		Kind: KindThinking,
		Role: "thinking",
		Text: delta,
		Raw:  delta,
	}
}

// parseTextDelta handles standalone text_delta events.
func parseTextDelta(evt map[string]any) *FormattedEvent {
	delta, _ := evt["delta"].(string)
	if delta == "" {
		return nil
	}
	return &FormattedEvent{
		Kind: KindText,
		Role: "assistant",
		Text: delta,
		Raw:  delta,
	}
}

func parseToolExecutionStart(evt map[string]any) *FormattedEvent {
	toolName := ExtractToolName(evt)
	if toolName == "" {
		return nil
	}

	detail := formatToolDetail(evt, toolName)

	label := "tool"
	if toolName != "" {
		label = fmt.Sprintf("tool %s", toolName)
	}

	text := fmt.Sprintf("tool: %s start", label)
	if detail != "" {
		text = fmt.Sprintf("tool: %s start (%s)", label, strings.TrimSpace(detail))
	}

	return &FormattedEvent{
		Kind:   KindTool,
		Role:   "tool",
		Text:   text,
		Raw:    "",
		Tool:   toolName,
		Detail: detail,
	}
}

// parseToolExecutionEnd handles tool_execution_end events.
func parseToolExecutionEnd(evt map[string]any) *FormattedEvent {
	toolName := ExtractToolName(evt)

	label := "tool"
	if toolName != "" {
		label = fmt.Sprintf("tool %s", toolName)
	}

	isError, _ := evt["isError"].(bool)
	if isError {
		result, _ := evt["result"].(string)
		if result == "" {
			result = "error"
		}
		return &FormattedEvent{
			Kind: KindTool,
			Role: "tool",
			Text: fmt.Sprintf("tool: %s error: %s", label, truncpkg.TruncateString(result, 200)),
		}
	}

	return &FormattedEvent{
		Kind: KindTool,
		Role: "tool",
		Text: fmt.Sprintf("tool: %s done", label),
	}
}

func parseAgentEnd(evt map[string]any) *FormattedEvent {
	errMsg, _ := evt["error"].(string)
	if errMsg != "" {
		return &FormattedEvent{Kind: KindMeta, Role: "ai", Text: "ai: agent failed: " + errMsg}
	}
	if success, ok := evt["success"].(bool); ok && !success {
		return &FormattedEvent{Kind: KindMeta, Role: "ai", Text: "ai: agent failed"}
	}
	return &FormattedEvent{Kind: KindMeta, Role: "ai", Text: "ai: agent done"}
}

func parseSessionSwitch(evt map[string]any) *FormattedEvent {
	sessionID, _ := evt["session"].(string)
	sessionName, _ := evt["sessionName"].(string)

	text := ""
	if sessionName != "" {
		text = fmt.Sprintf("--- session: %s (%s) ---", sessionName, sessionID)
	} else if sessionID != "" {
		text = fmt.Sprintf("--- session: %s ---", sessionID)
	}
	if text == "" {
		return nil
	}
	return &FormattedEvent{Kind: KindSessionSwitch, Role: "ai", Text: text}
}

// parseCompactionStart handles compaction_start events.
func parseCompactionStart(evt map[string]any) *FormattedEvent {
	info, _ := evt["info"].(map[string]any)
	label := "compaction"
	if auto, _ := info["auto"].(bool); auto {
		label = "auto-compaction"
	}

	before := intFromMap(info, "before")
	if before > 0 {
		return &FormattedEvent{Kind: KindMeta, Role: "ai", Text: fmt.Sprintf("ai: %s started (%d messages)", label, before)}
	}
	return &FormattedEvent{Kind: KindMeta, Role: "ai", Text: fmt.Sprintf("ai: %s started", label)}
}

// parseCompactionEnd handles compaction_end events.
func parseCompactionEnd(evt map[string]any) *FormattedEvent {
	info, _ := evt["info"].(map[string]any)
	label := "compaction"
	if auto, _ := info["auto"].(bool); auto {
		label = "auto-compaction"
	}

	// Check for error
	if errStr, _ := info["error"].(string); errStr != "" {
		return &FormattedEvent{Kind: KindMeta, Role: "ai", Text: fmt.Sprintf("ai: %s failed: %s", label, errStr)}
	}

	// Mini compaction (context management)
	compType, _ := info["type"].(string)
	if compType == "mini" {
		truncated := intFromMap(info, "truncatedCount")
		tokensBefore := intFromMap(info, "tokensBefore")
		tokensAfter := intFromMap(info, "tokensAfter")
		llmUpdated, _ := info["llmContextUpdated"].(bool)

		msg := fmt.Sprintf("ai: %s done ", label)
		if truncated > 0 {
			msg += fmt.Sprintf("(%d messages truncated", truncated)
			if tokensBefore > 0 && tokensAfter > 0 {
				msg += fmt.Sprintf(", %d -> %d tokens", tokensBefore, tokensAfter)
			}
			msg += ")"
			if llmUpdated {
				msg += " (LLM context updated)"
			}
		} else {
			msg += "(no action needed)"
		}
		return &FormattedEvent{Kind: KindMeta, Role: "ai", Text: msg}
	}

	// Major compaction
	before := intFromMap(info, "before")
	after := intFromMap(info, "after")
	if before > 0 && after > 0 {
		return &FormattedEvent{Kind: KindMeta, Role: "ai", Text: fmt.Sprintf("ai: %s done (%d -> %d messages)", label, before, after)}
	}

	return &FormattedEvent{Kind: KindMeta, Role: "ai", Text: fmt.Sprintf("ai: %s done", label)}
}

// parseLoopGuard handles loop_guard_triggered events.
func parseLoopGuard(evt map[string]any) *FormattedEvent {
	reason := ""
	if lg, ok := evt["loopGuard"].(map[string]any); ok {
		reason, _ = lg["reason"].(string)
	}
	if reason == "" {
		reason, _ = evt["reason"].(string)
	}
	if reason == "" {
		reason = "unknown"
	}
	return &FormattedEvent{Kind: KindMeta, Role: "ai", Text: "ai: loop guard triggered: " + reason}
}

// parseToolCallRecovery handles tool_call_recovery events.
func parseToolCallRecovery(evt map[string]any) *FormattedEvent {
	reason := "malformed tool-call markup"
	if r, _ := evt["reason"].(string); r != "" {
		reason = r
	}
	attempt := intFromMap(evt, "attempt")
	if attempt > 0 {
		return &FormattedEvent{Kind: KindMeta, Role: "ai", Text: fmt.Sprintf("ai: recovered malformed tool call (attempt %d): %s", attempt, truncpkg.TruncateString(reason, 220))}
	}
	return &FormattedEvent{Kind: KindMeta, Role: "ai", Text: "ai: recovered malformed tool call: " + truncpkg.TruncateString(reason, 220)}
}

// parseLLMRetry handles llm_retry events, making rate-limit and other
// transient LLM errors visible to watchers.
func parseLLMRetry(evt map[string]any) *FormattedEvent {
	info, _ := evt["llmRetry"].(map[string]any)
	if info == nil {
		return nil
	}
	attempt := intFromMap(info, "attempt")
	maxRetries := intFromMap(info, "maxRetries")
	delayNs := intFromMap(info, "delay")
	errorType, _ := info["errorType"].(string)
	errMsg, _ := info["error"].(string)

	if attempt <= 0 {
		return nil
	}

	delay := time.Duration(delayNs) * time.Nanosecond
	delayStr := delay.Round(time.Millisecond).String()
	if delay >= time.Second {
		delayStr = fmt.Sprintf("%.1fs", delay.Seconds())
	}

	label := errorType
	if label == "" {
		label = "unknown"
	}

	text := fmt.Sprintf("ai: LLM retry %d/%d (%s, waiting %s)",
		attempt, maxRetries, label, delayStr)
	if errMsg != "" {
		text += ": " + truncpkg.TruncateString(errMsg, 120)
	}

	return &FormattedEvent{Kind: KindMeta, Role: "ai", Text: text}
}

// formatToolDetail tries to extract a short summary of tool arguments.
func formatToolDetail(evt map[string]any, toolName string) string {
	// Try data.args or top-level args
	var args map[string]any
	if data, ok := evt["data"].(map[string]any); ok {
		args, _ = data["args"].(map[string]any)
	}
	if args == nil {
		args, _ = evt["args"].(map[string]any)
	}
	if args == nil {
		return ""
	}

	// Pick the most relevant argument based on common tools
	parts := make([]string, 0, 2)
	for _, key := range []string{"path", "file", "command", "pattern", "query", "url"} {
		if v, ok := args[key]; ok {
			parts = append(parts, fmt.Sprintf("%s=%v", key, v))
		}
	}
	if len(parts) > 0 {
		return " " + strings.Join(parts, " ")
	}
	return ""
}

// intFromMap safely extracts an int from a map[string]any.
func intFromMap(m map[string]any, key string) int {
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	}
	return 0
}

// parseResponseEvent handles RPC response events from slash commands.
