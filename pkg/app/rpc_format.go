package app

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/tiancaiamao/ai/pkg/compact"
)

// TruncateText truncates text to at most limit bytes, appending "..." if truncation occurs.
// Returns "" if limit <= 0.
func TruncateText(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(text) <= limit {
		return text
	}
	if limit <= 3 {
		return text[:limit]
	}
	return text[:limit-3] + "..."
}

// FormatIntOrUnknown returns the integer as a string, or "unknown" if value <= 0.
func FormatIntOrUnknown(value int) string {
	if value <= 0 {
		return "unknown"
	}
	return strconv.Itoa(value)
}

// FormatLimit returns the integer as a string, or "disabled" if value <= 0.
func FormatLimit(value int) string {
	if value <= 0 {
		return "disabled"
	}
	return strconv.Itoa(value)
}

// FormatTokenLimit renders a compaction state's token limit for display.
func FormatTokenLimit(state *compact.CompactionState) string {
	if state == nil || state.TokenLimit <= 0 {
		return "unknown"
	}
	source := FormatTokenLimitSource(state.TokenLimitSource)
	if source == "" {
		return strconv.Itoa(state.TokenLimit)
	}
	return fmt.Sprintf("%d (%s)", state.TokenLimit, source)
}

// FormatTokenLimitSource normalizes a token-limit source string for display.
func FormatTokenLimitSource(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "context_window":
		return "context-window"
	case "max_tokens":
		return "max-tokens"
	case "none":
		return ""
	default:
		return strings.TrimSpace(value)
	}
}
