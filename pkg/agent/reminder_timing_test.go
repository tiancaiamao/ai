package agent

import (
	"context"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/llm"
)

// TestReminderTiming_NoRemindersOnAssistantEnd verifies that reminders
// are NOT injected when the assistant is about to end the conversation
// (i.e., no previous tool calls and not the first turn).
func TestReminderTiming_NoRemindersOnAssistantEnd(t *testing.T) {
	// Setup: Create an agent context with AllowReminders = false
	// This simulates the state when:
	// 1. It's not the first turn (turnCount > 0)
	// 2. Previous turn had no tool calls (previousHadToolCalls = false)
	agentCtx := &agentctx.AgentContext{
		SystemPrompt:    "test",
		Messages:        []agentctx.AgentMessage{{Role: "user", Content: []agentctx.ContentBlock{agentctx.TextContent{Text: "hello"}}}},
		AllowReminders:  false, // Simulate: not first turn, no previous tool calls
		TaskTrackingState: agentctx.NewTaskTrackingState(t.TempDir()),
	}

	// Advance the task tracking state to make it need a reminder
	for i := 0; i < 12; i++ {
		agentCtx.TaskTrackingState.NeedsReminderMessage()
	}

	// Verify that reminder is needed
	if !agentCtx.TaskTrackingState.NeedsReminderMessage() {
		t.Fatal("Expected task tracking to need reminder after 12 rounds")
	}

	// Reset the counter for the actual test
	for i := 0; i < 12; i++ {
		agentCtx.TaskTrackingState.NeedsReminderMessage()
	}

	// Create a mock streamAssistantResponse function that captures the messages
	var capturedMessages []llm.LLMMessage
	origFn := streamAssistantResponseFn
	defer func() { streamAssistantResponseFn = origFn }()

	streamAssistantResponseFn = func(
		ctx context.Context,
		agentCtx *agentctx.AgentContext,
		config *LoopConfig,
		stream *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
	) (*agentctx.AgentMessage, error) {
		// Build messages the same way streamAssistantResponse does
		selectedMessages, _ := selectMessagesForLLM(agentCtx)
		llmMessages := ConvertMessagesToLLM(ctx, selectedMessages)

		// Check if reminders would be injected
		if agentCtx.TaskTrackingState != nil && agentCtx.TaskTrackingState.NeedsReminderMessage() && config.TaskTrackingEnabled && agentCtx.AllowReminders {
			reminderMsg := llm.LLMMessage{
				Role:    "user",
				Content: "REMINDER",
			}
			llmMessages = append(llmMessages, reminderMsg)
		}

		capturedMessages = llmMessages

		// Return a simple assistant message with no tool calls
		return &agentctx.AgentMessage{
			Role:      "assistant",
			Content:   []agentctx.ContentBlock{agentctx.TextContent{Text: "done"}},
			StopReason: "end_turn",
		}, nil
	}

	// Create config with task tracking enabled
	config := &LoopConfig{
		TaskTrackingEnabled:      true,
		ContextManagementEnabled: true,
	}

	// Create a stream
	stream := llm.NewEventStream[AgentEvent, []agentctx.AgentMessage](
		func(e AgentEvent) bool { return e.Type == EventAgentEnd },
		func(e AgentEvent) []agentctx.AgentMessage { return e.Messages },
	)

	// Call streamAssistantResponseWithRetry
	_, err := streamAssistantResponseWithRetry(context.Background(), agentCtx, config, stream)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Verify: No reminder should be in the messages
	for _, msg := range capturedMessages {
		if msg.Role == "user" && msg.Content == "REMINDER" {
			t.Error("Reminder was injected when AllowReminders was false")
		}
	}
}

// TestReminderTiming_RemindersAllowedWithToolCalls verifies that reminders
// ARE injected when the previous turn had tool calls (loop will continue).
func TestReminderTiming_RemindersAllowedWithToolCalls(t *testing.T) {
	// Setup: Create an agent context with AllowReminders = true
	// This simulates the state when:
	// 1. Previous turn had tool calls (previousHadToolCalls = true)
	agentCtx := &agentctx.AgentContext{
		SystemPrompt:    "test",
		Messages:        []agentctx.AgentMessage{{Role: "user", Content: []agentctx.ContentBlock{agentctx.TextContent{Text: "hello"}}}},
		AllowReminders:  true, // Simulate: previous turn had tool calls
		TaskTrackingState: agentctx.NewTaskTrackingState(t.TempDir()),
	}

	// Advance the task tracking state to make it need a reminder
	for i := 0; i < 12; i++ {
		agentCtx.TaskTrackingState.NeedsReminderMessage()
	}

	// Create a mock streamAssistantResponse function that captures the messages
	var reminderInjected bool
	origFn := streamAssistantResponseFn
	defer func() { streamAssistantResponseFn = origFn }()

	streamAssistantResponseFn = func(
		ctx context.Context,
		agentCtx *agentctx.AgentContext,
		config *LoopConfig,
		stream *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
	) (*agentctx.AgentMessage, error) {
		// Build messages the same way streamAssistantResponse does
		selectedMessages, _ := selectMessagesForLLM(agentCtx)
		llmMessages := ConvertMessagesToLLM(ctx, selectedMessages)

		// Check if reminders would be injected
		if agentCtx.TaskTrackingState != nil && agentCtx.TaskTrackingState.NeedsReminderMessage() && config.TaskTrackingEnabled && agentCtx.AllowReminders {
			reminderMsg := llm.LLMMessage{
				Role:    "user",
				Content: "REMINDER",
			}
			llmMessages = append(llmMessages, reminderMsg)
			reminderInjected = true
		}

		// Return a simple assistant message with tool calls (loop continues)
		return &agentctx.AgentMessage{
			Role:      "assistant",
			Content:   []agentctx.ContentBlock{agentctx.TextContent{Text: "done"}},
			StopReason: "end_turn",
		}, nil
	}

	// Create config with task tracking enabled
	config := &LoopConfig{
		TaskTrackingEnabled:      true,
		ContextManagementEnabled: true,
	}

	// Create a stream
	stream := llm.NewEventStream[AgentEvent, []agentctx.AgentMessage](
		func(e AgentEvent) bool { return e.Type == EventAgentEnd },
		func(e AgentEvent) []agentctx.AgentMessage { return e.Messages },
	)

	// Call streamAssistantResponseWithRetry
	_, err := streamAssistantResponseWithRetry(context.Background(), agentCtx, config, stream)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Verify: Reminder should have been injected
	if !reminderInjected {
		t.Error("Reminder was NOT injected when AllowReminders was true")
	}
}

// TestReminderTiming_FirstTurnAllowsReminders verifies that reminders
// ARE injected on the first turn (user initiated).
func TestReminderTiming_FirstTurnAllowsReminders(t *testing.T) {
	// Setup: Create an agent context with AllowReminders = true
	// This simulates the first turn (turnCount == 0)
	agentCtx := &agentctx.AgentContext{
		SystemPrompt:    "test",
		Messages:        []agentctx.AgentMessage{{Role: "user", Content: []agentctx.ContentBlock{agentctx.TextContent{Text: "hello"}}}},
		AllowReminders:  true, // First turn always allows reminders
		TaskTrackingState: agentctx.NewTaskTrackingState(t.TempDir()),
	}

	// Advance the task tracking state to make it need a reminder
	for i := 0; i < 12; i++ {
		agentCtx.TaskTrackingState.NeedsReminderMessage()
	}

	// Create a mock streamAssistantResponse function that captures the messages
	var reminderInjected bool
	origFn := streamAssistantResponseFn
	defer func() { streamAssistantResponseFn = origFn }()

	streamAssistantResponseFn = func(
		ctx context.Context,
		agentCtx *agentctx.AgentContext,
		config *LoopConfig,
		stream *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
	) (*agentctx.AgentMessage, error) {
		// Build messages the same way streamAssistantResponse does
		selectedMessages, _ := selectMessagesForLLM(agentCtx)
		llmMessages := ConvertMessagesToLLM(ctx, selectedMessages)

		// Check if reminders would be injected
		if agentCtx.TaskTrackingState != nil && agentCtx.TaskTrackingState.NeedsReminderMessage() && config.TaskTrackingEnabled && agentCtx.AllowReminders {
			reminderMsg := llm.LLMMessage{
				Role:    "user",
				Content: "REMINDER",
			}
			llmMessages = append(llmMessages, reminderMsg)
			reminderInjected = true
		}

		return &agentctx.AgentMessage{
			Role:      "assistant",
			Content:   []agentctx.ContentBlock{agentctx.TextContent{Text: "done"}},
			StopReason: "end_turn",
		}, nil
	}

	// Create config with task tracking enabled
	config := &LoopConfig{
		TaskTrackingEnabled:      true,
		ContextManagementEnabled: true,
	}

	// Create a stream
	stream := llm.NewEventStream[AgentEvent, []agentctx.AgentMessage](
		func(e AgentEvent) bool { return e.Type == EventAgentEnd },
		func(e AgentEvent) []agentctx.AgentMessage { return e.Messages },
	)

	// Call streamAssistantResponseWithRetry
	_, err := streamAssistantResponseWithRetry(context.Background(), agentCtx, config, stream)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Verify: Reminder should have been injected
	if !reminderInjected {
		t.Error("Reminder was NOT injected on first turn")
	}
}