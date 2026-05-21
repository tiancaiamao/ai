package compact

import (
	"strings"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

func TestCollectMessageMetadata(t *testing.T) {
	agentCtx := agentctx.NewAgentContext("system")

	// Add various types of messages
	agentCtx.RecentMessages = append(agentCtx.RecentMessages,
		agentctx.NewUserMessage("First user message about task X"),
		agentctx.NewToolResultMessage(
			"call_1_0",
			"bash",
			[]agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: strings.Repeat("x", 5000)}},
			false,
		),
		agentctx.NewToolResultMessage(
			"", // Empty tool_call_id - not selectable
			"grep",
			[]agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: strings.Repeat("y", 3000)}},
			false,
		),
		agentctx.NewUserMessage("Second user message"),
		agentctx.NewToolResultMessage(
			"call_3_0",
			"bash",
			[]agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: strings.Repeat("z", 300)}}, // < 500 chars, not selectable
			false,
		),
		// Add 5 protected messages at the end
	)
	for i := 0; i < 5; i++ {
		agentCtx.RecentMessages = append(agentCtx.RecentMessages,
			agentctx.NewToolResultMessage(
				"call_protected_"+string(rune('0'+i)),
				"bash",
				[]agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: strings.Repeat("protected", 500)}},
				false,
			),
		)
	}

	metadata := collectMessageMetadata(agentCtx)

	// Verify we got metadata for all agent-visible messages
	expectedVisible := len(agentCtx.RecentMessages)
	if len(metadata) != expectedVisible {
		t.Errorf("Expected %d metadata entries, got %d", expectedVisible, len(metadata))
	}

	// Check user message metadata
	userMsg := metadata[0]
	if userMsg.Role != "user" {
		t.Errorf("Expected role=user, got %s", userMsg.Role)
	}
	if userMsg.ContentPreview == "" {
		t.Error("Expected non-empty content preview for user message")
	}
	if !strings.Contains(userMsg.ContentPreview, "First user message") {
		t.Errorf("Content preview should contain message text, got: %s", userMsg.ContentPreview)
	}

	// Check selectable tool result
	toolMsg := metadata[1]
	if toolMsg.Role != "toolResult" {
		t.Errorf("Expected role=toolResult, got %s", toolMsg.Role)
	}
	if !toolMsg.IsSelectable {
		t.Error("Expected tool message with valid ID and >=500 chars to be selectable")
	}
	if toolMsg.ToolName != "bash" {
		t.Errorf("Expected ToolName=bash, got %s", toolMsg.ToolName)
	}
	if toolMsg.ToolCallID != "call_1_0" {
		t.Errorf("Expected ToolCallID=call_1_0, got %s", toolMsg.ToolCallID)
	}

	// Check non-selectable tool result (missing ID)
	nonSelectableMsg := metadata[2]
	if nonSelectableMsg.IsSelectable {
		t.Error("Expected tool message with empty ID to be non-selectable")
	}

	// Check small tool result (<500 chars)
	smallMsg := metadata[4]
	if smallMsg.IsSelectable {
		t.Error("Expected small tool message (<500 chars) to be non-selectable")
	}

	// Check protected messages
	protectedCount := 0
	for _, meta := range metadata {
		if meta.IsProtected {
			protectedCount++
			if meta.IsSelectable {
				t.Errorf("Protected message at index %d should not be selectable", meta.Index)
			}
		}
	}
	if protectedCount != agentctx.RecentMessagesKeep {
		t.Errorf("Expected %d protected messages, got %d", agentctx.RecentMessagesKeep, protectedCount)
	}
}

func TestBuildPhase1Messages(t *testing.T) {
	agentCtx := agentctx.NewAgentContext("system")
	agentCtx.LLMContext = "Current task: Implement two-phase context management."

	// Build a similar test session to the baseline test
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

	// Add 5 protected messages
	for i := 0; i < 5; i++ {
		agentCtx.RecentMessages = append(agentCtx.RecentMessages,
			agentctx.NewUserMessage("Recent user message "+string(rune('a'+i%26))))
	}

	compactor := NewContextManager(DefaultContextManagerConfig(), llmModelStub(), "", 200000, "system", nil)
	msgs := compactor.buildPhase1Messages(agentCtx, nil)

	if len(msgs) != 1 {
		t.Fatalf("Expected 1 message (combined metadata + state), got %d", len(msgs))
	}

	// Calculate token count
	var totalChars int
	for _, msg := range msgs {
		totalChars += len(msg.Content)
	}
	totalTokens := totalChars / 4

	t.Logf("=== Phase 1 Token Usage ===")
	t.Logf("Total chars in Phase 1 request: %d", totalChars)
	t.Logf("Estimated tokens: %d", totalTokens)
	t.Logf("Context window: 200000")
	t.Logf("Phase 1 overhead: %.2f%%", float64(totalTokens)/200000*100)

	// Verify Phase 1 is significantly smaller than baseline
	if totalTokens >= 5000 {
		t.Errorf("Phase 1 should use < 5000 tokens, got %d tokens", totalTokens)
	}

	// Verify content contains expected sections
	content := msgs[0].Content
	if !strings.Contains(content, "## Conversation Metadata") {
		t.Error("Expected '## Conversation Metadata' section in Phase 1 messages")
	}
	if !strings.Contains(content, "Truncatable tool outputs:") {
		t.Error("Expected truncatable count in Phase 1 messages")
	}
	if !strings.Contains(content, "Protected messages") {
		t.Error("Expected protected message info in Phase 1 messages")
	}

	// Verify metadata format
	if !strings.Contains(content, "role=user, size=") {
		t.Error("Expected user message metadata format: 'role=user, size='")
	}
	if !strings.Contains(content, "role=toolResult, tool=") {
		t.Error("Expected tool result metadata format: 'role=toolResult, tool='")
	}
	if !strings.Contains(content, "PROTECTED") {
		t.Error("Expected PROTECTED marker in Phase 1 messages")
	}
	if !strings.Contains(content, "NON_TRUNCATABLE") {
		t.Error("Expected NON_TRUNCATABLE marker in Phase 1 messages")
	}
}

func TestBuildPhase1MessagesReducesTokensVsBaseline(t *testing.T) {
	// This test verifies that the optimized implementation is efficient
	agentCtx := agentctx.NewAgentContext("system")
	agentCtx.LLMContext = "Current task: Implement two-phase context management."

	// Build test session
	for i := 0; i < 100; i++ {
		if i%3 == 0 {
			agentCtx.RecentMessages = append(agentCtx.RecentMessages,
				agentctx.NewUserMessage("Task step "+string(rune('a'+i%26))+": Continue working"))
		} else if i%3 == 1 {
			agentCtx.RecentMessages = append(agentCtx.RecentMessages,
				agentctx.NewAssistantMessage())
		} else {
			toolCallID := "call_" + string(rune('0'+i%10)) + "_" + string(rune('0'+(i/10)%10))
			output := strings.Repeat("x", 2000)
			agentCtx.RecentMessages = append(agentCtx.RecentMessages,
				agentctx.NewToolResultMessage(
					toolCallID,
					"bash",
					[]agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: output}},
					false,
				))
		}
	}

	// Add protected messages
	for i := 0; i < 5; i++ {
		agentCtx.RecentMessages = append(agentCtx.RecentMessages,
			agentctx.NewUserMessage("Recent "+string(rune('a'+i%26))))
	}

	compactor := NewContextManager(DefaultContextManagerConfig(), llmModelStub(), "", 200000, "system", nil)

	// Build messages with optimized implementation
	msgs := compactor.buildContextMgmtMessages(agentCtx)
	var totalChars int
	for _, msg := range msgs {
		totalChars += len(msg.Content)
	}
	totalTokens := totalChars / 4

	t.Logf("=== Optimized Implementation Token Usage ===")
	t.Logf("Total chars: %d", totalChars)
	t.Logf("Estimated tokens: %d", totalTokens)
	t.Logf("Context window: 200000")
	t.Logf("Overhead: %.2f%%", float64(totalTokens)/200000*100)

	// Verify it's under 5K tokens
	if totalTokens >= 5000 {
		t.Errorf("Optimized implementation should be < 5000 tokens, got %d", totalTokens)
	}

	// Verify messages contain metadata format
	content := msgs[0].Content + msgs[1].Content
	if !strings.Contains(content, "role=user, size=") {
		t.Error("Expected metadata format: 'role=user, size='")
	}
	if !strings.Contains(content, "role=toolResult, tool=") {
		t.Error("Expected metadata format: 'role=toolResult, tool='")
	}
}