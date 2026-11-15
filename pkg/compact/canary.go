package compact

import (
	"crypto/rand"
	"encoding/hex"
	"strings"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// CanaryKind is the MessageMetadata.Kind value for canary messages.
const CanaryKind = "canary"

// generateCanaryValue generates a random hex string (12 hex chars, 48 bits)
// for use as a unique canary identifier.
func generateCanaryValue() string {
	buf := make([]byte, 6) // 48 bits
	_, err := rand.Read(buf)
	if err != nil {
		return "deadbeefcafe"
	}
	return hex.EncodeToString(buf)
}

// InsertCanary appends a canary message to RecentMessages and returns the
// canary value. Called once after each compaction (from performCompaction).
// The canary stays in RecentMessages until the next compaction cleans it.
// askLLM only reads it — never modifies RecentMessages.
func InsertCanary(agentCtx *agentctx.AgentContext) string {
	if agentCtx == nil {
		return ""
	}

	value := generateCanaryValue()
	content := `<agent:canary value="` + value + `"/>`

	msg := agentctx.NewUserMessage(content).
		WithKind(CanaryKind).
		WithVisibility(true, false)

	agentCtx.RecentMessages = append(agentCtx.RecentMessages, msg)
	return value
}

// FindCanaryValue returns the value of the most recently planted canary in
// messages, or "" when no canary message is present.
//
// The canary message is the source of truth for the retention check: it lives
// in RecentMessages, is persisted with the session and survives a restart,
// whereas any in-memory copy of the value would be lost. Keeping the value and
// the message together prevents the two from drifting apart (a stale in-memory
// value makes every ask look like a failed retention check).
func FindCanaryValue(messages []agentctx.AgentMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Metadata == nil || msg.Metadata.Kind != CanaryKind {
			continue
		}
		if value := parseCanaryValue(msg.ExtractText()); value != "" {
			return value
		}
	}
	return ""
}

// parseCanaryValue extracts the value attribute from a
// `<agent:canary value="..."/>` message body.
func parseCanaryValue(content string) string {
	const marker = `value="`
	idx := strings.Index(content, marker)
	if idx < 0 {
		return ""
	}
	rest := content[idx+len(marker):]
	end := strings.IndexByte(rest, '"')
	if end < 0 {
		return ""
	}
	return rest[:end]
}

// RemoveAllCanaries returns a new message slice with all canary messages
// removed, preserving the order of remaining messages.
func RemoveAllCanaries(messages []agentctx.AgentMessage) []agentctx.AgentMessage {
	if len(messages) == 0 {
		return messages
	}
	result := make([]agentctx.AgentMessage, 0, len(messages))
	for _, msg := range messages {
		if msg.Metadata != nil && msg.Metadata.Kind == CanaryKind {
			continue
		}
		result = append(result, msg)
	}
	return result
}
