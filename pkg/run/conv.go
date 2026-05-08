package run

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/tiancaiamao/ai/pkg/rpc"
)

// EventKind classifies the type of formatted output.
type EventKind string

const (
	KindText          EventKind = "text"
	KindThinking      EventKind = "thinking"
	KindTool          EventKind = "tool"
	KindMeta          EventKind = "meta"
	KindSessionSwitch EventKind = "session_switch"
	KindResponse      EventKind = "response" // slash command response
)

// FormattedEvent is the result of parsing a raw JSON event line.
type FormattedEvent struct {
	Kind   EventKind
	Role   string // role prefix: "assistant", "thinking", "tool", "ai" for system messages
	Text   string // human-readable line (already formatted)
	Raw    string // original raw delta text (for stream.log append)
	Tool   string // tool name (KindTool only)
	Detail string // tool detail (KindTool only)
}

// ParseEvent parses a single JSON event from ai --mode rpc stdout
// and returns a FormattedEvent. Returns nil for events that should
// be skipped (unknown types, empty deltas, etc.).
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
			Text: fmt.Sprintf("tool: %s error: %s", label, truncate(result, 200)),
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
	reason, _ := evt["reason"].(string)
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
		return &FormattedEvent{Kind: KindMeta, Role: "ai", Text: fmt.Sprintf("ai: recovered malformed tool call (attempt %d): %s", attempt, truncate(reason, 220))}
	}
	return &FormattedEvent{Kind: KindMeta, Role: "ai", Text: "ai: recovered malformed tool call: " + truncate(reason, 220)}
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

// truncate shortens text to maxLen with ellipsis.
func truncate(text string, maxLen int) string {
	if len(text) <= maxLen {
		return text
	}
	return text[:maxLen] + "..."
}

// parseResponseEvent handles RPC response events from slash commands.
// It renders command output for the watch TUI
// so that `ai watch` displays the same human-readable output.
func parseResponseEvent(evt map[string]any) *FormattedEvent {
	success, _ := evt["success"].(bool)

	if !success {
		errMsg, _ := evt["error"].(string)
		if errMsg == "" {
			errMsg = "command failed"
		}
		return &FormattedEvent{Kind: KindResponse, Role: "ai", Text: "ai: " + errMsg}
	}

	dataRaw, _ := evt["data"].(map[string]any)
	if dataRaw == nil {
		return nil
	}

	// Re-serialize to use typed deserialization.
	dataJSON, err := json.Marshal(dataRaw)
	if err != nil {
		return &FormattedEvent{Kind: KindMeta, Text: fmt.Sprintf("%v", dataRaw)}
	}

		// Detect response type and render accordingly.
	// Order matters: most specific detections first.

	// /show settings → {type: "settings", data: {...}}
	if typ, _ := dataRaw["type"].(string); typ == "settings" {
		return renderSettings(dataJSON)
	}

	// /skills, /get_commands → {commands: [{name, source, description, ...}]}
	if _, hasCommands := dataRaw["commands"]; hasCommands {
		return renderSkills(dataJSON)
	}

	// /context → {state: SessionState, stats: SessionStats, models: ...}
	if _, hasState := dataRaw["state"]; hasState {
		return renderContext(dataJSON)
	}

	// /session → SessionState (has sessionId + model + thinkingLevel)
	if _, hasSessionID := dataRaw["sessionId"]; hasSessionID {
		return renderSessionState(dataJSON)
	}

	// /messages → {messages: [...]}
	if _, hasMessages := dataRaw["messages"]; hasMessages {
		return renderMessages(dataJSON)
	}

		// /sessions → {sessions: [...]}
	if _, hasSessions := dataRaw["sessions"]; hasSessions {
		return renderSessions(dataJSON)
	}

	// /model (list), /get_available_models → {models: [...]}
	if _, hasModels := dataRaw["models"]; hasModels {
		return renderModelList(dataJSON)
	}

	// /model, /cycle_model → {model: {...}, thinkingLevel: ...}
	if _, hasModel := dataRaw["model"]; hasModel {
		return renderModel(dataJSON)
	}

	// /session stats → {sessionId, totalMessages, tokens, ...} (but not SessionState)
	if _, hasTotalMsg := dataRaw["totalMessages"]; hasTotalMsg {
		return renderSessionStats(dataJSON)
	}

	// /thinking → {level: "..."}
	if level, ok := dataRaw["level"].(string); ok {
		return &FormattedEvent{Kind: KindMeta, Text: fmt.Sprintf("Thinking level: %s", level)}
	}

	// /compact → {message: "..."} or CompactResult
	if msg, ok := dataRaw["message"].(string); ok {
		return &FormattedEvent{Kind: KindMeta, Text: msg}
	}

	// /trace-events → {events: [...]}
	if _, hasEvents := dataRaw["events"]; hasEvents {
		return renderTraceEvents(dataJSON)
	}

	// /tree → {entries: [...]} or {root: ...}
	if _, hasEntries := dataRaw["entries"]; hasEntries {
		return renderTree(dataJSON)
	}

		// Fallback: pretty-print JSON.
	pretty, _ := json.MarshalIndent(dataRaw, "", "  ")
	text := string(pretty)
	if len(text) > 500 {
		text = text[:500] + "..."
	}
	return &FormattedEvent{Kind: KindMeta, Text: text}
}

// FormatResponseData formats a slash command response's data field into
// a human-readable string. It reuses the same rendering logic as the
// interactive TUI. Used by external clients (e.g. claw) that receive
// response data via RPC and need to display it to users.
func FormatResponseData(data any) string {
	if data == nil {
		return ""
	}
	// Construct a fake response event to reuse parseResponseEvent.
	fakeEvent := map[string]any{
		"type":    "response",
		"success": true,
		"data":    data,
	}
	result := parseResponseEvent(fakeEvent)
	if result == nil {
		return ""
	}
	return result.Text
}

// renderSessions renders /sessions output.
func renderSessions(dataJSON []byte) *FormattedEvent {
	var payload struct {
		Sessions []struct {
			ID           string `json:"id"`
			Name         string `json:"name"`
			Title        string `json:"title"`
			UpdatedAt    string `json:"updatedAt"`
			MessageCount int    `json:"messageCount"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(dataJSON, &payload); err != nil {
		return fallbackJSON(dataJSON)
	}

	if len(payload.Sessions) == 0 {
		return &FormattedEvent{Kind: KindMeta, Text: "No sessions found"}
	}

	var b strings.Builder
	b.WriteString("─────────────────────\n")
	b.WriteString("Available Sessions\n")
	b.WriteString("─────────────────────\n\n")

	// Data source (ListSessions) sorts by UpdatedAt ascending (oldest first),
	// so oldest appears at top (index 0), newest at bottom — display matches /resume index.

	for i, sess := range payload.Sessions {
		name := sess.Name
		if name == "" {
			name = sess.ID
		}
		b.WriteString(fmt.Sprintf("%d: %s (id: %s)\n", i, name, sess.ID))
		b.WriteString(fmt.Sprintf("    updated: %s  messages: %d\n", truncate(sess.UpdatedAt, 16), sess.MessageCount))
	}

	b.WriteString("\n─────────────────────\n")
	b.WriteString("Usage:\n  - /resume <index|id|path>\n")

	return &FormattedEvent{Kind: KindResponse, Text: b.String()}
}

// renderSkills renders /skills output.
func renderSkills(dataJSON []byte) *FormattedEvent {
	var payload struct {
		Commands []rpc.SlashCommand `json:"commands"`
	}
	if err := json.Unmarshal(dataJSON, &payload); err != nil {
		return fallbackJSON(dataJSON)
	}

	commands := payload.Commands
	if len(commands) == 0 {
		return &FormattedEvent{Kind: KindMeta, Text: "no commands available"}
	}

	sort.Slice(commands, func(i, j int) bool {
		if commands[i].Source == commands[j].Source {
			return commands[i].Name < commands[j].Name
		}
		return commands[i].Source < commands[j].Source
	})

		var b strings.Builder
	b.WriteString("Commands:\n")
	for _, cmd := range commands {
		desc := strings.TrimSpace(cmd.Description)
		source := cmd.Source
		if source == "" {
			source = "slash"
		}
		if desc != "" {
			b.WriteString(fmt.Sprintf("  [%s] %s - %s\n", source, cmd.Name, desc))
		} else {
			b.WriteString(fmt.Sprintf("  [%s] %s\n", source, cmd.Name))
		}
	}
	return &FormattedEvent{Kind: KindMeta, Text: strings.TrimRight(b.String(), "\n")}
}

// renderContext renders /context output.
func renderContext(dataJSON []byte) *FormattedEvent {
	var payload struct {
		State  *rpc.SessionState `json:"state"`
		Stats  *rpc.SessionStats `json:"stats"`
		Models struct {
			Models []rpc.ModelInfo `json:"models"`
		} `json:"models"`
	}
	if err := json.Unmarshal(dataJSON, &payload); err != nil {
		return fallbackJSON(dataJSON)
	}

	state := payload.State
	stats := payload.Stats
	if state == nil || stats == nil {
		return fallbackJSON(dataJSON)
	}

	modelName := "unknown"
	modelContextWindow := 0
	if state.Model != nil {
		modelName = fmt.Sprintf("%s/%s", state.Model.Provider, state.Model.ID)
		modelContextWindow = state.Model.ContextWindow
	}

	tokensMax := modelContextWindow
	if tokensMax == 0 && state.Compaction != nil {
		tokensMax = state.Compaction.ContextWindow
	}
	if tokensMax == 0 {
		tokensMax = 200000
	}

	tokensUsed := stats.Tokens.ActiveWindowTokens
	tokensPercent := float64(tokensUsed) / float64(tokensMax) * 100
	freeTokens := tokensMax - tokensUsed

	systemPromptTokens := stats.Tokens.SystemPromptTokens
	systemToolsTokens := stats.Tokens.SystemToolsTokens
	messagesTokens := tokensUsed - systemPromptTokens - systemToolsTokens
	if messagesTokens < 0 {
		messagesTokens = 0
	}

	totalBars := 30
	usedBars := int(float64(totalBars) * float64(tokensUsed) / float64(tokensMax))
	if usedBars > totalBars {
		usedBars = totalBars
	}
	freeBars := totalBars - usedBars

	var bar strings.Builder
	for i := 0; i < usedBars; i++ {
		bar.WriteString("⛁")
	}
	for i := 0; i < freeBars; i++ {
		bar.WriteString("⛶")
	}

	var b strings.Builder
	b.WriteString("  Context Usage\n")
	b.WriteString(fmt.Sprintf("%s  %s - %dk/%dk tokens (%.0f%%)\n",
		bar.String(), modelName, tokensUsed/1024, tokensMax/1024, tokensPercent))
	b.WriteString(fmt.Sprintf("     System prompt: ~%dk tokens (%.1f%%)\n",
		systemPromptTokens/1024, float64(systemPromptTokens)/float64(tokensMax)*100))
	b.WriteString(fmt.Sprintf("     System tools: ~%dk tokens (%.1f%%)\n",
		systemToolsTokens/1024, float64(systemToolsTokens)/float64(tokensMax)*100))
	b.WriteString(fmt.Sprintf("     Messages: ~%dk tokens (%.1f%%)\n",
		messagesTokens/1024, float64(messagesTokens)/float64(tokensMax)*100))
	b.WriteString(fmt.Sprintf("     Free space: %dk (%.1f%%)\n",
		freeTokens/1024, float64(freeTokens)/float64(tokensMax)*100))
	b.WriteString("     (Breakdowns are estimates based on string length)\n")
	b.WriteString("\n")
	b.WriteString(" Session Stats\n")
	b.WriteString(fmt.Sprintf(" Messages: %d total (user %d, assistant %d)\n",
		stats.TotalMessages, stats.UserMessages, stats.AssistantMessages))
	b.WriteString(fmt.Sprintf(" Tools: %d calls, %d results\n",
		stats.ToolCalls, stats.ToolResults))
	b.WriteString(fmt.Sprintf(" Compactions: %d\n", stats.CompactionCount))
	b.WriteString(fmt.Sprintf(" Cost: $%.4f\n", stats.Cost))
	b.WriteString(fmt.Sprintf(" Auto-compaction: %s\n", onOff(state.AutoCompactionEnabled)))
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf(" Model: %s\n", modelName))
	b.WriteString(fmt.Sprintf(" Context window: %dk tokens\n", tokensMax/1024))
	b.WriteString(fmt.Sprintf(" Session total: %dk tokens (all turns)\n", stats.Tokens.Total/1024))
	b.WriteString(fmt.Sprintf(" Streaming: %s", onOff(state.IsStreaming)))

	return &FormattedEvent{Kind: KindMeta, Text: b.String()}
}

// renderSessionState renders /session output.
func renderSessionState(dataJSON []byte) *FormattedEvent {
	var state rpc.SessionState
	if err := json.Unmarshal(dataJSON, &state); err != nil {
		return fallbackJSON(dataJSON)
	}

	model := "unknown"
	if state.Model != nil {
		model = state.Model.ID
		if state.Model.Provider != "" {
			model = fmt.Sprintf("%s/%s", state.Model.Provider, state.Model.ID)
		}
	}

	compactionContext := orUnknown("")
	compactionLimit := orUnknown("")
	compactionReserve := orUnknown("")
	compactionKeepRecent := orUnknown("")
	compactionKeepRecentTokens := orUnknown("")
	if state.Compaction != nil {
		compactionContext = formatIntOrUnknown(state.Compaction.ContextWindow)
		compactionLimit = formatTokenLimit(state.Compaction)
		compactionReserve = formatIntOrUnknown(state.Compaction.ReserveTokens)
		compactionKeepRecent = formatIntOrUnknown(state.Compaction.KeepRecent)
		compactionKeepRecentTokens = formatIntOrUnknown(state.Compaction.KeepRecentTokens)
	}

	aiPID := "unknown"
	if state.AIPid > 0 {
		aiPID = fmt.Sprintf("%d", state.AIPid)
	}
	aiLogPath := state.AILogPath
	if aiLogPath == "" {
		aiLogPath = "unknown"
	}
	aiWorkingDir := state.AIWorkingDir
	if aiWorkingDir == "" {
		aiWorkingDir = "unknown"
	}

	text := fmt.Sprintf(`Session:
  id: %s
  name: %s
  file: %s
  ai-pid: %s
  ai-log: %s
  ai-cwd: %s
  model: %s
  context-window: %s
  compaction-limit: %s
  compaction-reserve: %s
  compaction-keep-recent: %s
  compaction-keep-recent-tokens: %s
  thinking-level: %s
  auto-compaction: %s
  messages: %d
  pending: %d
  streaming: %s
  compacting: %s`,
		orUnknown(state.SessionID),
		orUnknown(state.SessionName),
		orUnknown(state.SessionFile),
		aiPID,
		aiLogPath,
		aiWorkingDir,
		model,
		compactionContext,
		compactionLimit,
		compactionReserve,
		compactionKeepRecent,
		compactionKeepRecentTokens,
		orUnknown(state.ThinkingLevel),
		onOff(state.AutoCompactionEnabled),
		state.MessageCount,
		state.PendingMessageCount,
		onOff(state.IsStreaming),
		onOff(state.IsCompacting),
	)

	return &FormattedEvent{Kind: KindMeta, Text: text}
}

// renderMessages renders /messages output.
func renderMessages(dataJSON []byte) *FormattedEvent {
	var payload struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(dataJSON, &payload); err != nil {
		// Try raw array
		var messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}
		if err2 := json.Unmarshal(dataJSON, &messages); err2 != nil {
			return fallbackJSON(dataJSON)
		}
		payload.Messages = messages
	}

	if len(payload.Messages) == 0 {
		return &FormattedEvent{Kind: KindMeta, Text: "no messages"}
	}

	const maxMessages = 10
	total := len(payload.Messages)
	display := payload.Messages
	baseIndex := 0
	if total > maxMessages {
		baseIndex = total - maxMessages
		display = payload.Messages[baseIndex:]
	}

	var b strings.Builder
	if total > maxMessages {
		b.WriteString(fmt.Sprintf("Messages (last %d of %d):\n", maxMessages, total))
	} else {
		b.WriteString("Messages:\n")
	}

	for i, msg := range display {
		text := strings.TrimSpace(msg.Content)
		if text == "" {
			text = "(no text)"
		}
		if len(text) > 120 {
			text = text[:120] + "..."
		}
		b.WriteString(fmt.Sprintf("  [%d] %s: %s\n", baseIndex+i, msg.Role, text))
	}

	return &FormattedEvent{Kind: KindMeta, Text: strings.TrimRight(b.String(), "\n")}
}

// renderModel renders /model output.
func renderModel(dataJSON []byte) *FormattedEvent {
	var result rpc.CycleModelResult
	if err := json.Unmarshal(dataJSON, &result); err != nil {
		// Fallback: try just {model: {id, name}}
		var payload struct {
			Model *rpc.ModelInfo `json:"model"`
		}
		if err2 := json.Unmarshal(dataJSON, &payload); err2 != nil || payload.Model == nil {
			return fallbackJSON(dataJSON)
		}
		return &FormattedEvent{Kind: KindMeta, Text: fmt.Sprintf("Model: %s/%s (%s)",
			payload.Model.Provider, payload.Model.Name, payload.Model.ID)}
	}

	name := result.Model.Name
	provider := result.Model.Provider
	id := result.Model.ID
	return &FormattedEvent{Kind: KindMeta, Text: fmt.Sprintf("Model: %s/%s (%s)", provider, name, id)}
}

// renderModelList renders /model (no args) and /get_available_models output.
func renderModelList(dataJSON []byte) *FormattedEvent {
	var payload struct {
		Models       []rpc.ModelInfo `json:"models"`
		CurrentIndex *int            `json:"currentIndex,omitempty"`
		Current      *struct {
			Provider string `json:"provider"`
			ID       string `json:"id"`
		} `json:"current,omitempty"`
	}
	if err := json.Unmarshal(dataJSON, &payload); err != nil {
		return fallbackJSON(dataJSON)
	}
	if len(payload.Models) == 0 {
		return &FormattedEvent{Kind: KindMeta, Text: "no models available"}
	}

	currentIndex := -1
	if payload.CurrentIndex != nil {
		currentIndex = *payload.CurrentIndex
	} else if payload.Current != nil {
		for i, m := range payload.Models {
			if m.Provider == payload.Current.Provider && m.ID == payload.Current.ID {
				currentIndex = i
				break
			}
		}
	}

	maxID := 0
	for _, m := range payload.Models {
		id := fmt.Sprintf("%s/%s", m.Provider, m.ID)
		if len(id) > maxID {
			maxID = len(id)
		}
	}

	var b strings.Builder
	b.WriteString("Available Models\n")
	for i, m := range payload.Models {
		ref := fmt.Sprintf("%s/%s", m.Provider, m.ID)
		name := m.Name
		if name == "" {
			name = m.ID
		}
		current := ""
		if i == currentIndex {
			current = " [current]"
		}
		b.WriteString(fmt.Sprintf("%d: %-*s - %s%s\n", i, maxID, ref, name, current))
	}
	b.WriteString("Usage: /model <index>\n")

	return &FormattedEvent{Kind: KindMeta, Text: strings.TrimRight(b.String(), "\n")}
}

// renderSettings renders /show settings output.
func renderSettings(dataJSON []byte) *FormattedEvent {
	var payload struct {
		Type string         `json:"type"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(dataJSON, &payload); err != nil || payload.Type != "settings" {
		return fallbackJSON(dataJSON)
	}

	var sb strings.Builder
	sb.WriteString("Display Settings:\n")
	keys := []string{
		"model",
		"show-thinking",
		"tools",
		"prefix",
		"thinking-level",
		"busy-mode",
		"auto-compaction",
		"compaction-context-window",
		"compaction-reserve-tokens",
		"compaction-token-limit",
		"compaction-max-messages",
		"compaction-max-tokens",
		"compaction-keep-recent",
		"compaction-keep-recent-tokens",
	}
	for _, k := range keys {
		v, ok := payload.Data[k]
		if !ok {
			v = "unknown"
		}
		sb.WriteString(fmt.Sprintf("  %s: %v\n", k, v))
	}

	return &FormattedEvent{Kind: KindMeta, Text: strings.TrimRight(sb.String(), "\n")}
}

// renderSessionStats renders /session stats output.
func renderSessionStats(dataJSON []byte) *FormattedEvent {
	var stats rpc.SessionStats
	if err := json.Unmarshal(dataJSON, &stats); err != nil {
		return fallbackJSON(dataJSON)
	}

	rateLine := "  token-rate: unavailable"
	recentLine := "  token-rate-recent: unavailable"
	lastLine := "  token-rate-last: unavailable"
	if stats.TokenRate != nil {
		rateLine = fmt.Sprintf("  token-rate: active in %.1f/s, out %.1f/s, total %.1f/s | wall total %.1f/s",
			stats.TokenRate.ActiveInputPerSec,
			stats.TokenRate.ActiveOutputPerSec,
			stats.TokenRate.ActiveTotalPerSec,
			stats.TokenRate.WallTotalPerSec,
		)
		recentLine = fmt.Sprintf("  token-rate-recent(%ds): in %.1f/s, out %.1f/s, total %.1f/s",
			stats.TokenRate.RecentWindowSeconds,
			stats.TokenRate.RecentInputPerSec,
			stats.TokenRate.RecentOutputPerSec,
			stats.TokenRate.RecentTotalPerSec,
		)
		lastLine = fmt.Sprintf("  token-rate-last: in %.1f/s, out %.1f/s, total %.1f/s",
			stats.TokenRate.LastInputPerSec,
			stats.TokenRate.LastOutputPerSec,
			stats.TokenRate.LastTotalPerSec,
		)
	}

	text := fmt.Sprintf(`Usage:
  session: %s
  messages: %d (user %d, assistant %d)
  tools: %d calls, %d results
  compactions: %d
  tokens: in %d, out %d, cache read %d, cache write %d, total %d
%s
%s
%s
  cost: %.4f`,
		orUnknown(stats.SessionID),
		stats.TotalMessages,
		stats.UserMessages,
		stats.AssistantMessages,
		stats.ToolCalls,
		stats.ToolResults,
		stats.CompactionCount,
		stats.Tokens.Input,
		stats.Tokens.Output,
		stats.Tokens.CacheRead,
		stats.Tokens.CacheWrite,
		stats.Tokens.Total,
		rateLine,
		recentLine,
		lastLine,
		stats.Cost,
	)

	return &FormattedEvent{Kind: KindMeta, Text: text}
}

// renderTraceEvents renders /trace-events output.
func renderTraceEvents(dataJSON []byte) *FormattedEvent {
	var payload struct {
		Events []string `json:"events"`
	}
	if err := json.Unmarshal(dataJSON, &payload); err != nil {
		return fallbackJSON(dataJSON)
	}

	if len(payload.Events) == 0 {
		return &FormattedEvent{Kind: KindMeta, Text: "trace events set to: <none>"}
	}

	return &FormattedEvent{Kind: KindMeta, Text: fmt.Sprintf("trace events set to: %s",
		strings.Join(payload.Events, ", "))}
}

// renderTree renders /tree output.
func renderTree(dataJSON []byte) *FormattedEvent {
	var payload struct {
		Entries []rpc.TreeEntry `json:"entries"`
	}
	if err := json.Unmarshal(dataJSON, &payload); err != nil {
		return fallbackJSON(dataJSON)
	}

	if len(payload.Entries) == 0 {
		return &FormattedEvent{Kind: KindMeta, Text: "no entries"}
	}

	var b strings.Builder
	for _, e := range payload.Entries {
		text := strings.TrimSpace(e.Text)
		if len(text) > 80 {
			text = text[:80] + "..."
		}
		b.WriteString(fmt.Sprintf("  %s [%s] %s\n",
			strings.Repeat("  ", e.Depth), e.EntryID, text))
	}

	return &FormattedEvent{Kind: KindMeta, Text: strings.TrimRight(b.String(), "\n")}
}

// --- helper functions ---

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return "unknown"
	}
	return s
}

func formatIntOrUnknown(n int) string {
	if n == 0 {
		return "unknown"
	}
	return fmt.Sprintf("%d", n)
}

func formatTokenLimit(cs *rpc.CompactionState) string {
	if cs == nil {
		return "unknown"
	}
	source := cs.TokenLimitSource
	if source == "" {
		source = "context-window"
	}
	return fmt.Sprintf("%d (%s)", cs.TokenLimit, source)
}

func fallbackJSON(dataJSON []byte) *FormattedEvent {
	var m map[string]any
	if json.Unmarshal(dataJSON, &m) != nil {
		return &FormattedEvent{Kind: KindMeta, Text: string(dataJSON)}
	}
	pretty, _ := json.MarshalIndent(m, "", "  ")
	text := string(pretty)
	if len(text) > 500 {
		text = text[:500] + "..."
	}
	return &FormattedEvent{Kind: KindMeta, Text: text}
}
