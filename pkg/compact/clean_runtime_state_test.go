package compact

import (
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/stretchr/testify/assert"
)

// TestCompactionCleansRuntimeState verifies AS-4: cleanOldRuntimeState removes
// all but the last runtime_state message, leaving other messages untouched.
func TestCompactionCleansRuntimeState(t *testing.T) {
	runtimeStateKind := "runtime_state"

	makeRuntimeMsg := func(content string) agentctx.AgentMessage {
		return agentctx.AgentMessage{
			Role:    "user",
			Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: content}},
			Metadata: &agentctx.MessageMetadata{Kind: runtimeStateKind},
		}
	}

	t.Run("no runtime_state messages unchanged", func(t *testing.T) {
		input := []agentctx.AgentMessage{
			agentctx.NewUserMessage("hello"),
			agentctx.NewAssistantMessage(),
		}
		result := cleanOldRuntimeState(input)
		assert.Equal(t, len(input), len(result), "should not remove any messages")
	})

	t.Run("single runtime_state kept", func(t *testing.T) {
		input := []agentctx.AgentMessage{
			agentctx.NewUserMessage("hello"),
			makeRuntimeMsg("runtime_0"),
		}
		result := cleanOldRuntimeState(input)
		assert.Equal(t, 2, len(result), "should keep both messages")
		// The runtime_state message should still be there.
		foundRuntime := false
		for _, msg := range result {
			if msg.Metadata != nil && msg.Metadata.Kind == runtimeStateKind {
				foundRuntime = true
			}
		}
		assert.True(t, foundRuntime, "runtime_state message should be preserved")
	})

	t.Run("three runtime_state only last kept", func(t *testing.T) {
		input := []agentctx.AgentMessage{
			makeRuntimeMsg("runtime_0"),
			makeRuntimeMsg("runtime_1"),
			makeRuntimeMsg("runtime_2"),
		}
		result := cleanOldRuntimeState(input)
		assert.Equal(t, 1, len(result), "should keep only the last runtime_state")
		assert.Equal(t, "runtime_2", result[0].ExtractText())
	})

	t.Run("interleaved runtime_state and user messages", func(t *testing.T) {
		input := []agentctx.AgentMessage{
			agentctx.NewUserMessage("hello"),
			makeRuntimeMsg("runtime_0"),
			agentctx.NewUserMessage("turn 2"),
			makeRuntimeMsg("runtime_1"),
			agentctx.NewAssistantMessage(),
			makeRuntimeMsg("runtime_2"),
		}
		result := cleanOldRuntimeState(input)
		// user "hello" + user "turn 2" + assistant + runtime "runtime_2" = 4
		assert.Equal(t, 4, len(result), "should keep non-runtime messages and last runtime_state")

		// Verify non-runtime messages are preserved.
		assert.Equal(t, "hello", result[0].ExtractText())
		assert.Equal(t, "turn 2", result[1].ExtractText())
		assert.Equal(t, "assistant", result[2].Role)

		// Verify last message is the final runtime_state.
		assert.Equal(t, runtimeStateKind, result[3].Metadata.Kind)
		assert.Equal(t, "runtime_2", result[3].ExtractText())
	})

	t.Run("all messages are runtime_state only last kept", func(t *testing.T) {
		input := []agentctx.AgentMessage{
			makeRuntimeMsg("r0"),
			makeRuntimeMsg("r1"),
			makeRuntimeMsg("r2"),
			makeRuntimeMsg("r3"),
		}
		result := cleanOldRuntimeState(input)
		assert.Equal(t, 1, len(result), "should keep only the last runtime_state")
		assert.Equal(t, "r3", result[0].ExtractText())
	})

	t.Run("empty slice returns empty", func(t *testing.T) {
		input := []agentctx.AgentMessage{}
		result := cleanOldRuntimeState(input)
		assert.Equal(t, 0, len(result), "empty input should return empty result")
	})

	t.Run("nil Metadata messages not affected", func(t *testing.T) {
		msgNoMeta := agentctx.AgentMessage{
			Role:    "user",
			Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "no meta"}},
		}
		input := []agentctx.AgentMessage{
			msgNoMeta,
			makeRuntimeMsg("runtime_0"),
			msgNoMeta,
		}
		result := cleanOldRuntimeState(input)
		assert.Equal(t, 3, len(result), "nil metadata messages should not be affected")
		// The runtime_state in the middle is the only (and last) one, so it stays.
		assert.Equal(t, runtimeStateKind, result[1].Metadata.Kind)
	})
}