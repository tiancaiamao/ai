package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

func TestAppendCompactionHint_AppendsToEnd(t *testing.T) {
	ctx := agentctx.NewAgentContext("sys")
	ctx.RecentMessages = []agentctx.AgentMessage{
		agentctx.NewCompactionSummaryMessage("## Current Task\nDo something"),
		agentctx.NewUserMessage("recent msg 1"),
		agentctx.NewUserMessage("recent msg 2"),
	}

	AppendCompactionHint(ctx)

	// Message count increased by 1
	assert.Len(t, ctx.RecentMessages, 4)

	// Last message is the hint
	last := ctx.RecentMessages[len(ctx.RecentMessages)-1]
	assert.Equal(t, "user", last.Role)
	text := last.ExtractText()
	assert.Contains(t, text, "<agent:hint>")
	assert.Contains(t, text, "Context was just compacted")
	assert.Contains(t, text, "find_skill")

		// AgentVisible=true, UserVisible=false
	assert.True(t, last.IsAgentVisible())
	assert.False(t, last.IsUserVisible())
	assert.Equal(t, "compaction_hint", last.Metadata.Kind)
}

func TestAppendCompactionHint_EmptyMessages(t *testing.T) {
	ctx := agentctx.NewAgentContext("sys")
	// Should still append even with no prior messages
	AppendCompactionHint(ctx)
	assert.Len(t, ctx.RecentMessages, 1)
	assert.Contains(t, ctx.RecentMessages[0].ExtractText(), "<agent:hint>")
}

func TestAppendCompactionHint_OriginalMessagesUntouched(t *testing.T) {
	ctx := agentctx.NewAgentContext("sys")
	ctx.RecentMessages = []agentctx.AgentMessage{
		agentctx.NewCompactionSummaryMessage("original summary"),
	}

	AppendCompactionHint(ctx)

	// Summary message is unchanged
	assert.Contains(t, ctx.RecentMessages[0].ExtractText(), "original summary")
	assert.NotContains(t, ctx.RecentMessages[0].ExtractText(), "<agent:hint>")
}
