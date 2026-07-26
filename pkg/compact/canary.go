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

// FindCanaryValue returns the value of the most recent <agent:canary> message.
// Returns empty string if no canary is found.
func FindCanaryValue(messages []agentctx.AgentMessage) string {
	const prefix = `value="`
	const suffix = `"`

	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Metadata != nil && msg.Metadata.Kind == CanaryKind {
			text := msg.ExtractText()
			p := strings.Index(text, prefix)
			if p < 0 {
				continue
			}
			start := p + len(prefix)
			q := strings.Index(text[start:], suffix)
			if q < 0 {
				continue
			}
			return text[start : start+q]
		}
	}
	return ""
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
