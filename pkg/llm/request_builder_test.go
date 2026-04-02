package llm

import (
	"strings"
	"testing"

	"github.com/tiancaiamao/ai/pkg/context"
)

// TestBuildLLMRequest_LLMContextNotInSystemPrompt tests that the LLMContext
// is NOT injected into the system prompt, but into a user message before
// the last user message, for cache-friendly LLM requests (Category 3.3).
func TestBuildLLMRequest_LLMContextNotInSystemPrompt(t *testing.T) {
	// Given: A snapshot with LLMContext
	snapshot := &context.ContextSnapshot{
		LLMContext: "Task: Implement X",
		RecentMessages: []context.AgentMessage{
			context.NewUserMessage("do task"),
		},
		AgentState: *context.NewAgentState("test-session", "/test/dir"),
	}

	// When: Building LLM request in Normal mode
	request, err := BuildRequest(snapshot, context.ModeNormal, nil, "test-model")
	if err != nil {
		t.Fatalf("BuildRequest failed: %v", err)
	}

	// Then: System prompt is clean (no LLMContext)
	if strings.Contains(request.SystemPrompt, "Implement X") {
		t.Error("System prompt should not contain LLMContext, got:", request.SystemPrompt)
	}

	if request.SystemPrompt == "" {
		t.Error("System prompt should not be empty")
	}

	// And LLMContext is in user messages before last user message
	llmContextMsg := findLLMContextMessage(request.Messages)
	if llmContextMsg == nil {
		t.Fatal("Should have a user message with llm_context")
	}

	if llmContextMsg.Role != "user" {
		t.Errorf("llm_context message should be user role, got: %s", llmContextMsg.Role)
	}

	if !strings.Contains(llmContextMsg.Content, "<agent:llm_context>") {
		t.Error("llm_context message should use <agent:llm_context> tag")
	}

	if !strings.Contains(llmContextMsg.Content, "Implement X") {
		t.Error("llm_context message should contain LLMContext content")
	}

	// And llm_context is inserted BEFORE the last user message
	// In this case, we have: llm_context -> runtime_state -> "do task"
	// So the last user message is "do task"
	lastUserIndex := -1
	for i := len(request.Messages) - 1; i >= 0; i-- {
		if request.Messages[i].Role == "user" && !isMetaMessage(request.Messages[i]) {
			lastUserIndex = i
			break
		}
	}

	if lastUserIndex == -1 {
		t.Fatal("Should have at least one non-meta user message")
	}

	// Check that llm_context comes before last user message
	llmContextFound := false
	for i := 0; i < lastUserIndex; i++ {
		if request.Messages[i].Role == "user" &&
		   strings.Contains(request.Messages[i].Content, "<agent:llm_context>") {
			llmContextFound = true
			break
		}
	}

	if !llmContextFound {
		t.Error("llm_context should be inserted before last user message")
	}
}

// TestBuildLLMRequest_RuntimeStateInjected tests that runtime_state
// is always injected before the last user message.
func TestBuildLLMRequest_RuntimeStateInjected(t *testing.T) {
	// Given: A snapshot
	snapshot := &context.ContextSnapshot{
		LLMContext: "Current task",
		RecentMessages: []context.AgentMessage{
			context.NewUserMessage("message 1"),
			context.NewAssistantMessage(),
			context.NewUserMessage("message 2"),
		},
		AgentState: *context.NewAgentState("test-session", "/test/dir"),
	}
	snapshot.AgentState.TokensUsed = 15000
	snapshot.AgentState.TokensLimit = 100000
	snapshot.AgentState.TotalTurns = 25

	// When: Building LLM request
	request, err := BuildRequest(snapshot, context.ModeNormal, nil, "test-model")
	if err != nil {
		t.Fatalf("BuildRequest failed: %v", err)
	}

	// Then: runtime_state should be present in user messages
	runtimeStateFound := false
	for _, msg := range request.Messages {
		if msg.Role == "user" && strings.Contains(msg.Content, "<agent:runtime_state>") {
			runtimeStateFound = true

			// Verify key fields are present
			if !strings.Contains(msg.Content, "tokens_used:") {
				t.Error("runtime_state should contain tokens_used")
			}
			if !strings.Contains(msg.Content, "tokens_limit:") {
				t.Error("runtime_state should contain tokens_limit")
			}
			if !strings.Contains(msg.Content, "turn:") {
				t.Error("runtime_state should contain turn")
			}
			break
		}
	}

	if !runtimeStateFound {
		t.Error("Should have runtime_state in user messages")
	}
}

// TestBuildLLMRequest_ContextMgmtModeNoLLMContextInUserMsg tests that
// in Context Management mode, LLMContext is not injected as a user message
// (it's built into the input instead).
func TestBuildLLMRequest_ContextMgmtModeNoLLMContextInUserMsg(t *testing.T) {
	// Given: A snapshot with LLMContext
	snapshot := &context.ContextSnapshot{
		LLMContext: "Current task summary",
		RecentMessages: []context.AgentMessage{
			context.NewUserMessage("message"),
		},
		AgentState: *context.NewAgentState("test-session", "/test/dir"),
	}

	// When: Building LLM request in Context Management mode
	request, err := BuildRequest(snapshot, context.ModeContextMgmt, nil, "test-model")
	if err != nil {
		t.Fatalf("BuildRequest failed: %v", err)
	}

	// Then: Should NOT have <agent:llm_context> user message
	for _, msg := range request.Messages {
		if msg.Role == "user" && strings.Contains(msg.Content, "<agent:llm_context>") {
			t.Error("ContextMgmt mode should not inject <agent:llm_context> as user message")
		}
	}

	// But system prompt should be different (context management prompt)
	if !strings.Contains(request.SystemPrompt, "CONTEXT MANAGEMENT") &&
	   !strings.Contains(request.SystemPrompt, "context_management") {
		t.Error("ContextMgmt mode should use context management system prompt")
	}
}

// Helper function to check if a message is a meta message (llm_context, runtime_state, etc.)
func isMetaMessage(msg LLMMessage) bool {
	content := msg.Content
	return strings.Contains(content, "<agent:llm_context>") ||
		strings.Contains(content, "<agent:runtime_state>") ||
		strings.Contains(content, "<agent:current_state>")
}

// Helper function to find llm_context message
func findLLMContextMessage(messages []LLMMessage) *LLMMessage {
	for i := range messages {
		if messages[i].Role == "user" && strings.Contains(messages[i].Content, "<agent:llm_context>") {
			return &messages[i]
		}
	}
	return nil
}
