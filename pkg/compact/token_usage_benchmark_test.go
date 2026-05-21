package compact

import (
	"strings"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

// TestMeasureCurrentTokenUsage measures the token usage of the current
// context management implementation to establish a baseline for comparison.
func TestMeasureCurrentTokenUsage(t *testing.T) {
	agentCtx := agentctx.NewAgentContext("system")
	agentCtx.LLMContext = "Current task: Implement two-phase context management optimization."

	// Simulate a session with 100 messages (similar to the 161K chars mentioned in task)
	// Pattern: user messages, assistant tool calls, large tool results
	for i := 0; i < 100; i++ {
		if i%3 == 0 {
			// User message
			agentCtx.RecentMessages = append(agentCtx.RecentMessages,
				agentctx.NewUserMessage("Task step "+string(rune('a'+i%26))+": Continue working on the optimization task"))
		} else if i%3 == 1 {
			// Assistant tool call
			agentCtx.RecentMessages = append(agentCtx.RecentMessages,
				agentctx.NewAssistantMessage())
		} else {
			// Large tool result (simulate bash output, grep results, etc.)
			toolCallID := "call_" + string(rune('0'+i%10)) + "_" + string(rune('0'+(i/10)%10))
			output := strings.Repeat("DEBUG: spawned test monster at coordinates (", 100) +
				strings.Repeat("x", 2000) + // Large content
				"failed to spawn at (%d,%d)\n"
			agentCtx.RecentMessages = append(agentCtx.RecentMessages,
				agentctx.NewToolResultMessage(
					toolCallID,
					"bash",
					[]agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: output}},
					false,
				))
		}
	}

	// Add 5 protected messages at the end
	for i := 0; i < 5; i++ {
		agentCtx.RecentMessages = append(agentCtx.RecentMessages,
			agentctx.NewUserMessage("Recent user message "+string(rune('a'+i%26))))
	}

	// Build context management messages using current implementation
	compactor := NewContextManager(DefaultContextManagerConfig(), llmModelStub(), "", 200000, "system", nil)
	msgs := compactor.buildContextMgmtMessages(agentCtx)

	// Calculate token count
	var totalChars int
	for _, msg := range msgs {
		totalChars += len(msg.Content)
	}
	totalTokens := totalChars / 4 // rough estimate

	t.Logf("=== Current Implementation Token Usage ===")
	t.Logf("Total messages in conversation: %d", len(agentCtx.RecentMessages))
	t.Logf("Total chars in context management request: %d", totalChars)
	t.Logf("Estimated tokens: %d", totalTokens)
	t.Logf("Context window: 200000")
	t.Logf("Meta overhead: %.2f%%", float64(totalTokens)/200000*100)

	// This establishes the baseline we're trying to improve
	// Expected: ~45K tokens as mentioned in the task
	// Goal: < 5K tokens with two-phase approach
	if totalTokens < 40000 {
		t.Logf("WARNING: Token usage (%d) is lower than expected baseline (~45K). Test data may not be representative.", totalTokens)
	}
}

// TestBuildPhase1MetadataMessages tests the new metadata-based approach
// that will be implemented as phase 1 of two-phase context management.
func TestBuildPhase1MetadataMessages(t *testing.T) {
	agentCtx := agentctx.NewAgentContext("system")
	agentCtx.LLMContext = "Current task: Implement two-phase context management optimization."

	// Build similar test data as above
	for i := 0; i < 100; i++ {
		if i%3 == 0 {
			agentCtx.RecentMessages = append(agentCtx.RecentMessages,
				agentctx.NewUserMessage("Task step "+string(rune('a'+i%26))+": Continue working on the optimization task"))
		} else if i%3 == 1 {
			agentCtx.RecentMessages = append(agentCtx.RecentMessages,
				agentctx.NewAssistantMessage())
		} else {
			toolCallID := "call_" + string(rune('0'+i%10)) + "_" + string(rune('0'+(i/10)%10))
			output := strings.Repeat("DEBUG: spawned test monster at coordinates (", 100) +
				strings.Repeat("x", 2000) +
				"failed to spawn at (%d,%d)\n"
			agentCtx.RecentMessages = append(agentCtx.RecentMessages,
				agentctx.NewToolResultMessage(
					toolCallID,
					"bash",
					[]agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: output}},
					false,
				))
		}
	}

	for i := 0; i < 5; i++ {
		agentCtx.RecentMessages = append(agentCtx.RecentMessages,
			agentctx.NewUserMessage("Recent user message "+string(rune('a'+i%26))))
	}

	// This test will be implemented in phase 1 of the task
	// For now, it's a placeholder to verify the expected metadata structure
	t.Skip("To be implemented in phase 1")
}