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
	KindTool          EventKind = "tool"
	KindMeta          EventKind = "meta"
	KindSessionSwitch EventKind = "session_switch"
)

// FormattedEvent is the result of parsing a raw JSON event line.
type FormattedEvent struct {
	Kind   EventKind
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
	case "message_update":
		return parseMessageUpdate(evt)
	case "tool_execution_start":
		return parseToolExecutionStart(evt)
	case "agent_start":
		return &FormattedEvent{Kind: KindMeta, Text: "--- agent started ---"}
	case "agent_end":
		return parseAgentEnd(evt)
	case "turn_start":
		return &FormattedEvent{Kind: KindMeta, Text: "--- turn ---"}
	case "turn_end":
		return nil // silent
	case "tool_execution_end":
		return nil // silent
	case "error":
		errMsg, _ := evt["error"].(string)
		if errMsg == "" {
			errMsg = "unknown error"
		}
		return &FormattedEvent{Kind: KindMeta, Text: fmt.Sprintf("❌ error: %s", errMsg)}
	case "response":
		return parseResponseEvent(evt)
	case "session_switch":
		return parseSessionSwitch(evt)
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
	delta := ExtractTextDelta(evt)
	if delta == "" {
		return nil
	}
	return &FormattedEvent{
		Kind: KindText,
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

	return &FormattedEvent{
		Kind:   KindTool,
		Text:   fmt.Sprintf("🔧 %s%s", toolName, detail),
		Raw:    "",
		Tool:   toolName,
		Detail: detail,
	}
}

func parseAgentEnd(evt map[string]any) *FormattedEvent {
	errMsg, _ := evt["error"].(string)
	if errMsg != "" {
		return &FormattedEvent{Kind: KindMeta, Text: fmt.Sprintf("--- agent failed: %s ---", errMsg)}
	}
	if success, ok := evt["success"].(bool); ok && !success {
		return &FormattedEvent{Kind: KindMeta, Text: "--- agent failed ---"}
	}
	return &FormattedEvent{Kind: KindMeta, Text: "--- agent done ---"}
}

func parseSessionSwitch(evt map[string]any) *FormattedEvent {
	sessionID, _ := evt["session"].(string)
	sessionName, _ := evt["sessionName"].(string)

	if sessionName != "" {
		return &FormattedEvent{
			Kind: KindSessionSwitch,
			Text: fmt.Sprintf("--- session: %s (%s) ---", sessionName, sessionID),
			Raw:  "",
		}
	}
	if sessionID != "" {
		return &FormattedEvent{
			Kind: KindSessionSwitch,
			Text: fmt.Sprintf("--- session: %s ---", sessionID),
			Raw:  "",
		}
	}
	return nil
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

// parseResponseEvent handles RPC response events from slash commands.
// It replicates the rendering logic from ai-win's interpreter.go
// so that `ai watch` displays the same human-readable output.
func parseResponseEvent(evt map[string]any) *FormattedEvent {
	success, _ := evt["success"].(bool)

	if !success {
		errMsg, _ := evt["error"].(string)
		if errMsg == "" {
			errMsg = "command failed"
		}
		return &FormattedEvent{Kind: KindMeta, Text: fmt.Sprintf("❌ %s", errMsg)}
	}

	dataRaw, _ := evt["data"].(map[string]any)
	if dataRaw == nil {
		return nil
	}

	// Re-serialize to use typed deserialization, matching ai-win behavior.
	dataJSON, err := json.Marshal(dataRaw)
	if err != nil {
		return &FormattedEvent{Kind: KindMeta, Text: fmt.Sprintf("%v", dataRaw)}
	}

	// Detect response type and render accordingly.
	// Order matters: most specific detections first.

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

// renderSkills renders /skills output, matching ai-win handleCommands.
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

// renderContext renders /context output, matching ai-win showContext.
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

// renderSessionState renders /session output, matching ai-win showState.
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

// renderMessages renders /messages output, matching ai-win handleMessages.
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

// --- helper functions (matching ai-win style) ---

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
