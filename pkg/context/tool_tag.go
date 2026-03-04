package context

import (
	"strconv"
	"strings"
)

// HasAgentToolMetadataTag checks if text contains agent:tool metadata tag.
func HasAgentToolMetadataTag(text string) bool {
	return strings.Contains(text, "<agent:tool")
}

// IsTruncatedAgentToolTag checks if text contains a truncated agent:tool tag.
func IsTruncatedAgentToolTag(text string) bool {
	return HasAgentToolMetadataTag(text) && strings.Contains(text, `truncated="true"`)
}

// ParseCharsFromAgentToolTag extracts the chars value from an agent:tool tag.
func ParseCharsFromAgentToolTag(text string) (int, bool) {
	const prefix = `chars="`
	start := strings.Index(text, prefix)
	if start < 0 {
		return 0, false
	}
	start += len(prefix)
	end := strings.Index(text[start:], `"`)
	if end <= 0 {
		return 0, false
	}
	value := text[start : start+end]
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}