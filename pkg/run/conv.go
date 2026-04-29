package run

import (
	"encoding/json"
	"fmt"
	"strings"
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
func parseResponseEvent(evt map[string]any) *FormattedEvent {
	success, _ := evt["success"].(bool)
	cmdName, _ := evt["command"].(string)

	if !success {
		errMsg, _ := evt["error"].(string)
		if errMsg == "" {
			errMsg = "command failed"
		}
		return &FormattedEvent{Kind: KindMeta, Text: fmt.Sprintf("❌ /%s: %s", cmdName, errMsg)}
	}

	// Try to format the response data for common commands.
	data, _ := evt["data"].(map[string]any)
	if data == nil {
		// Simple success with no data.
		return nil
	}

	// /get_commands → data.commands
	if commands, ok := data["commands"].([]any); ok {
		var lines []string
		lines = append(lines, "Available commands:")
		for _, c := range commands {
			if cm, ok := c.(map[string]any); ok {
				name, _ := cm["name"].(string)
				desc, _ := cm["description"].(string)
				if desc != "" {
					lines = append(lines, fmt.Sprintf("  /%-20s %s", name, desc))
				} else {
					lines = append(lines, fmt.Sprintf("  /%s", name))
				}
			}
		}
		return &FormattedEvent{Kind: KindMeta, Text: strings.Join(lines, "\n")}
	}

	// /get_state → data fields
	if _, hasState := data["status"]; hasState {
		jsonBytes, _ := json.MarshalIndent(data, "", "  ")
		return &FormattedEvent{Kind: KindMeta, Text: string(jsonBytes)}
	}

	// Generic: pretty-print data if it has interesting content.
	if len(data) > 0 {
		jsonBytes, _ := json.MarshalIndent(data, "", "  ")
		text := string(jsonBytes)
		if len(text) > 500 {
			text = text[:500] + "..."
		}
		return &FormattedEvent{Kind: KindMeta, Text: text}
	}

	return nil
}