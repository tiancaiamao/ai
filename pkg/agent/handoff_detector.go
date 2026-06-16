package agent

import (
	"strings"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// handoffCompleteMarker is the sentinel string the LLM emits to signal that
// the handoff document is complete.
const handoffCompleteMarker = "<handoff_complete>"

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
// message. The handoff doc is the text content BEFORE the
// <handoff_complete> marker.
//
// If the marker is in a text block, everything before it in that block (plus
// all prior text blocks) is the handoff doc. If the marker is not found an
// empty string is returned.
func extractHandoffDoc(msg *agentctx.AgentMessage) string {
	if msg == nil {
		return ""
	}
	var parts []string
	for _, block := range msg.Content {
		tc, ok := block.(agentctx.TextContent)
		if !ok {
			continue
		}
		if idx := strings.Index(tc.Text, handoffCompleteMarker); idx >= 0 {
			parts = append(parts, tc.Text[:idx])
			return strings.Join(parts, "\n")
		}
		parts = append(parts, tc.Text)
	}
	return strings.Join(parts, "\n")
}
