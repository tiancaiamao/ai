package app

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/tiancaiamao/ai/pkg/config"
	truncpkg "github.com/tiancaiamao/ai/pkg/truncate"
)

// This file hosts the shared slash-command result renderers. Every UI
// frontend renders through them so all clients show identical
// human-readable output:
//
//   - RPC TUI event stream (`ai run` / `ai watch`, via subcommand/run/tui)
//   - ACP hosts (AionUi, Zed, agent-shell — via formatACPCommandResult)
//   - external RPC clients (via the tui.FormatResponseData shim)
//
// Renderers are pure formatters: no app/server access, no business logic.
// They return "" on unexpected shapes so callers can fall back to JSON.

// FormatCommandResult renders a slash-command result as human-readable text.
// command is the registered slash-command name when the caller knows it; it
// resolves shapes that detection alone cannot distinguish (e.g. a /resume
// switch confirmation vs. session state). Pass "" to rely purely on shape
// detection. Returns "" when nothing matches; the caller falls back to JSON.
func FormatCommandResult(command string, data any) string {
	if data == nil {
		return ""
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil || m == nil {
		return ""
	}
	command = strings.TrimPrefix(command, "/")
	if s := renderNamedCommand(command, raw); s != "" {
		return s
	}
	return renderResponseByShape(m, raw)
}

// renderNamedCommand formats results for known commands. Named dispatch wins
// over shape sniffing because some result shapes are ambiguous without the
// command context (e.g. {sessionId: ...} means different things for /resume,
// /session and /new).
func renderNamedCommand(command string, raw []byte) string {
	switch command {
	case "context":
		return renderContextCompact(raw)
	case "help", "skills", "get_commands":
		return renderSkillsTable(raw)
	case "messages":
		return renderMessagesText(raw)
	case "model", "get_available_models", "cycle_model", "set_model":
		return renderModelResult(raw)
	case "resume":
		return renderResumeText(raw)
	case "session":
		return renderSessionStateEnriched(raw)
	case "sessions":
		return renderSessionsTable(raw)
	case "set":
		if s := renderSetUsage(raw); s != "" {
			return s
		}
		return renderSetResult(raw)
	case "show":
		return renderSettingsSorted(raw)
	case "toggle":
		return renderSetResult(raw)
	case "trace-events":
		return renderTraceEventsText(raw)
	case "tree":
		return renderTreeText(raw)
	}
	return ""
}

// renderResponseByShape detects the payload shape of an unrecognized or
// unnamed response. Order matters: most specific keys first. A branch whose
// key matches but whose renderer fails yields "" (JSON fallback) rather than
// falling through to a wrong interpretation.
func renderResponseByShape(m map[string]any, raw []byte) string {
	if typ, _ := m["type"].(string); typ == "settings" {
		return renderSettingsSorted(raw)
	}
	if _, ok := m["commands"]; ok {
		return renderSkillsTable(raw)
	}
	if _, hasState := m["state"]; hasState {
		if _, hasStats := m["stats"]; hasStats {
			return renderContextCompact(raw)
		}
	}
	if _, ok := m["messages"]; ok {
		return renderMessagesText(raw)
	}
	if _, ok := m["sessions"]; ok {
		return renderSessionsTable(raw)
	}
	// /new → {sessionId, cancelled}: not a displayable state dump. Check both
	// fields so /fork ({cancelled} but no {sessionId}) still falls through.
	if _, hasCancelled := m["cancelled"]; hasCancelled {
		if _, hasSessionID := m["sessionId"]; hasSessionID {
			return ""
		}
	}
	// Session stats before plain sessionId: SessionStats also carries one,
	// but no messageCount (which distinguishes a full SessionState).
	if _, ok := m["totalMessages"]; ok {
		return renderSessionStatsText(raw)
	}
	// Session state before the /model switch: SessionState carries both a
	// sessionId and a model, so the sessionId must win here.
	if id, ok := m["sessionId"].(string); ok && id != "" {
		if _, hasModel := m["model"]; hasModel {
			return renderSessionStateEnriched(raw)
		}
		if _, hasName := m["sessionName"]; hasName {
			return renderResumeSwitchConfirm(raw)
		}
		return renderSessionStateEnriched(raw)
	}
	if _, ok := m["models"]; ok {
		return renderModelList(raw)
	}
	if _, ok := m["model"]; ok {
		return renderModelSwitched(raw)
	}
	if level, ok := m["level"].(string); ok {
		return fmt.Sprintf("Thinking level: %s", level)
	}
	if msg, ok := m["message"].(string); ok {
		return msg
	}
	// /set, /toggle → {setting, value}. Checked late: "setting" never
	// appears in other payloads, but keep generic shapes first.
	if _, ok := m["setting"]; ok {
		return renderSetResult(raw)
	}
	// /set help → {usage, settings}
	if _, ok := m["usage"]; ok {
		return renderSetUsage(raw)
	}
	if _, ok := m["events"]; ok {
		return renderTraceEventsText(raw)
	}
	if _, ok := m["entries"]; ok {
		return renderTreeText(raw)
	}
	return ""
}

// --- per-command renderers ---

// renderSessionStateEnriched renders /session with all 17 fields of session
// information, matching the original TUI output format.
func renderSessionStateEnriched(raw []byte) string {
	var state SessionState
	if err := json.Unmarshal(raw, &state); err != nil || state.SessionID == "" {
		return ""
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

	return text
}

// onOff converts a boolean to "on" or "off".
func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// renderContextCompact renders /context with detailed context usage bar and
// session stats, matching the original TUI output format.
func renderContextCompact(raw []byte) string {
	var payload struct {
		State *SessionState `json:"state"`
		Stats *SessionStats `json:"stats"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	state, stats := payload.State, payload.Stats
	if state == nil || stats == nil || state.SessionID == "" {
		return ""
	}

	// Get model info for context window
	modelName := "unknown"
	modelContextWindow := 0
	if state.Model != nil {
		modelName = fmt.Sprintf("%s/%s", state.Model.Provider, state.Model.ID)
		modelContextWindow = state.Model.ContextWindow
	}

	// Determine context window
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

	// Break down token usage
	systemPromptTokens := stats.Tokens.SystemPromptTokens
	systemToolsTokens := stats.Tokens.SystemToolsTokens
	messagesTokens := tokensUsed - systemPromptTokens - systemToolsTokens
	if messagesTokens < 0 {
		messagesTokens = 0
	}

	// Draw Unicode bar (30 characters total)
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

	// Build detailed output
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

	return b.String()
}

// renderTokenUsageLine renders one line of token usage from session stats,
// appending the share of the context window when both numbers are known.
func renderTokenUsageLine(state *SessionState, stats *SessionStats) string {
	t := stats.Tokens
	line := fmt.Sprintf("Tokens: in %d · out %d · cache %d/%d · total %d",
		t.Input, t.Output, t.CacheRead, t.CacheWrite, t.Total)
	if t.ActiveWindowTokens > 0 && state.Compaction != nil && state.Compaction.ContextWindow > 0 {
		line += fmt.Sprintf(" (%d%% of window)",
			100*t.ActiveWindowTokens/state.Compaction.ContextWindow)
	}
	return line
}

// sessionStatusLine is the shared one-line summary:
// "Model: <model> · Session: <id[:8]> · Streaming: <status>".
func sessionStatusLine(state *SessionState) string {
	return fmt.Sprintf("Model: %s · Session: %s · Streaming: %s",
		modelDisplayName(state.Model), shortID(state.SessionID), streamingStatus(state))
}

// streamingStatus describes the streaming state in one word.
func streamingStatus(state *SessionState) string {
	switch {
	case state.IsStreaming:
		return "active"
	case state.IsCompacting:
		return "compacting"
	default:
		return "idle"
	}
}

// renderResumeText renders both /resume shapes: a session switch confirmation
// or the no-arg session list table.
func renderResumeText(raw []byte) string {
	var sw struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(raw, &sw); err == nil && sw.SessionID != "" {
		return renderResumeSwitchConfirm(raw)
	}
	return renderSessionsTable(raw)
}

// renderResumeSwitchConfirm renders a successful /resume switch.
func renderResumeSwitchConfirm(raw []byte) string {
	var sw struct {
		SessionID   string `json:"sessionId"`
		SessionName string `json:"sessionName"`
	}
	if err := json.Unmarshal(raw, &sw); err != nil || sw.SessionID == "" {
		return ""
	}
	if sw.SessionName != "" {
		return fmt.Sprintf("Switched to session %s (%s)", sw.SessionName, shortID(sw.SessionID))
	}
	return fmt.Sprintf("Switched to session %s", shortID(sw.SessionID))
}

// renderSessionsTable renders the session list with its index doubling as
// the /resume <index> argument. Used by /sessions and /resume (no args).
func renderSessionsTable(raw []byte) string {
	var payload struct {
		Sessions []struct {
			ID           string `json:"id"`
			Name         string `json:"name"`
			Title        string `json:"title"`
			UpdatedAt    string `json:"updatedAt"`
			MessageCount int    `json:"messageCount"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	if len(payload.Sessions) == 0 {
		return "No sessions found"
	}

	var b strings.Builder
	b.WriteString("─────────────────────\n")
	b.WriteString("Available Sessions\n")
	b.WriteString("─────────────────────\n\n")

	// Data source (ListSessions) sorts by UpdatedAt ascending (oldest first),
	// so oldest appears at top (index 0), newest at bottom — display matches
	// the /resume index.
	for i, sess := range payload.Sessions {
		name := sess.Name
		if name == "" {
			name = sess.ID
		}
		b.WriteString(fmt.Sprintf("%d: %s (id: %s)\n", i, name, sess.ID))
		b.WriteString(fmt.Sprintf("    updated: %s  messages: %d\n",
			truncpkg.TruncateString(sess.UpdatedAt, 16), sess.MessageCount))
	}

	b.WriteString("\n─────────────────────\n")
	b.WriteString("Usage:\n  - /resume <index|id|path>\n")

	return b.String()
}

// renderSkillsTable renders /skills, /get_commands and /help output as
// "  [source] name - description" lines, grouped by source.
func renderSkillsTable(raw []byte) string {
	var payload struct {
		Commands []SlashCommand `json:"commands"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	commands := payload.Commands
	if len(commands) == 0 {
		return "no commands available"
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
	return strings.TrimRight(b.String(), "\n")
}

// settingsDisplayKeys fixes the display order of /show settings keys.
var settingsDisplayKeys = []string{
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

// renderSettingsSorted renders /show settings in a fixed key order; keys
// missing from the payload display as "unknown".
func renderSettingsSorted(raw []byte) string {
	var payload struct {
		Type string         `json:"type"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || payload.Type != "settings" {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("Display Settings:\n")
	for _, k := range settingsDisplayKeys {
		v, ok := payload.Data[k]
		if !ok {
			v = "unknown"
		}
		sb.WriteString(fmt.Sprintf("  %s: %v\n", k, v))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// renderSetResult renders /set and /toggle confirmations ({setting, value})
// as a single line, e.g. "thinking-level: low".
func renderSetResult(raw []byte) string {
	var payload struct {
		Setting string `json:"setting"`
		Value   any    `json:"value"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || payload.Setting == "" {
		return ""
	}
	return fmt.Sprintf("%s: %v", payload.Setting, payload.Value)
}

// renderSetUsage renders the /set help listing ({usage, settings}).
func renderSetUsage(raw []byte) string {
	var payload struct {
		Usage    string   `json:"usage"`
		Settings []string `json:"settings"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil || payload.Usage == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("usage: ")
	b.WriteString(payload.Usage)
	b.WriteString("\nsettings:\n")
	for _, s := range payload.Settings {
		b.WriteString("  ")
		b.WriteString(s)
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderModelResult dispatches the two /model result shapes: the model list
// (no args) and the switched-model confirmation (/model <index>).
func renderModelResult(raw []byte) string {
	var probe struct {
		Models []config.ModelInfo `json:"models"`
	}
	if err := json.Unmarshal(raw, &probe); err == nil && len(probe.Models) > 0 {
		return renderModelList(raw)
	}
	return renderModelSwitched(raw)
}

// renderModelList renders the model list with indices usable as the
// /model <index> argument; the current model gets a "[current]" marker.
func renderModelList(raw []byte) string {
	var payload struct {
		Models       []config.ModelInfo `json:"models"`
		CurrentIndex *int               `json:"currentIndex,omitempty"`
		Current      *struct {
			Provider string `json:"provider"`
			ID       string `json:"id"`
		} `json:"current,omitempty"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	if len(payload.Models) == 0 {
		return "no models available"
	}
	currentIndex := -1
	if payload.CurrentIndex != nil {
		currentIndex = *payload.CurrentIndex
	} else if payload.Current != nil {
		for i, mm := range payload.Models {
			if mm.Provider == payload.Current.Provider && mm.ID == payload.Current.ID {
				currentIndex = i
				break
			}
		}
	}
	maxID := 0
	for _, mm := range payload.Models {
		id := fmt.Sprintf("%s/%s", mm.Provider, mm.ID)
		if len(id) > maxID {
			maxID = len(id)
		}
	}
	var b strings.Builder
	b.WriteString("Available Models\n")
	for i, mm := range payload.Models {
		ref := fmt.Sprintf("%s/%s", mm.Provider, mm.ID)
		name := mm.Name
		if name == "" {
			name = mm.ID
		}
		current := ""
		if i == currentIndex {
			current = " [current]"
		}
		b.WriteString(fmt.Sprintf("%d: %-*s - %s%s\n", i, maxID, ref, name, current))
	}
	b.WriteString("Usage: /model <index>")
	return b.String()
}

// renderModelSwitched renders a /model <index> switch confirmation.
func renderModelSwitched(raw []byte) string {
	var result CycleModelResult
	if err := json.Unmarshal(raw, &result); err != nil || result.Model.ID == "" {
		var payload struct {
			Model *config.ModelInfo `json:"model"`
		}
		if err2 := json.Unmarshal(raw, &payload); err2 != nil || payload.Model == nil || payload.Model.ID == "" {
			return ""
		}
		result.Model = *payload.Model
	}
	return fmt.Sprintf("Model: %s/%s (%s)", result.Model.Provider, result.Model.Name, result.Model.ID)
}

// renderMessagesText renders /messages output (new MessagesResult shape,
// legacy {messages: [...]} shape, or a legacy top-level array).
func renderMessagesText(raw []byte) string {
	var result MessagesResult
	if err := json.Unmarshal(raw, &result); err == nil && result.Total > 0 {
		return renderFormattedMessages(result)
	}
	var legacy struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &legacy); err == nil && legacy.Messages != nil {
		if len(legacy.Messages) == 0 {
			return "no messages"
		}
		converted := MessagesResult{
			Total:    len(legacy.Messages),
			Showing:  len(legacy.Messages),
			Messages: make([]FormattedMessage, len(legacy.Messages)),
		}
		for i, msg := range legacy.Messages {
			converted.Messages[i] = FormattedMessage{Index: i, Role: msg.Role, Preview: msg.Content}
		}
		return renderFormattedMessages(converted)
	}
	var arr []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(raw, &arr); err != nil || len(arr) == 0 {
		return ""
	}
	converted := MessagesResult{
		Total:    len(arr),
		Showing:  len(arr),
		Messages: make([]FormattedMessage, len(arr)),
	}
	for i, msg := range arr {
		converted.Messages[i] = FormattedMessage{Index: i, Role: msg.Role, Preview: msg.Content}
	}
	return renderFormattedMessages(converted)
}

// renderFormattedMessages renders a MessagesResult into readable lines.
func renderFormattedMessages(result MessagesResult) string {
	if len(result.Messages) == 0 {
		return "no messages"
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
		if len(msg.ToolCalls) > 0 {
			line += fmt.Sprintf(" (tools: %s)", strings.Join(msg.ToolCalls, ", "))
		}
		b.WriteString(line + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderSessionStatsText renders usage statistics as labeled lines.
func renderSessionStatsText(raw []byte) string {
	var stats SessionStats
	if err := json.Unmarshal(raw, &stats); err != nil {
		return ""
	}
	return fmt.Sprintf(`Usage:
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
}

// renderTraceEventsText renders the trace-events filter list.
func renderTraceEventsText(raw []byte) string {
	var payload struct {
		Events []string `json:"events"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	if len(payload.Events) == 0 {
		return "trace events set to: <none>"
	}
	return fmt.Sprintf("trace events set to: %s", strings.Join(payload.Events, ", "))
}

// renderTreeText renders conversation-tree entries indented by depth.
func renderTreeText(raw []byte) string {
	var payload struct {
		Entries []TreeEntry `json:"entries"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	if len(payload.Entries) == 0 {
		return "no entries"
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
	return strings.TrimRight(b.String(), "\n")
}

// --- shared helpers ---

// shortID truncates a session id to its first 8 characters for summaries.
func shortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// modelDisplayName formats a model as "provider/id" (or just "id" when the
// provider is unset); unknown/nil models degrade to "unknown".
func modelDisplayName(m *config.ModelInfo) string {
	if m == nil || m.ID == "" {
		return "unknown"
	}
	if m.Provider == "" {
		return m.ID
	}
	return m.Provider + "/" + m.ID
}

// orUnknown maps empty strings to "unknown" for display.
func orUnknown(s string) string {
	if strings.TrimSpace(s) == "" {
		return "unknown"
	}
	return s
}
