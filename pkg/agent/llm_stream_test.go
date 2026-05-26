package agent

import (
	"bytes"
	"context"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/stretchr/testify/assert"
)

// TestCacheFirstRuntimeStatePersist verifies AS-1: In cache-first mode,
// runtime_state messages are appended to RecentMessages as persistent
// AgentMessages, and the serialized LLM message prefix stays stable across turns.
func TestCacheFirstRuntimeStatePersist(t *testing.T) {
	agentCtx := &agentctx.AgentContext{
		RecentMessages: []agentctx.AgentMessage{
			agentctx.NewUserMessage("hello"),
			agentctx.NewAssistantMessage(),
		},
		AgentState: &agentctx.AgentState{},
	}

	initialLen := len(agentCtx.RecentMessages)

	// Simulate 3 turns of cache-first persistence.
	// In cache-first mode, runtime_state is appended as a persistent AgentMessage.
	turnSnapshots := make([][]llm.LLMMessage, 3)
	for turn := 0; turn < 3; turn++ {
		// Simulate appending a runtime_state message (cache-first path).
		runtimeMsg := agentctx.AgentMessage{
			Role:      "user",
					Content:   []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "runtime_state_turn_" + string(rune('0'+turn))}},
			Timestamp: int64(turn),
			Metadata:  &agentctx.MessageMetadata{Kind: "runtime_state"},
		}
		agentCtx.RecentMessages = append(agentCtx.RecentMessages, runtimeMsg)

		// Also append a user and assistant message for realism.
		agentCtx.RecentMessages = append(agentCtx.RecentMessages,
			agentctx.NewUserMessage("user turn "+string(rune('0'+turn))),
		)
		agentCtx.RecentMessages = append(agentCtx.RecentMessages,
			agentctx.NewAssistantMessage(),
		)

		// Serialize via selectMessagesForLLM + ConvertMessagesToLLM.
		selected, _ := selectMessagesForLLM(agentCtx)
		llmMessages := ConvertMessagesToLLM(context.Background(), selected)
		turnSnapshots[turn] = llmMessages
	}

	// Verify: 3 runtime_state messages were persisted.
	runtimeCount := 0
	for _, msg := range agentCtx.RecentMessages {
		if msg.Metadata != nil && msg.Metadata.Kind == "runtime_state" {
			runtimeCount++
		}
	}
	assert.Equal(t, 3, runtimeCount, "all 3 runtime_state messages should be in RecentMessages")

	// Verify: message count increased by 3 per turn (1 runtime + 1 user + 1 assistant).
	assert.Equal(t, initialLen+9, len(agentCtx.RecentMessages),
		"RecentMessages should have grown by 9 (3 turns × 3 messages)")

	// Verify: the last runtime_state message has correct metadata.
	lastRuntime := -1
	for i, msg := range agentCtx.RecentMessages {
		if msg.Metadata != nil && msg.Metadata.Kind == "runtime_state" {
			lastRuntime = i
		}
	}
	assert.True(t, lastRuntime >= 0, "should find at least one runtime_state message")
	assert.Equal(t, "runtime_state", agentCtx.RecentMessages[lastRuntime].Metadata.Kind)

	// Verify prefix stability: Turn N-1's full messages are a prefix of Turn N's messages.
	for i := 1; i < len(turnSnapshots); i++ {
		prevLen := len(turnSnapshots[i-1])
		currLen := len(turnSnapshots[i])
		assert.Greater(t, currLen, prevLen,
			"turn %d should have more messages than turn %d", i, i-1)

		// The first prevLen messages of turn i should equal all of turn i-1.
		// Compare by serializing role+content for determinism.
		prevSerialized := serializeLLMMessages(turnSnapshots[i-1])
		currPrefix := serializeLLMMessages(turnSnapshots[i][:prevLen])
		assert.True(t, bytes.Equal(prevSerialized, currPrefix),
			"turn %d prefix should equal turn %d full messages", i, i-1)
	}
}

// TestContextFirstRuntimeStateEphemeral verifies AS-2: In context-first mode,
// runtime_state is injected ephemerally via insertBeforeLastUserMessage and
// does NOT change RecentMessages.
func TestContextFirstRuntimeStateEphemeral(t *testing.T) {
	agentCtx := &agentctx.AgentContext{
		RecentMessages: []agentctx.AgentMessage{
			agentctx.NewUserMessage("hello"),
			agentctx.NewAssistantMessage(),
			agentctx.NewUserMessage("what is 2+2?"),
		},
		AgentState: &agentctx.AgentState{},
	}

	originalLen := len(agentCtx.RecentMessages)
	originalMessages := make([]agentctx.AgentMessage, len(agentCtx.RecentMessages))
	copy(originalMessages, agentCtx.RecentMessages)

	// Simulate context-first path: serialize messages to LLM format.
	selected, _ := selectMessagesForLLM(agentCtx)
	llmMessages := ConvertMessagesToLLM(context.Background(), selected)

	// Inject ephemeral runtime_state before last user message.
	runtimeMsg := llm.LLMMessage{
		Role:    "user",
		Content: "<agent:runtime_state/>",
	}
	result := insertBeforeLastUserMessage(llmMessages, runtimeMsg)

	// Verify: RecentMessages is UNCHANGED (no persistent runtime_state).
	assert.Equal(t, originalLen, len(agentCtx.RecentMessages),
		"RecentMessages should not be modified in context-first mode")
	for i := range agentCtx.RecentMessages {
		assert.Equal(t, originalMessages[i].Metadata, agentCtx.RecentMessages[i].Metadata,
			"message %d metadata should be unchanged", i)
	}

	// Verify: the returned slice has one more message than the original.
	assert.Equal(t, len(llmMessages)+1, len(result),
		"insertBeforeLastUserMessage should add exactly one message")

	// Verify: the runtime message is placed before the last user message.
	// Find the runtime message in the result.
	runtimeIdx := -1
	lastUserIdx := -1
	for i, msg := range result {
		if msg.Content == "<agent:runtime_state/>" {
			runtimeIdx = i
		}
	}
	for i := len(result) - 1; i >= 0; i-- {
		if result[i].Role == "user" && result[i].Content != "<agent:runtime_state/>" {
			lastUserIdx = i
			break
		}
	}
	assert.True(t, runtimeIdx >= 0, "runtime message should be found in result")
	assert.True(t, lastUserIdx >= 0, "last user message should be found in result")
	assert.Equal(t, runtimeIdx+1, lastUserIdx,
		"runtime message should be immediately before the last user message")

	// Verify: no runtime_state in RecentMessages.
	for _, msg := range agentCtx.RecentMessages {
		if msg.Metadata != nil {
			assert.NotEqual(t, "runtime_state", msg.Metadata.Kind,
				"RecentMessages should not contain runtime_state in context-first mode")
		}
	}
}

// TestPrefixConsistency verifies AS-5: Over 10 turns in cache-first mode,
// each turn's serialized []llm.LLMMessage has a prefix that is byte-identical
// to the previous turn's full message list, and message count grows monotonically.
func TestPrefixConsistency(t *testing.T) {
	agentCtx := &agentctx.AgentContext{
		RecentMessages: []agentctx.AgentMessage{},
		AgentState:     &agentctx.AgentState{},
	}

	numTurns := 10
	turnSnapshots := make([][]llm.LLMMessage, numTurns)

	for turn := 0; turn < numTurns; turn++ {
		// Each turn: user message → runtime_state → assistant message.
		agentCtx.RecentMessages = append(agentCtx.RecentMessages,
			agentctx.NewUserMessage("user turn"))
		agentCtx.RecentMessages = append(agentCtx.RecentMessages, agentctx.AgentMessage{
			Role:      "user",
			Content:   []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "runtime"}},
			Timestamp: int64(turn),
			Metadata:  &agentctx.MessageMetadata{Kind: "runtime_state"},
		})
		agentCtx.RecentMessages = append(agentCtx.RecentMessages,
			agentctx.NewAssistantMessage())

		selected, _ := selectMessagesForLLM(agentCtx)
		llmMessages := ConvertMessagesToLLM(context.Background(), selected)
		turnSnapshots[turn] = llmMessages
	}

	// Verify monotonic growth.
	for i := 1; i < numTurns; i++ {
		assert.Greater(t, len(turnSnapshots[i]), len(turnSnapshots[i-1]),
			"turn %d should have strictly more messages than turn %d", i, i-1)
	}

	// Verify prefix stability: each turn's prefix equals the previous turn's full messages.
	for i := 1; i < numTurns; i++ {
		prevLen := len(turnSnapshots[i-1])
		prevSerialized := serializeLLMMessages(turnSnapshots[i-1])
		currPrefix := serializeLLMMessages(turnSnapshots[i][:prevLen])
		assert.True(t, bytes.Equal(prevSerialized, currPrefix),
			"turn %d prefix should be byte-identical to turn %d full messages", i, i-1)
	}
}

// serializeLLMMessages produces a deterministic byte representation of []llm.LLMMessage
// for comparison purposes. We use Role+Content as the stable key since other fields
// like ToolCalls may be nil vs empty slice.
func serializeLLMMessages(msgs []llm.LLMMessage) []byte {
	var buf bytes.Buffer
	for _, msg := range msgs {
		buf.WriteString(msg.Role)
		buf.WriteByte(0)
		buf.WriteString(msg.Content)
		buf.WriteByte(0)
		// Also include ToolCallID to distinguish tool messages.
		buf.WriteString(msg.ToolCallID)
		buf.WriteByte(0)
	}
	return buf.Bytes()
}