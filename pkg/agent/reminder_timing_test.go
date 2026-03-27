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

// TestReminderTiming_MalformedToolCallRecoveryAllowsReminders verifies that
// after a malformed tool call recovery injects a user message, reminders
// ARE allowed on the subsequent turn.
func TestReminderTiming_MalformedToolCallRecoveryAllowsReminders(t *testing.T) {
	// This test verifies the fix for the edge case where:
	// 1. Assistant has no tool calls (hasMoreToolCalls = false)
	// 2. maybeRecoverMalformedToolCall returns true (injects recovery message)
	// 3. Loop continues with previousHadToolCalls = true
	// 4. AllowReminders should be true for the recovery turn

	agentCtx := &agentctx.AgentContext{
		SystemPrompt:      "test",
		Messages:          []agentctx.AgentMessage{{Role: "user", Content: []agentctx.ContentBlock{agentctx.TextContent{Text: "hello"}}}},
		AllowReminders:    true, // Simulate: recovery turn (previousHadToolCalls = true after fix)
		TaskTrackingState: agentctx.NewTaskTrackingState(t.TempDir()),
	}

	// Advance the task tracking state to make it need a reminder
	for i := 0; i < 12; i++ {
		agentCtx.TaskTrackingState.NeedsReminderMessage()
	}

	var reminderInjected bool
	origFn := streamAssistantResponseFn
	defer func() { streamAssistantResponseFn = origFn }()

	streamAssistantResponseFn = func(
		ctx context.Context,
		agentCtx *agentctx.AgentContext,
		config *LoopConfig,
		stream *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
	) (*agentctx.AgentMessage, error) {
		selectedMessages, _ := selectMessagesForLLM(agentCtx)
		llmMessages := ConvertMessagesToLLM(ctx, selectedMessages)

		if agentCtx.TaskTrackingState != nil && agentCtx.TaskTrackingState.NeedsReminderMessage() && config.TaskTrackingEnabled && agentCtx.AllowReminders {
			reminderMsg := llm.LLMMessage{
				Role:    "user",
				Content: "REMINDER",
			}
			llmMessages = append(llmMessages, reminderMsg)
			reminderInjected = true
		}

		return &agentctx.AgentMessage{
			Role:       "assistant",
			Content:    []agentctx.ContentBlock{agentctx.TextContent{Text: "recovered"}},
			StopReason: "end_turn",
		}, nil
	}

	config := &LoopConfig{
		TaskTrackingEnabled:      true,
		ContextManagementEnabled: true,
	}

	stream := llm.NewEventStream[AgentEvent, []agentctx.AgentMessage](
		func(e AgentEvent) bool { return e.Type == EventAgentEnd },
		func(e AgentEvent) []agentctx.AgentMessage { return e.Messages },
	)

	_, err := streamAssistantResponseWithRetry(context.Background(), agentCtx, config, stream)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Verify: Reminder SHOULD be injected on recovery turn
	if !reminderInjected {
		t.Error("Reminder was NOT injected on malformed tool call recovery turn - this indicates the fix is missing")
	}
}

// TestReminderTiming_ContextManagementReminderRespectsFlag verifies that
// context_management reminders also respect the AllowReminders flag.
func TestReminderTiming_ContextManagementReminderRespectsFlag(t *testing.T) {
	// Test with AllowReminders = false - context management reminder should NOT be injected
	agentCtx := &agentctx.AgentContext{
		SystemPrompt:      "test",
		Messages:          []agentctx.AgentMessage{{Role: "user", Content: []agentctx.ContentBlock{agentctx.TextContent{Text: "hello"}}}},
		AllowReminders:    false,
		LLMContext:        agentctx.NewLLMContext(t.TempDir()),
		ContextMgmtState:  agentctx.DefaultContextMgmtState(),
		TaskTrackingState: agentctx.NewTaskTrackingState(t.TempDir()),
	}

	// Set up context management state to trigger a reminder
	agentCtx.ContextMgmtState.SetCurrentTurn(15) // High turn count

	var contextReminderInjected bool
	origFn := streamAssistantResponseFn
	defer func() { streamAssistantResponseFn = origFn }()

	streamAssistantResponseFn = func(
		ctx context.Context,
		agentCtx *agentctx.AgentContext,
		config *LoopConfig,
		stream *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
	) (*agentctx.AgentMessage, error) {
		_, _ = selectMessagesForLLM(agentCtx) // Used for side effects

		// Check if context management reminder would be injected
		if agentCtx.LLMContext != nil && agentCtx.ContextMgmtState != nil && config.ContextManagementEnabled && agentCtx.AllowReminders {
			contextReminderInjected = true
		}

		return &agentctx.AgentMessage{
			Role:       "assistant",
			Content:    []agentctx.ContentBlock{agentctx.TextContent{Text: "done"}},
			StopReason: "end_turn",
		}, nil
	}

	config := &LoopConfig{
		TaskTrackingEnabled:      true,
		ContextManagementEnabled: true,
	}

	stream := llm.NewEventStream[AgentEvent, []agentctx.AgentMessage](
		func(e AgentEvent) bool { return e.Type == EventAgentEnd },
		func(e AgentEvent) []agentctx.AgentMessage { return e.Messages },
	)

	_, err := streamAssistantResponseWithRetry(context.Background(), agentCtx, config, stream)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Verify: Context management reminder should NOT be injected when AllowReminders is false
	if contextReminderInjected {
		t.Error("Context management reminder was injected when AllowReminders was false")
	}
}