package agent

import (
	"strings"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// handoffCompleteMarker is the sentinel string the LLM emits to signal that
// the handoff document is complete.
const handoffCompleteMarker = "<handoff_complete>"

// handoffCompleteEndMarker is the optional closing tag. When present, the
// handoff document is the text enclosed between the opening and closing tags.
const handoffCompleteEndMarker = "</handoff_complete>"

// hasHandoffMarker returns true if the assistant message contains
// <handoff_complete> in any of its text content blocks.
func hasHandoffMarker(msg *agentctx.AgentMessage) bool {
	if msg == nil {
		return false
	}
	for _, block := range msg.Content {
		if tc, ok := block.(agentctx.TextContent); ok {
			if strings.Contains(tc.Text, handoffCompleteMarker) {
				return true
			}
		}
	}
	return false
}

// extractHandoffDoc extracts the handoff document text from the assistant
// message. The LLM places the handoff document INSIDE the
// <handoff_complete>...</handoff_complete> tags.
//
// Extraction rules:
//   - If <handoff_complete>...</handoff_complete> exists: extract the text
//     between the opening and closing tags.
//   - If only <handoff_complete> exists (no closing tag): extract everything
//     after the opening marker.
//   - If no marker is found: return "".
//
// The result is trimmed of leading/trailing whitespace.
func extractHandoffDoc(msg *agentctx.AgentMessage) string {
	if msg == nil {
		return ""
	}
	// Combine all text content into a single string so that tags may span
	// multiple content blocks.
	var parts []string
	for _, block := range msg.Content {
		tc, ok := block.(agentctx.TextContent)
		if !ok {
			continue
		}
		parts = append(parts, tc.Text)
	}
	combined := strings.Join(parts, "\n")

	// Find the opening marker.
	openIdx := strings.Index(combined, handoffCompleteMarker)
	if openIdx < 0 {
		return ""
	}
	contentStart := openIdx + len(handoffCompleteMarker)

	// Look for a closing tag after the opening marker.
	closeIdx := strings.Index(combined[contentStart:], handoffCompleteEndMarker)
	if closeIdx >= 0 {
		return strings.TrimSpace(combined[contentStart : contentStart+closeIdx])
	}

	// No closing tag — extract everything after the opening marker.
	return strings.TrimSpace(combined[contentStart:])
}
