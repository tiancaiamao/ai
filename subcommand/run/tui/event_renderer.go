package tui

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/tiancaiamao/ai/pkg/rpc"
	"github.com/tiancaiamao/ai/pkg/compact"
	"github.com/tiancaiamao/ai/pkg/config"
	truncpkg "github.com/tiancaiamao/ai/pkg/truncate"
)

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

	// /new → {sessionId, cancelled} — skip; session_switch event already handles display
	// Must check both fields to avoid suppressing /fork which also has {cancelled} but no {sessionId}.
	if _, hasCancelled := dataRaw["cancelled"]; hasCancelled {
		if _, hasSessionID := dataRaw["sessionId"]; hasSessionID {
			return nil
		}
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
		b.WriteString(fmt.Sprintf("    updated: %s  messages: %d\n", truncpkg.TruncateString(sess.UpdatedAt, 16), sess.MessageCount))
	}

	b.WriteString("\n─────────────────────\n")
	b.WriteString("Usage:\n  - /resume <index|id|path>\n")

	return &FormattedEvent{Kind: KindResponse, Text: b.String()}
}

// renderSkills renders /skills output.
func renderSkills(dataJSON []byte) *FormattedEvent {
	var payload struct {
		Commands []app.SlashCommand `json:"commands"`
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
		State  *app.SessionState `json:"state"`
		Stats  *app.SessionStats `json:"stats"`
		Models struct {
			Models []config.ModelInfo `json:"models"`
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
	var state app.SessionState
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
	// Try new MessagesResult format first (has total/showing/preview fields)
	var result app.MessagesResult
	if err := json.Unmarshal(dataJSON, &result); err == nil && result.Total > 0 {
		return renderFormattedMessages(result)
	}

	// Legacy format: {messages: [{role, content}]}
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

	// Convert legacy format to MessagesResult
	legacyResult := app.MessagesResult{
		Total:    len(payload.Messages),
		Showing:  len(payload.Messages),
		Messages: make([]app.FormattedMessage, len(payload.Messages)),
	}
	for i, msg := range payload.Messages {
		legacyResult.Messages[i] = app.FormattedMessage{
			Index:   i,
			Role:    msg.Role,
			Preview: msg.Content,
		}
	}
	return renderFormattedMessages(legacyResult)
}

// renderFormattedMessages renders a MessagesResult into a human-readable display.
func renderFormattedMessages(result app.MessagesResult) *FormattedEvent {
	if len(result.Messages) == 0 {
		return &FormattedEvent{Kind: KindMeta, Text: "no messages"}
	}

	var b strings.Builder
	if result.Showing < result.Total {
		b.WriteString(fmt.Sprintf("Messages (last %d of %d):\n", result.Showing, result.Total))
	} else {
		b.WriteString(fmt.Sprintf("Messages (%d):\n", result.Total))
	}

	for _, msg := range result.Messages {
		text := strings.TrimSpace(msg.Preview)
		if text == "" {
			text = "(no text)"
		}
		if len(text) > 120 {
			text = text[:120] + "..."
		}

		role := msg.Role
		if msg.ToolName != "" {
			role = fmt.Sprintf("%s: %s", msg.Role, msg.ToolName)
		}

		line := fmt.Sprintf("  [%d] %s: %s", msg.Index, role, text)

		// Append tool call names if present
		if len(msg.ToolCalls) > 0 {
			line += fmt.Sprintf(" (tools: %s)", strings.Join(msg.ToolCalls, ", "))
		}

		b.WriteString(line + "\n")
	}

	return &FormattedEvent{Kind: KindMeta, Text: strings.TrimRight(b.String(), "\n")}
}

// renderModel renders /model output.
func renderModel(dataJSON []byte) *FormattedEvent {
	var result app.CycleModelResult
	if err := json.Unmarshal(dataJSON, &result); err != nil {
		// Fallback: try just {model: {id, name}}
		var payload struct {
			Model *config.ModelInfo `json:"model"`
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
		Models       []config.ModelInfo `json:"models"`
		CurrentIndex *int               `json:"currentIndex,omitempty"`
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
	var stats app.SessionStats
	if err := json.Unmarshal(dataJSON, &stats); err != nil {
		return fallbackJSON(dataJSON)
	}
	text := fmt.Sprintf(`Usage:
  session: %s
  messages: %d (user %d, assistant %d)
  tools: %d calls, %d results
  compactions: %d
  tokens: in %d, out %d, cache read %d, cache write %d, total %d
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
		Entries []app.TreeEntry `json:"entries"`
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

func formatTokenLimit(cs *compact.CompactionState) string {
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
