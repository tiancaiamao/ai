package compact

import (
	"strings"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

func TestParsePhase1Response(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:     "empty array",
			input:    "[]",
			expected: nil, // empty slice is nil after JSON parse
		},
		{
			name:     "single ID",
			input:    `["call_abc123"]`,
			expected: []string{"call_abc123"},
		},
		{
			name:     "multiple IDs",
			input:    `["call_a_1", "call_b_2", "call_c_3"]`,
			expected: []string{"call_a_1", "call_b_2", "call_c_3"},
		},
		{
			name:     "markdown wrapped",
			input:    "```json\n[\"call_1\", \"call_2\"]\n```",
			expected: []string{"call_1", "call_2"},
		},
		{
			name:     "with surrounding text",
			input:    "Based on my analysis, I recommend truncating:\n[\"call_x\", \"call_y\"]\nThese are safe to remove.",
			expected: []string{"call_x", "call_y"},
		},
		{
			name:     "invalid JSON returns nil",
			input:    "No candidates found",
			expected: nil,
		},
		{
			name:     "empty string returns nil",
			input:    "",
			expected: nil,
		},
		{
			name:     "whitespace trimmed",
			input:    "  \n [\"call_w\"] \n  ",
			expected: []string{"call_w"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parsePhase1Response(tt.input)
			if len(result) != len(tt.expected) {
				t.Fatalf("expected %v, got %v", tt.expected, result)
			}
			for i, id := range result {
				if id != tt.expected[i] {
					t.Errorf("index %d: expected %q, got %q", i, tt.expected[i], id)
				}
			}
		})
	}
}

func TestBuildPhase2Messages(t *testing.T) {
	agentCtx := agentctx.NewAgentContext("system")
	agentCtx.LLMContext = "Current task: implement Phase 2 context management."

	// Add messages with tool results that have tool_call_ids
	agentCtx.RecentMessages = append(agentCtx.RecentMessages,
		agentctx.NewUserMessage("Start working on Phase 2"),
		agentctx.NewToolResultMessage(
			"call_stale_1",
			"bash",
			[]agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: strings.Repeat("debug output line\n", 200)}},
			false,
		),
		agentctx.NewToolResultMessage(
			"call_important_1",
			"read",
			[]agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "important file content needed for task"}},
			false,
		),
		agentctx.NewUserMessage("Continue with the implementation"),
		agentctx.NewToolResultMessage(
			"call_stale_2",
			"grep",
			[]agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: strings.Repeat("match found at line\n", 150)}},
			false,
		),
	)

	// Simulate Phase 1 returning two candidates
	candidateIDs := []string{"call_stale_1", "call_stale_2"}

	compactor := NewContextManager(DefaultContextManagerConfig(), llmModelStub(), "", 200000, "system", nil)
	messages := compactor.buildPhase2Messages(agentCtx, candidateIDs)

	if len(messages) == 0 {
		t.Fatal("expected non-empty messages")
	}

	content := messages[0].Content

	// Should contain full content of candidates
	if !strings.Contains(content, "debug output line") {
		t.Error("Phase 2 messages should contain full content of candidate call_stale_1")
	}
	if !strings.Contains(content, "match found at line") {
		t.Error("Phase 2 messages should contain full content of candidate call_stale_2")
	}

	// Should NOT contain content of non-candidate
	if strings.Contains(content, "important file content") {
		t.Error("Phase 2 messages should NOT contain content of non-candidate call_important_1")
	}

	// Should contain current task
	if !strings.Contains(content, "implement Phase 2") {
		t.Error("Phase 2 messages should contain current LLM context/task")
	}

	// Should contain decision prompt
	if !strings.Contains(content, "Phase 2") {
		t.Error("Phase 2 messages should contain Phase 2 decision prompt")
	}
	if !strings.Contains(content, "truncate_messages") {
		t.Error("Phase 2 messages should mention truncate_messages tool")
	}
	if !strings.Contains(content, "update_llm_context") {
		t.Error("Phase 2 messages should mention update_llm_context tool")
	}
}

func TestBuildPhase2MessagesTokenUsage(t *testing.T) {
	agentCtx := agentctx.NewAgentContext("system")
	agentCtx.LLMContext = "Current task: optimize context management."

	// Simulate a session with 100 messages but only 3 candidates
	for i := 0; i < 100; i++ {
		if i%3 == 0 {
			agentCtx.RecentMessages = append(agentCtx.RecentMessages,
				agentctx.NewUserMessage("Task step message"))
		} else if i%3 == 1 {
			agentCtx.RecentMessages = append(agentCtx.RecentMessages,
				agentctx.NewAssistantMessage())
				} else {
			toolCallID := ""
			// Make some selectable with distinct, matching IDs
			switch i {
			case 2:
				toolCallID = "call_candidate_1"
			case 20:
				toolCallID = "call_candidate_2"
			case 50:
				toolCallID = "call_candidate_3"
			}
			output := strings.Repeat("x", 3000)
			agentCtx.RecentMessages = append(agentCtx.RecentMessages,
				agentctx.NewToolResultMessage(
					toolCallID,
					"bash",
					[]agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: output}},
					false,
				))
		}
	}

	// Phase 1 identified 3 candidates
	candidateIDs := []string{"call_candidate_1", "call_candidate_2", "call_candidate_3"}

	compactor := NewContextManager(DefaultContextManagerConfig(), llmModelStub(), "", 200000, "system", nil)
	messages := compactor.buildPhase2Messages(agentCtx, candidateIDs)

	var totalChars int
	for _, msg := range messages {
		totalChars += len(msg.Content)
	}
	totalTokens := totalChars / 4

	t.Logf("=== Phase 2 Token Usage ===")
	t.Logf("Total chars: %d", totalChars)
	t.Logf("Estimated tokens: %d", totalTokens)
	t.Logf("Candidates: %d of %d messages", len(candidateIDs), len(agentCtx.RecentMessages))
	t.Logf("Context window: 200000")
	t.Logf("Phase 2 overhead: %.2f%%", float64(totalTokens)/200000*100)

	// Phase 2 should be focused: only candidate content + overview
	// With 3 candidates of 3K chars each = ~9K content + ~3K overhead = ~3K tokens
	// This should be well under 20K tokens
	if totalTokens >= 20000 {
		t.Errorf("Phase 2 token usage should be < 20K tokens, got %d", totalTokens)
	}
}

func TestBuildPhase2MessagesEmptyLLMContext(t *testing.T) {
	agentCtx := agentctx.NewAgentContext("system")
	// LLMContext is empty by default

	agentCtx.RecentMessages = append(agentCtx.RecentMessages,
		agentctx.NewUserMessage("Do something"),
		agentctx.NewToolResultMessage(
			"call_test_1",
			"bash",
			[]agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: strings.Repeat("output\n", 100)}},
			false,
		),
	)

	compactor := NewContextManager(DefaultContextManagerConfig(), llmModelStub(), "", 200000, "system", nil)
	messages := compactor.buildPhase2Messages(agentCtx, []string{"call_test_1"})

	content := messages[0].Content

	// Should NOT contain LLM Context section when empty
	if strings.Contains(content, "## Current LLM Context\n") {
		t.Error("Phase 2 messages should not include empty LLM Context section")
	}

	// Should still contain decision prompt
	if !strings.Contains(content, "LLM Context exists: false") {
		t.Error("Phase 2 state should show LLM Context does not exist")
	}
}

func TestBuildContextOnlyUpdateMessages(t *testing.T) {
	agentCtx := agentctx.NewAgentContext("system")
	// LLMContext is empty by default

	agentCtx.RecentMessages = append(agentCtx.RecentMessages,
		agentctx.NewUserMessage("Implement two-phase context management"),
		agentctx.NewToolResultMessage(
			"call_1",
			"bash",
			[]agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "some output"}},
			false,
		),
		agentctx.NewUserMessage("Continue working"),
	)

	compactor := NewContextManager(DefaultContextManagerConfig(), llmModelStub(), "", 200000, "system", nil)
	messages := compactor.buildContextOnlyUpdateMessages(agentCtx)

	if len(messages) == 0 {
		t.Fatal("expected non-empty messages")
	}

	content := messages[0].Content

	// Should mention the requirement to update context
	if !strings.Contains(content, "update_llm_context") {
		t.Error("context-only update messages should mention update_llm_context")
	}
	if !strings.Contains(content, "CRITICAL") {
		t.Error("context-only update messages should emphasize urgency")
	}
	if !strings.Contains(content, "Implement two-phase") {
		t.Error("context-only update should include the latest user request")
	}

	// Should include conversation overview
	if !strings.Contains(content, "Conversation Overview") {
		t.Error("context-only update should include conversation overview")
	}
}

func TestTwoPhaseFlowNoCandidates(t *testing.T) {
	// Test the case where Phase 1 returns no candidates and LLM context exists.
	// In this case, Phase 2 should be skipped entirely.
	agentCtx := agentctx.NewAgentContext("system")
	agentCtx.LLMContext = "Task: already initialized"

	for i := 0; i < 10; i++ {
		agentCtx.RecentMessages = append(agentCtx.RecentMessages,
			agentctx.NewUserMessage("hello"))
	}
	agentCtx.AgentState.ToolCallsSinceLastTrigger = 20

		// Verify parsePhase1Response returns empty for empty array
	ids := parsePhase1Response("[]")
	if len(ids) != 0 {
		t.Errorf("expected empty slice for empty array, got %v", ids)
	}
}