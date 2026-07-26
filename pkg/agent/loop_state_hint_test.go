package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tiancaiamao/ai/pkg/compact"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

func TestAppendCompactionHint_AppendsToEnd(t *testing.T) {
	ctx := agentctx.NewAgentContext("sys")
	ctx.RecentMessages = []agentctx.AgentMessage{
		agentctx.NewCompactionSummaryMessage("## Current Task\nDo something"),
		agentctx.NewUserMessage("recent msg 1"),
		agentctx.NewUserMessage("recent msg 2"),
	}

	compact.AppendCompactionHint(ctx)

	// Message count increased by 1
	assert.Len(t, ctx.RecentMessages, 4)

	// Last message is the hint
	last := ctx.RecentMessages[len(ctx.RecentMessages)-1]
	assert.Equal(t, "user", last.Role)
	text := last.ExtractText()
	assert.Contains(t, text, "<agent:hint>")
	assert.Contains(t, text, "Context was just compacted")
	assert.Contains(t, text, "Behavioral Constraints")
	assert.Contains(t, text, "find_skill")
	assert.Contains(t, text, compact.CompactionAckTag)
	assert.Contains(t, text, "I acknowledge the compaction")

	// AgentVisible=true, UserVisible=false
	assert.True(t, last.IsAgentVisible())
	assert.False(t, last.IsUserVisible())
	assert.Equal(t, "compaction_hint", last.Metadata.Kind)
}

func TestAppendCompactionHint_EmptyMessages(t *testing.T) {
	ctx := agentctx.NewAgentContext("sys")
	// Should still append even with no prior messages
	compact.AppendCompactionHint(ctx)
	assert.Len(t, ctx.RecentMessages, 1)
	assert.Contains(t, ctx.RecentMessages[0].ExtractText(), "<agent:hint>")
}

func TestAppendCompactionHint_OriginalMessagesUntouched(t *testing.T) {
	ctx := agentctx.NewAgentContext("sys")
	ctx.RecentMessages = []agentctx.AgentMessage{
		agentctx.NewCompactionSummaryMessage("original summary"),
	}

	compact.AppendCompactionHint(ctx)

	// Summary message is unchanged
	assert.Contains(t, ctx.RecentMessages[0].ExtractText(), "original summary")
	assert.NotContains(t, ctx.RecentMessages[0].ExtractText(), "<agent:hint>")
}

func TestCheckCompactionHintAcknowledged_NoHint_ReturnsTrue(t *testing.T) {
	ctx := agentctx.NewAgentContext("sys")
	ctx.RecentMessages = []agentctx.AgentMessage{
		agentctx.NewUserMessage("hello"),
		agentctx.NewAssistantMessage(),
	}
	assert.True(t, checkCompactionHintAcknowledged(ctx))
}

func TestCheckCompactionHintAcknowledged_HintWithoutAck_ReturnsFalse(t *testing.T) {
	ctx := agentctx.NewAgentContext("sys")
	hint := agentctx.NewUserMessage("hint").WithKind("compaction_hint")
	ctx.RecentMessages = append(ctx.RecentMessages, hint)

	asst := agentctx.NewAssistantMessage()
	asst.Content = []agentctx.ContentBlock{
		agentctx.TextContent{Type: "text", Text: "ok I'll do it"},
	}
	ctx.RecentMessages = append(ctx.RecentMessages, asst)

	assert.False(t, checkCompactionHintAcknowledged(ctx))
}

func TestCheckCompactionHintAcknowledged_HintWithAck_ReturnsTrue(t *testing.T) {
	ctx := agentctx.NewAgentContext("sys")
	hint := agentctx.NewUserMessage("hint").WithKind("compaction_hint")
	ctx.RecentMessages = append(ctx.RecentMessages, hint)

	asst := agentctx.NewAssistantMessage()
	asst.Content = []agentctx.ContentBlock{
		agentctx.TextContent{Type: "text", Text: compact.CompactionAckTag + "I acknowledge the compaction" + compact.CompactionAckTag},
	}
	ctx.RecentMessages = append(ctx.RecentMessages, asst)

	assert.True(t, checkCompactionHintAcknowledged(ctx))
}

func TestCheckCompactionHintAcknowledged_ToolCallsOnly_ReturnsFalse(t *testing.T) {
	ctx := agentctx.NewAgentContext("sys")
	hint := agentctx.NewUserMessage("hint").WithKind("compaction_hint")
	ctx.RecentMessages = append(ctx.RecentMessages, hint)

	asst := agentctx.NewAssistantMessage()
	asst.Content = []agentctx.ContentBlock{
		agentctx.ToolCallContent{ID: "c1", Type: "function", Name: "read", Arguments: map[string]any{}},
	}
	ctx.RecentMessages = append(ctx.RecentMessages, asst)

	assert.False(t, checkCompactionHintAcknowledged(ctx))
}

func TestNewCompactionHintReminder(t *testing.T) {
	reminder := newCompactionHintReminder()
	assert.Equal(t, "user", reminder.Role)
	assert.True(t, reminder.IsAgentVisible())
	assert.False(t, reminder.IsUserVisible())
	assert.Equal(t, "compaction_hint_reminder", reminder.Metadata.Kind)
	text := reminder.ExtractText()
	assert.Contains(t, text, compact.CompactionAckTag)
	assert.Contains(t, text, "I acknowledge the compaction")
}

func TestCheckCompactionHintAcknowledged_ReminderWithoutAck_ReturnsFalse(t *testing.T) {
	ctx := agentctx.NewAgentContext("sys")
	// Simulate: hint → assistant (tool calls, no ack) → reminder → assistant (tool calls, no ack)
	ctx.RecentMessages = append(ctx.RecentMessages,
		agentctx.NewUserMessage("hint").WithKind("compaction_hint"),
		func() agentctx.AgentMessage {
			m := agentctx.NewAssistantMessage()
			m.Content = []agentctx.ContentBlock{
				agentctx.ToolCallContent{ID: "c1", Type: "function", Name: "read", Arguments: map[string]any{}},
			}
			return m
		}(),
		agentctx.NewUserMessage("reminder").WithKind("compaction_hint_reminder"),
	)
	asst := agentctx.NewAssistantMessage()
	asst.Content = []agentctx.ContentBlock{
		agentctx.ToolCallContent{ID: "c2", Type: "function", Name: "write", Arguments: map[string]any{}},
	}
	ctx.RecentMessages = append(ctx.RecentMessages, asst)

	assert.False(t, checkCompactionHintAcknowledged(ctx),
		"should return false when reminder is present and assistant makes tool calls without ack")
}

func TestCheckCompactionHintAcknowledged_ReminderWithAck_ReturnsTrue(t *testing.T) {
	ctx := agentctx.NewAgentContext("sys")
	ctx.RecentMessages = append(ctx.RecentMessages,
		agentctx.NewUserMessage("hint").WithKind("compaction_hint"),
		func() agentctx.AgentMessage {
			m := agentctx.NewAssistantMessage()
			m.Content = []agentctx.ContentBlock{
				agentctx.ToolCallContent{ID: "c1", Type: "function", Name: "read", Arguments: map[string]any{}},
			}
			return m
		}(),
		agentctx.NewUserMessage("reminder").WithKind("compaction_hint_reminder"),
	)
	asst := agentctx.NewAssistantMessage()
	asst.Content = []agentctx.ContentBlock{
		agentctx.TextContent{Type: "text", Text: compact.CompactionAckTag + "I acknowledge the compaction" + compact.CompactionAckTag},
	}
	ctx.RecentMessages = append(ctx.RecentMessages, asst)

	assert.True(t, checkCompactionHintAcknowledged(ctx),
		"should return true when reminder is present and assistant acknowledges")
}
