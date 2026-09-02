package tui

import (
	"encoding/json"

	"github.com/tiancaiamao/ai/pkg/app"
)

// parseResponseEvent handles RPC response events from slash commands so that
// `ai watch` displays the same human-readable output. All rendering lives in
// pkg/app/render.go (shared with the ACP side); this file only maps the

// rendered text onto a FormattedEvent and preserves TUI-specific behavior
// (suppressing /new, response styling).
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

	// /new → {sessionId, cancelled} — not displayed; the session_switch event
	// that follows handles it. /fork also carries {cancelled} without
	// {sessionId}, so check both fields.
	if _, hasCancelled := dataRaw["cancelled"]; hasCancelled {
		if _, hasSessionID := dataRaw["sessionId"]; hasSessionID {
			return nil
		}
	}

	// The response carries the originating command name, which resolves
	// shapes that detection alone cannot distinguish (/resume vs /session).
	// Callers without a command name (FormatResponseData) fall back to shape
	// detection inside the shared renderer.
	command, _ := evt["command"].(string)
	if text := app.FormatCommandResult(command, dataRaw); text != "" {
		kind := KindMeta
		if _, hasSessions := dataRaw["sessions"]; hasSessions {
			// /sessions renders as a boxed response block, not a meta line.
			kind = KindResponse
		}
		return &FormattedEvent{Kind: kind, Text: text}
	}

	// Fallback: pretty-print JSON, truncated for display.
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
