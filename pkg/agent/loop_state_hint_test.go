package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

func TestAppendCompactionHint_AddsHintToSummary(t *testing.T) {
	ctx := agentctx.NewAgentContext("sys")
	ctx.RecentMessages = []agentctx.AgentMessage{
		agentctx.NewCompactionSummaryMessage("## Current Task\nDo something"),
		agentctx.NewUserMessage("recent msg"),
	}

	AppendCompactionHint(ctx)

	summary := ctx.RecentMessages[0].ExtractText()
	assert.Contains(t, summary, "<agent:hint>")
	assert.Contains(t, summary, "Context was just compacted")
	assert.Contains(t, summary, "find_skill")
	// The original summary text is still there
	assert.Contains(t, summary, "Do something")
	// Message count unchanged (no new message inserted)
	assert.Len(t, ctx.RecentMessages, 2)
}

func TestAppendCompactionHint_Idempotent(t *testing.T) {
	ctx := agentctx.NewAgentContext("sys")
	ctx.RecentMessages = []agentctx.AgentMessage{
		agentctx.NewCompactionSummaryMessage("summary text"),
	}

	AppendCompactionHint(ctx)
	textAfterFirst := ctx.RecentMessages[0].ExtractText()
	assert.Contains(t, textAfterFirst, "<agent:hint>")

	// Second call should be a no-op
	AppendCompactionHint(ctx)
	textAfterSecond := ctx.RecentMessages[0].ExtractText()
	assert.Equal(t, textAfterFirst, textAfterSecond)
}

func TestAppendCompactionHint_NoSummaryMessage(t *testing.T) {
	ctx := agentctx.NewAgentContext("sys")
	ctx.RecentMessages = []agentctx.AgentMessage{
		agentctx.NewUserMessage("regular user message"),
	}

	// Should do nothing — first message is not a compactionSummary
	AppendCompactionHint(ctx)

	text := ctx.RecentMessages[0].ExtractText()
	assert.NotContains(t, text, "<agent:hint>")
}

func TestAppendCompactionHint_EmptyMessages(t *testing.T) {
	ctx := agentctx.NewAgentContext("sys")
	// Should not panic
	AppendCompactionHint(ctx)
}
