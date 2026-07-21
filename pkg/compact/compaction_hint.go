package compact

import (
	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// CompactionAckTag is the XML tag the LLM must include in its response to
// acknowledge a compaction hint. Detection is done via strings.Contains.
const CompactionAckTag = "<compaction_ack>"

// AppendCompactionHint appends a new user-role hint message at the END of
// RecentMessages after a successful compaction. The LLM's last input is the
// most attention-grabbing position, making it ideal for a "reload your skills"
// reminder. The message uses kind "compaction_hint" and is AgentVisible only.
// It also requires the LLM to acknowledge the hint with a CompactionAckTag
// marker, verified by checkCompactionHintAcknowledged after the next response.
func AppendCompactionHint(agentCtx *agentctx.AgentContext) {
	hint := `<agent:hint>
Context was just compacted. The compaction summary preserves key information:
1. "Skills Loaded" lists skills whose full content is now LOST. Reload via find_skill(name="<skill>", load=true) if you need the full details.
2. "Behavioral Constraints" captures process rules from loaded skills — follow these even though the skill content is gone.
3. Similarly, re-read any design docs or important files you were working with. Don't proceed on stale memory.

Please confirm you have read and understood this hint by including this acknowledgment in your response:
` + CompactionAckTag + `I acknowledge the compaction and will follow the instructions above` + CompactionAckTag + `
</agent:hint>`

	msg := agentctx.NewUserMessage(hint).
		WithKind("compaction_hint").
		WithVisibility(true, false)

	agentCtx.RecentMessages = append(agentCtx.RecentMessages, msg)
}
