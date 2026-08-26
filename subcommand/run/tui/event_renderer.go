package tui

import (
	"encoding/json"

	"github.com/tiancaiamao/ai/pkg/rpc"
)

// parseResponseEvent renders an RPC response event for display. Formatting is
// delegated to the server-side shared renderers (rpc.FormatCommandResult) so
// `ai run` / `ai watch` display the same human-readable output as ACP hosts
// and other clients.
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

	// /new → {sessionId, cancelled} — stay silent; the session_switch event
	// already handles display.
	if _, hasCancelled := dataRaw["cancelled"]; hasCancelled {
		if _, hasSessionID := dataRaw["sessionId"]; hasSessionID {
			return nil
		}
	}

	command, _ := evt["command"].(string)
	if text := rpc.FormatCommandResult(command, dataRaw); text != "" {
		return &FormattedEvent{Kind: KindResponse, Text: text}
	}

	// Fallback: pretty-print unrecognized shapes as truncated JSON.
	pretty, _ := json.MarshalIndent(dataRaw, "", "  ")
	text := string(pretty)
	if len(text) > 500 {
		text = text[:500] + "..."
	}
	return &FormattedEvent{Kind: KindMeta, Text: text}
}

// FormatResponseData formats a slash command response's data field into a
// human-readable string. Kept for external RPC clients (e.g. claw) that
// receive response data via RPC and need to display it to users; it uses the
// same shared renderers as the interactive TUI.
func FormatResponseData(data any) string {
	return rpc.FormatCommandResult("", data)
}
