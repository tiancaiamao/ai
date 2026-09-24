package app

import (
	"encoding/json"
	"strings"

	"github.com/tiancaiamao/ai/pkg/compact"
)

// SettingsSnapshot holds the mutable display settings shown by /show settings.
type SettingsSnapshot struct {
	ModelID        string
	ModelProvider  string
	ShowThinking   bool
	ShowTools      bool
	ShowPrefix     bool
	ThinkingLevel  string
	BusyMode       string
	AutoCompaction bool
	Compaction     *compact.CompactionState
}

// ToggleResult represents the outcome of parsing a toggle-style setting value.
type ToggleResult struct {
	Value   bool
	Changed bool // false means "no change requested" (invalid)
}

// BuildSettingsResponse formats a SettingsSnapshot into the /show settings response map.
func BuildSettingsResponse(s SettingsSnapshot) map[string]any {
	model := s.ModelID
	if s.ModelProvider != "" {
		model = s.ModelProvider + "/" + s.ModelID
	}

	compactionContext := "unknown"
	compactionReserve := "unknown"
	compactionLimit := "unknown"
	compactionMaxTokens := "disabled"
	compactionKeepRecentTokens := "unknown"
	if s.Compaction != nil {
		compactionContext = FormatIntOrUnknown(s.Compaction.ContextWindow)
		compactionReserve = FormatIntOrUnknown(s.Compaction.ReserveTokens)
		compactionLimit = FormatTokenLimit(s.Compaction)
		compactionMaxTokens = FormatLimit(s.Compaction.MaxTokens)
		compactionKeepRecentTokens = FormatIntOrUnknown(s.Compaction.KeepRecentTokens)
	}

	return map[string]any{
		"type": "settings",
		"data": map[string]any{
			"model":                         model,
			"show-thinking":                 boolStr(s.ShowThinking),
			"tools":                         boolStr(s.ShowTools),
			"prefix":                        boolStr(s.ShowPrefix),
			"thinking-level":                s.ThinkingLevel,
			"busy-mode":                     s.BusyMode,
			"auto-compaction":               boolStr(s.AutoCompaction),
			"compaction-context-window":     compactionContext,
			"compaction-reserve-tokens":     compactionReserve,
			"compaction-token-limit":        compactionLimit,
			"compaction-max-tokens":         compactionMaxTokens,
			"compaction-keep-recent-tokens": compactionKeepRecentTokens,
		},
	}
}

// SetUsage returns the help text for the /set command.
func SetUsage() map[string]any {
	return map[string]any{
		"usage": "/set <key> [value]",
		"settings": []string{
			"auto-retry <on|off>",
			"auto-compaction <on|off>",
			"busy-mode <steer|follow-up|reject>",
			"follow-up-mode <all|immediate|one-at-a-time>",
			"prefix-display <on|off|toggle>",
			"session-name <name>",
			"steering-mode <all|immediate|one-at-a-time>",
			"thinking-display <on|off|toggle>",
			"thinking-level <off|minimal|low|medium|high|xhigh>",
			"tool-call-cutoff <n>",
			"tool-summary-automation <off|fallback|always>",
			"tool-summary-strategy <llm|heuristic|off>",
			"tools-display <on|off|toggle>",
			"trace-events [on|off|all|enable <selectors>|disable <selectors>]",
		},
	}
}

// ParseToggleValue parses on/off/toggle for display settings.
// Returns Changed=false for invalid input.
func ParseToggleValue(value string, current bool) ToggleResult {
	switch strings.TrimSpace(value) {
	case "on":
		return ToggleResult{Value: true, Changed: true}
	case "off":
		return ToggleResult{Value: false, Changed: true}
	case "toggle", "":
		return ToggleResult{Value: !current, Changed: true}
	default:
		return ToggleResult{Changed: false}
	}
}

// ParseBoolFromInput extracts a boolean from a JSON object {"key": bool} or
// from plain text ("true"/"1"/"on" → true, anything else → false).
func ParseBoolFromInput(value string, jsonKey string) bool {
	var jsonData map[string]any
	trimmed := strings.TrimSpace(value)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		if json.Unmarshal([]byte(value), &jsonData) == nil {
			if v, ok := jsonData[jsonKey]; ok {
				if b, ok := v.(bool); ok {
					return b
				}
			}
		}
	}
	lower := strings.ToLower(strings.TrimSpace(value))
	return lower == "true" || lower == "1" || lower == "on"
}

func boolStr(b bool) string {
	if b {
		return "on"
	}
	return "off"
}
