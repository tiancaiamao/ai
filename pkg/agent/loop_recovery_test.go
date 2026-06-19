package agent

import (
	"context"
	"errors"
	"fmt"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"strings"
	"testing"

	"github.com/tiancaiamao/ai/pkg/llm"
)

type recoveryCompactor struct {
	calls         int
	shouldCompact bool
}

func (c *recoveryCompactor) ShouldCompact(_ context.Context, _ *agentctx.AgentContext) bool {
	return c.shouldCompact
}

func (c *recoveryCompactor) Compact(_ context.Context, ctx *agentctx.AgentContext) (*agentctx.CompactionResult, error) {
	c.calls++
	// Compactor directly modifies ctx.RecentMessages
	ctx.RecentMessages = []agentctx.AgentMessage{
		agentctx.NewUserMessage("[summary]"),
		agentctx.NewUserMessage("latest request"),
	}
	return &agentctx.CompactionResult{
		Summary: "[summary]",
	}, nil
}

func (c *recoveryCompactor) CalculateDynamicThreshold() int {
	return 100000 // Default threshold for tests
}

func newTestAgentEventStream() *llm.EventStream[AgentEvent, []agentctx.AgentMessage] {
	return llm.NewEventStream[AgentEvent, []agentctx.AgentMessage](
		func(e AgentEvent) bool { return e.Type == EventAgentEnd },
		func(e AgentEvent) []agentctx.AgentMessage { return e.Messages },
	)
}

func TestRunInnerLoopCompactionRecoveryOnContextLengthError(t *testing.T) {
	orig := streamAssistantResponseFn
	defer func() { streamAssistantResponseFn = orig }()

	compactor := &recoveryCompactor{}
	callCount := 0
	streamAssistantResponseFn = func(
		_ context.Context,
		_ *agentctx.AgentContext,
		_ *LoopConfig,
		_ *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
	) (*agentctx.AgentMessage, error) {
		callCount++
		if callCount == 1 {
			return nil, &llm.ContextLengthExceededError{Message: "maximum context length exceeded"}
		}

		msg := agentctx.NewAssistantMessage()
		msg.Content = []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "done"},
		}
		msg.StopReason = "stop"
		return &msg, nil
	}

	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.RecentMessages = append(agentCtx.RecentMessages, agentctx.NewUserMessage("hello"))

	config := &LoopConfig{
		Compactors: []Compactor{compactor},
	}

	stream := newTestAgentEventStream()
	runInnerLoop(context.Background(), agentCtx, nil, config, stream)

	var gotStart, gotEnd bool
	for item := range stream.Iterator(context.Background()) {
		if item.Done {
			break
		}
		if item.Value.Type == EventCompactionStart {
			gotStart = true
		}
		if item.Value.Type == EventCompactionEnd {
			gotEnd = true
		}
	}

	if compactor.calls != 1 {
		t.Fatalf("expected compactor to be called once, got %d", compactor.calls)
	}
	if callCount != 2 {
		t.Fatalf("expected assistant streaming to be called twice, got %d", callCount)
	}
	if !gotStart || !gotEnd {
		t.Fatalf("expected compaction start/end events, got start=%v end=%v", gotStart, gotEnd)
	}
}

func TestRunInnerLoopPreLLMCompactionTrigger(t *testing.T) {
	orig := streamAssistantResponseFn
	defer func() { streamAssistantResponseFn = orig }()

	compactor := &recoveryCompactor{shouldCompact: true}
	callCount := 0
	streamAssistantResponseFn = func(
		_ context.Context,
		_ *agentctx.AgentContext,
		_ *LoopConfig,
		_ *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
	) (*agentctx.AgentMessage, error) {
		callCount++
		msg := agentctx.NewAssistantMessage()
		msg.Content = []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "done"}}
		msg.StopReason = "stop"
		return &msg, nil
	}

	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.RecentMessages = append(agentCtx.RecentMessages, agentctx.NewUserMessage("hello"))
	stream := newTestAgentEventStream()
	runInnerLoop(context.Background(), agentCtx, nil, &LoopConfig{Compactors: []Compactor{compactor}}, stream)

	var sawStart, sawEnd bool
	for item := range stream.Iterator(context.Background()) {
		if item.Done {
			break
		}
		if item.Value.Type == EventCompactionStart && item.Value.Compaction != nil && item.Value.Compaction.Trigger == "pre_llm_threshold" {
			sawStart = true
		}
		if item.Value.Type == EventCompactionEnd && item.Value.Compaction != nil && item.Value.Compaction.Trigger == "pre_llm_threshold" {
			sawEnd = true
		}
	}

	if compactor.calls != 1 {
		t.Fatalf("expected compactor to be called once, got %d", compactor.calls)
	}
	if callCount != 1 {
		t.Fatalf("expected assistant streaming to be called once, got %d", callCount)
	}
	if !sawStart || !sawEnd {
		t.Fatalf("expected pre-LLM compaction start/end events, got start=%v end=%v", sawStart, sawEnd)
	}
}

func TestRunInnerLoopStopsRepeatedToolCalls(t *testing.T) {
	orig := streamAssistantResponseFn
	defer func() { streamAssistantResponseFn = orig }()

	callCount := 0
	streamAssistantResponseFn = func(
		_ context.Context,
		_ *agentctx.AgentContext,
		_ *LoopConfig,
		_ *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
	) (*agentctx.AgentMessage, error) {
		callCount++
		msg := agentctx.NewAssistantMessage()
		msg.Content = []agentctx.ContentBlock{
			agentctx.ToolCallContent{
				ID:        "call-repeat",
				Type:      "toolCall",
				Name:      "read",
				Arguments: map[string]any{"path": "/tmp/a.txt"},
			},
		}
		msg.StopReason = "tool_calls"
		return &msg, nil
	}

	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.RecentMessages = append(agentCtx.RecentMessages, agentctx.NewUserMessage("start"))
	stream := newTestAgentEventStream()
	runInnerLoop(context.Background(), agentCtx, nil, &LoopConfig{
		MaxConsecutiveToolCalls: 2,
		MaxToolCallsPerName:     100,
	}, stream)

	// With the feedback-to-LLM strategy + abort recovery:
	//   call 1-2: normal execution
	//   call 3: guard triggers → feedback #1 → LLM retries
	//   call 4: guard triggers → feedback #2 → LLM retries
	//   call 5: guard triggers → hard abort → recovery turn
	//   call 6: LLM retries same call → hard abort again (no more recovery) → terminate
	if callCount != 6 {
		t.Fatalf("expected loop guard to hard-abort after 6 calls (2 normal + 2 feedback + 2 abort with recovery), got %d calls", callCount)
	}

	var sawGuardedTurn bool
	var sawGuardEvent bool
	var guardEventCount int
	for item := range stream.Iterator(context.Background()) {
		if item.Done {
			break
		}
		if item.Value.Type == EventLoopGuardTriggered && item.Value.LoopGuard != nil && strings.TrimSpace(item.Value.LoopGuard.Reason) != "" {
			sawGuardEvent = true
			guardEventCount++
		}
		if item.Value.Type != EventTurnEnd || item.Value.Message == nil {
			continue
		}
		msg := item.Value.Message
		if msg.StopReason == "aborted" && strings.Contains(msg.ExtractText(), "[Loop guard]") && len(msg.ExtractToolCalls()) == 0 {
			sawGuardedTurn = true
		}
	}

	if !sawGuardedTurn {
		t.Fatal("expected guarded turn_end message without tool calls")
	}
	if !sawGuardEvent {
		t.Fatal("expected loop_guard_triggered event")
	}
	// Guard should trigger 4 times: 2 feedback + 2 hard abort (with recovery)
	if guardEventCount != 4 {
		t.Fatalf("expected 4 loop guard events (2 feedback + 2 hard abort), got %d", guardEventCount)
	}
}

func TestRunInnerLoopStopsRepeatedToolCallsByDefaultGuard(t *testing.T) {
	orig := streamAssistantResponseFn
	defer func() { streamAssistantResponseFn = orig }()

	callCount := 0
	streamAssistantResponseFn = func(
		_ context.Context,
		_ *agentctx.AgentContext,
		_ *LoopConfig,
		_ *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
	) (*agentctx.AgentMessage, error) {
		callCount++
		msg := agentctx.NewAssistantMessage()
		msg.Content = []agentctx.ContentBlock{
			agentctx.ToolCallContent{
				ID:        "call-repeat-default",
				Type:      "toolCall",
				Name:      "read",
				Arguments: map[string]any{"path": "/tmp/a.txt"},
			},
		}
		msg.StopReason = "tool_calls"
		return &msg, nil
	}

	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.RecentMessages = append(agentCtx.RecentMessages, agentctx.NewUserMessage("start"))
	stream := newTestAgentEventStream()
	runInnerLoop(context.Background(), agentCtx, nil, &LoopConfig{}, stream)

	// defaultLoopMaxConsecutiveToolCalls = 6, so guard triggers on the 7th call.
	// With feedback-to-LLM strategy (defaultLoopGuardMaxFeedback = 2) + abort recovery:
	//   call 1-6: normal execution
	//   call 7: guard triggers → feedback #1 → LLM retries
	//   call 8: guard triggers → feedback #2 → LLM retries
	//   call 9: guard triggers → hard abort → recovery turn
	//   call 10: LLM retries → hard abort again (no more recovery) → terminate
	if callCount != 10 {
		t.Fatalf("expected default loop guard to hard-abort after 10 calls (6 normal + 2 feedback + 2 abort with recovery), got %d calls", callCount)
	}
}

func TestRunInnerLoopPersistsAssistantMessagesInContext(t *testing.T) {
	orig := streamAssistantResponseFn
	defer func() { streamAssistantResponseFn = orig }()

	callCount := 0
	streamAssistantResponseFn = func(
		_ context.Context,
		_ *agentctx.AgentContext,
		_ *LoopConfig,
		_ *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
	) (*agentctx.AgentMessage, error) {
		callCount++
		if callCount == 1 {
			msg := agentctx.NewAssistantMessage()
			msg.Content = []agentctx.ContentBlock{
				agentctx.ToolCallContent{
					ID:        "call-1",
					Type:      "toolCall",
					Name:      "read",
					Arguments: map[string]any{"path": "/tmp/a.txt"},
				},
			}
			msg.StopReason = "tool_calls"
			return &msg, nil
		}

		msg := agentctx.NewAssistantMessage()
		msg.Content = []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "done"},
		}
		msg.StopReason = "stop"
		return &msg, nil
	}

	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.RecentMessages = append(agentCtx.RecentMessages, agentctx.NewUserMessage("start"))
	stream := newTestAgentEventStream()
	runInnerLoop(context.Background(), agentCtx, nil, &LoopConfig{}, stream)

	if callCount != 2 {
		t.Fatalf("expected two assistant turns, got %d", callCount)
	}

	var sawToolCallAssistant bool
	var sawFinalAssistant bool
	for _, msg := range agentCtx.RecentMessages {
		if msg.Role != "assistant" {
			continue
		}
		if len(msg.ExtractToolCalls()) > 0 {
			sawToolCallAssistant = true
		}
		if strings.Contains(msg.ExtractText(), "done") {
			sawFinalAssistant = true
		}
	}

	if !sawToolCallAssistant {
		t.Fatal("expected assistant tool-call message to be persisted in context")
	}
	if !sawFinalAssistant {
		t.Fatal("expected final assistant message to be persisted in context")
	}
}

func TestStreamAssistantResponseWithRetrySkipsRetryForContextLengthError(t *testing.T) {
	orig := streamAssistantResponseFn
	defer func() { streamAssistantResponseFn = orig }()

	callCount := 0
	streamAssistantResponseFn = func(
		_ context.Context,
		_ *agentctx.AgentContext,
		_ *LoopConfig,
		_ *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
	) (*agentctx.AgentMessage, error) {
		callCount++
		return nil, &llm.ContextLengthExceededError{Message: "context window exceeded"}
	}

	stream := newTestAgentEventStream()
	_, err := streamAssistantResponseWithRetry(
		context.Background(),
		agentctx.NewAgentContext("sys"),
		&LoopConfig{MaxLLMRetries: 3},
		stream,
	)
	if err == nil {
		t.Fatal("expected error")
	}
	if !llm.IsContextLengthExceeded(err) {
		t.Fatalf("expected context-length error, got %v", err)
	}
	if callCount != 1 {
		t.Fatalf("expected no retries for context-length error, got %d calls", callCount)
	}
}

func TestStreamAssistantResponseWithRetryPrefersLastLLMErrorOverContextCancel(t *testing.T) {
	orig := streamAssistantResponseFn
	defer func() { streamAssistantResponseFn = orig }()

	ctx, cancel := context.WithCancel(context.Background())
	callCount := 0
	streamAssistantResponseFn = func(
		_ context.Context,
		_ *agentctx.AgentContext,
		_ *LoopConfig,
		_ *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
	) (*agentctx.AgentMessage, error) {
		callCount++
		cancel()
		return nil, errors.New("API error (429): Rate limit reached for requests")
	}

	stream := newTestAgentEventStream()
	_, err := streamAssistantResponseWithRetry(
		ctx,
		agentctx.NewAgentContext("sys"),
		&LoopConfig{MaxLLMRetries: 3},
		stream,
	)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "Rate limit reached") {
		t.Fatalf("expected original 429 error, got %v", err)
	}
	if strings.TrimSpace(ErrorStack(err)) == "" {
		t.Fatalf("expected wrapped error stack, got empty stack for: %v", err)
	}
	if callCount != 1 {
		t.Fatalf("expected one call before cancellation, got %d", callCount)
	}
}

func TestStreamAssistantResponseWithRetrySkipsRetryForNonRetryable4xx(t *testing.T) {
	orig := streamAssistantResponseFn
	defer func() { streamAssistantResponseFn = orig }()

	callCount := 0
	streamAssistantResponseFn = func(
		_ context.Context,
		_ *agentctx.AgentContext,
		_ *LoopConfig,
		_ *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
	) (*agentctx.AgentMessage, error) {
		callCount++
		return nil, &llm.APIError{StatusCode: 401, Message: "unauthorized"}
	}

	stream := newTestAgentEventStream()
	_, err := streamAssistantResponseWithRetry(
		context.Background(),
		agentctx.NewAgentContext("sys"),
		&LoopConfig{MaxLLMRetries: 3},
		stream,
	)
	if err == nil {
		t.Fatal("expected error")
	}
	if callCount != 1 {
		t.Fatalf("expected no retries for 4xx non-rate-limit errors, got %d calls", callCount)
	}
}

func TestRunInnerLoopCompactionRecoveryFailureFallsBackToError(t *testing.T) {
	orig := streamAssistantResponseFn
	defer func() { streamAssistantResponseFn = orig }()

	streamAssistantResponseFn = func(
		_ context.Context,
		_ *agentctx.AgentContext,
		_ *LoopConfig,
		_ *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
	) (*agentctx.AgentMessage, error) {
		return nil, &llm.ContextLengthExceededError{Message: "context window exceeded"}
	}

	brokenCompactor := &failingCompactor{}
	stream := newTestAgentEventStream()
	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.RecentMessages = append(agentCtx.RecentMessages, agentctx.NewUserMessage("hello"))

	runInnerLoop(context.Background(), agentCtx, nil, &LoopConfig{Compactors: []Compactor{brokenCompactor}}, stream)

	var sawAgentEnd bool
	for item := range stream.Iterator(context.Background()) {
		if item.Done {
			break
		}
		if item.Value.Type == EventAgentEnd {
			sawAgentEnd = true
		}
	}

	if !sawAgentEnd {
		t.Fatal("expected agent end event on recovery failure")
	}
}

func TestRunInnerLoopEmitsErrorEventOnStreamingFailure(t *testing.T) {
	orig := streamAssistantResponseFn
	defer func() { streamAssistantResponseFn = orig }()

	streamAssistantResponseFn = func(
		_ context.Context,
		_ *agentctx.AgentContext,
		_ *LoopConfig,
		_ *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
	) (*agentctx.AgentMessage, error) {
		// Use 500 error to avoid rate limit retry logic (which has exponential backoff)
		return nil, &llm.APIError{StatusCode: 500, Message: "internal server error"}
	}

	stream := newTestAgentEventStream()
	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.RecentMessages = append(agentCtx.RecentMessages, agentctx.NewUserMessage("hello"))

	runInnerLoop(context.Background(), agentCtx, nil, &LoopConfig{}, stream)

	var sawErrorEvent bool
	var sawErrorStack bool
	var sawAgentEnd bool
	for item := range stream.Iterator(context.Background()) {
		if item.Done {
			break
		}
		if item.Value.Type == EventError && strings.Contains(item.Value.Error, "internal server error") {
			sawErrorEvent = true
			if strings.TrimSpace(item.Value.ErrorStack) != "" {
				sawErrorStack = true
			}
		}
		if item.Value.Type == EventAgentEnd {
			sawAgentEnd = true
		}
	}

	if !sawErrorEvent {
		t.Fatal("expected error event with streaming failure reason")
	}
	if !sawAgentEnd {
		t.Fatal("expected agent end event after streaming failure")
	}
	if !sawErrorStack {
		t.Fatal("expected error stack in streaming failure event")
	}
}

type failingCompactor struct{}

func (f *failingCompactor) ShouldCompact(_ context.Context, _ *agentctx.AgentContext) bool {
	return false
}

func (f *failingCompactor) Compact(_ context.Context, _ *agentctx.AgentContext) (*agentctx.CompactionResult, error) {
	return nil, errors.New("compaction failed")
}

func (f *failingCompactor) CalculateDynamicThreshold() int {
	return 100000 // Default threshold for tests
}

// TestRunInnerLoopMaxTurnsLimit tests that the loop stops when max turns is reached
func TestRunInnerLoopMaxTurnsLimit(t *testing.T) {
	orig := streamAssistantResponseFn
	defer func() { streamAssistantResponseFn = orig }()

	callCount := 0
	streamAssistantResponseFn = func(
		_ context.Context,
		_ *agentctx.AgentContext,
		_ *LoopConfig,
		_ *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
	) (*agentctx.AgentMessage, error) {
		callCount++
		msg := agentctx.NewAssistantMessage()
		// Always return a tool call to keep the loop going
		msg.Content = []agentctx.ContentBlock{
			agentctx.ToolCallContent{
				ID:        "tc-" + string(rune('0'+callCount)),
				Type:      "toolCall",
				Name:      "test-tool",
				Arguments: map[string]any{},
			},
		}
		msg.StopReason = "tool_calls"
		return &msg, nil
	}

	agentCtx := agentctx.NewAgentContext("sys")
	executor := NewToolExecutor(1, 10)

	config := &LoopConfig{
		Compactors: []Compactor{&recoveryCompactor{}},
		Executor:   executor,
		MaxTurns:   3, // Limit to 3 turns
	}

	stream := newTestAgentEventStream()
	runInnerLoop(context.Background(), agentCtx, nil, config, stream)

	// Should have exactly 3 LLM calls (one per turn)
	if callCount != 3 {
		t.Errorf("expected 3 LLM calls with MaxTurns=3, got %d", callCount)
	}
}

// TestRunInnerLoopMaxTurnsUnlimited tests that MaxTurns=0 means unlimited
func TestRunInnerLoopMaxTurnsUnlimited(t *testing.T) {
	orig := streamAssistantResponseFn
	defer func() { streamAssistantResponseFn = orig }()

	callCount := 0
	streamAssistantResponseFn = func(
		_ context.Context,
		_ *agentctx.AgentContext,
		_ *LoopConfig,
		_ *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
	) (*agentctx.AgentMessage, error) {
		callCount++
		msg := agentctx.NewAssistantMessage()
		if callCount >= 5 {
			// After 5 calls, stop with text response (no tool calls)
			msg.Content = []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: "done"},
			}
			msg.StopReason = "stop"
			return &msg, nil
		}
		// Return tool call to keep loop going
		msg.Content = []agentctx.ContentBlock{
			agentctx.ToolCallContent{
				ID:        "tc-" + string(rune('0'+callCount)),
				Type:      "toolCall",
				Name:      "test-tool",
				Arguments: map[string]any{},
			},
		}
		msg.StopReason = "tool_calls"
		return &msg, nil
	}

	agentCtx := agentctx.NewAgentContext("sys")
	executor := NewToolExecutor(1, 10)

	config := &LoopConfig{
		Compactors: []Compactor{&recoveryCompactor{}},
		Executor:   executor,
		MaxTurns:   0, // Unlimited
	}

	stream := newTestAgentEventStream()
	runInnerLoop(context.Background(), agentCtx, nil, config, stream)

	// With MaxTurns=0, should continue until LLM returns text without tool calls
	if callCount != 5 {
		t.Errorf("expected 5 LLM calls with MaxTurns=0 (unlimited), got %d", callCount)
	}
}

func TestRunInnerLoopRecoversMalformedToolCallResponse(t *testing.T) {
	orig := streamAssistantResponseFn
	defer func() { streamAssistantResponseFn = orig }()

	callCount := 0
	streamAssistantResponseFn = func(
		_ context.Context,
		_ *agentctx.AgentContext,
		_ *LoopConfig,
		_ *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
	) (*agentctx.AgentMessage, error) {
		callCount++
		msg := agentctx.NewAssistantMessage()
		if callCount == 1 {
			msg.Content = []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: "<tool_call><arg_key>path</arg_key><arg_value>file.txt</arg_value></tool_call>"},
			}
			msg.StopReason = "tool_calls"
			return &msg, nil
		}
		msg.Content = []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "done"}}
		msg.StopReason = "stop"
		return &msg, nil
	}

	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.RecentMessages = append(agentCtx.RecentMessages, agentctx.NewUserMessage("start"))
	stream := newTestAgentEventStream()
	runInnerLoop(context.Background(), agentCtx, nil, &LoopConfig{}, stream)

	if callCount != 2 {
		t.Fatalf("expected malformed tool-call recovery to trigger another LLM turn, got %d", callCount)
	}

	var sawRepairPrompt bool
	for _, msg := range agentCtx.RecentMessages {
		if msg.Role != "user" || msg.Metadata == nil || msg.Metadata.Kind != "tool_call_repair" {
			continue
		}
		if msg.IsUserVisible() {
			t.Fatal("expected tool_call_repair prompt to be hidden from user")
		}
		if !msg.IsAgentVisible() {
			t.Fatal("expected tool_call_repair prompt to be visible to agent")
		}
		sawRepairPrompt = true
	}
	if !sawRepairPrompt {
		t.Fatal("expected hidden tool_call_repair prompt in context")
	}
}

func TestRunInnerLoopMalformedToolCallRecoveryRespectsLimit(t *testing.T) {
	orig := streamAssistantResponseFn
	defer func() { streamAssistantResponseFn = orig }()

	callCount := 0
	streamAssistantResponseFn = func(
		_ context.Context,
		_ *agentctx.AgentContext,
		_ *LoopConfig,
		_ *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
	) (*agentctx.AgentMessage, error) {
		callCount++
		msg := agentctx.NewAssistantMessage()
		msg.Content = []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "<tool_call><arg_key>path</arg_key><arg_value>file.txt</arg_value></tool_call>"},
		}
		msg.StopReason = "tool_calls"
		return &msg, nil
	}

	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.RecentMessages = append(agentCtx.RecentMessages, agentctx.NewUserMessage("start"))
	stream := newTestAgentEventStream()
	runInnerLoop(context.Background(), agentCtx, nil, &LoopConfig{}, stream)

	if callCount != defaultMalformedToolCallRecoveries+1 {
		t.Fatalf("expected %d LLM calls (recoveries + final stop), got %d", defaultMalformedToolCallRecoveries+1, callCount)
	}
}

func TestRunInnerLoopRecoversWhenToolCallOnlyInThinking(t *testing.T) {
	orig := streamAssistantResponseFn
	defer func() { streamAssistantResponseFn = orig }()

	callCount := 0
	streamAssistantResponseFn = func(
		_ context.Context,
		_ *agentctx.AgentContext,
		_ *LoopConfig,
		_ *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
	) (*agentctx.AgentMessage, error) {
		callCount++
		msg := agentctx.NewAssistantMessage()
		if callCount == 1 {
			msg.Content = []agentctx.ContentBlock{
				agentctx.ThinkingContent{Type: "thinking", Thinking: "<tool_call><arg_key>command</arg_key><arg_value>pwd</arg_value></tool_call>"},
			}
			msg.StopReason = "tool_calls"
			return &msg, nil
		}
		msg.Content = []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "done"}}
		msg.StopReason = "stop"
		return &msg, nil
	}

	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.RecentMessages = append(agentCtx.RecentMessages, agentctx.NewUserMessage("start"))
	stream := newTestAgentEventStream()
	runInnerLoop(context.Background(), agentCtx, nil, &LoopConfig{}, stream)

	if callCount != 2 {
		t.Fatalf("expected loop to continue after thinking-only malformed tool call, got %d calls", callCount)
	}
}

func TestRunInnerLoopEmitsToolCallRecoveryEvent(t *testing.T) {
	orig := streamAssistantResponseFn
	defer func() { streamAssistantResponseFn = orig }()

	callCount := 0
	streamAssistantResponseFn = func(
		_ context.Context,
		_ *agentctx.AgentContext,
		_ *LoopConfig,
		_ *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
	) (*agentctx.AgentMessage, error) {
		callCount++
		msg := agentctx.NewAssistantMessage()
		if callCount == 1 {
			msg.Content = []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: "tool: bash\n<arg_key>command</arg_key><arg_value>make debug-asan</arg_value>"},
			}
			msg.StopReason = "tool_calls"
			return &msg, nil
		}
		msg.Content = []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "done"}}
		msg.StopReason = "stop"
		return &msg, nil
	}

	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.RecentMessages = append(agentCtx.RecentMessages, agentctx.NewUserMessage("start"))
	stream := newTestAgentEventStream()
	runInnerLoop(context.Background(), agentCtx, nil, &LoopConfig{}, stream)

	var sawRecovery bool
	for item := range stream.Iterator(context.Background()) {
		if item.Done {
			break
		}
		if item.Value.Type == EventToolCallRecovery && item.Value.ToolCallRecovery != nil {
			if item.Value.ToolCallRecovery.Attempt < 1 {
				t.Fatalf("expected recovery attempt > 0, got %d", item.Value.ToolCallRecovery.Attempt)
			}
			sawRecovery = true
		}
	}
	if !sawRecovery {
		t.Fatal("expected tool_call_recovery event")
	}
}

// TestRunInnerLoopTaskTrackingDoesNotTriggerLoopGuard verifies that task_tracking
// tool calls do not contribute to loop guard detection.
func TestRunInnerLoopTaskTrackingDoesNotTriggerLoopGuard(t *testing.T) {
	orig := streamAssistantResponseFn
	defer func() { streamAssistantResponseFn = orig }()

	callCount := 0
	// Simulate LLM calling task_tracking repeatedly, which should NOT trigger loop guard
	streamAssistantResponseFn = func(
		_ context.Context,
		_ *agentctx.AgentContext,
		_ *LoopConfig,
		_ *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
	) (*agentctx.AgentMessage, error) {
		callCount++
		msg := agentctx.NewAssistantMessage()

		// Call task_tracking repeatedly - this should never trigger loop guard
		// because we reset the counter after each execution
		if callCount <= 10 {
			msg.Content = []agentctx.ContentBlock{
				agentctx.ToolCallContent{
					ID:        "call-task-tracking-" + fmt.Sprint(callCount),
					Type:      "toolCall",
					Name:      "task_tracking",
					Arguments: map[string]any{"content": fmt.Sprintf("task %d", callCount)},
				},
			}
		} else {
			// After 10+ iterations, just return "done" to stop
			msg.Content = []agentctx.ContentBlock{
				agentctx.TextContent{Type: "text", Text: "done"},
			}
			msg.StopReason = "stop"
			return &msg, nil
		}
		msg.StopReason = "tool_calls"
		return &msg, nil
	}

	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.RecentMessages = append(agentCtx.RecentMessages, agentctx.NewUserMessage("start"))

	// Set a very low limit - without the fix, this would stop at 61 calls (default)
	// With the fix, task_tracking should be exempt and not trigger loop guard
	stream := newTestAgentEventStream()
	runInnerLoop(context.Background(), agentCtx, nil, &LoopConfig{
		MaxConsecutiveToolCalls: 10,
		MaxToolCallsPerName:     3, // Would stop any tool after 3 calls
	}, stream)

	// Should have more than 10 calls because task_tracking resets its counter each time
	// Without the fix, loop would stop at 61 calls (default) or earlier if MaxToolCallsPerName=3
	if callCount <= 10 {
		t.Fatalf("expected more than 10 LLM calls (task_tracking should be exempt), got %d", callCount)
	}

	var guardTriggeredForTaskTracking bool
	for item := range stream.Iterator(context.Background()) {
		if item.Done {
			break
		}
		if item.Value.Type == EventLoopGuardTriggered && item.Value.LoopGuard != nil {
			reason := item.Value.LoopGuard.Reason
			// Check if loop guard was triggered for task_tracking
			if strings.Contains(reason, "task_tracking") {
				guardTriggeredForTaskTracking = true
			}
		}
	}

	if guardTriggeredForTaskTracking {
		t.Fatal("task_tracking should NOT trigger loop guard")
	}
}

// TestLoopGuardFeedbackAllowsLLMSelfCorrection verifies that when the loop guard
// detects repeated tool calls, it returns a ToolResult (feedback) to the LLM
// instead of immediately aborting. The LLM can then self-correct by calling a
// different tool and completing successfully.
func TestLoopGuardFeedbackAllowsLLMSelfCorrection(t *testing.T) {
	orig := streamAssistantResponseFn
	defer func() { streamAssistantResponseFn = orig }()

	callCount := 0
	streamAssistantResponseFn = func(
		_ context.Context,
		_ *agentctx.AgentContext,
		_ *LoopConfig,
		_ *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
	) (*agentctx.AgentMessage, error) {
		callCount++
		msg := agentctx.NewAssistantMessage()

		if callCount <= 3 {
			// First 3 calls: repeat the same tool call (will trigger guard on call 3)
			msg.Content = []agentctx.ContentBlock{
				agentctx.ToolCallContent{
					ID:        "call-repeat-" + fmt.Sprint(callCount),
					Type:      "toolCall",
					Name:      "read",
					Arguments: map[string]any{"path": "/tmp/a.txt"},
				},
			}
			msg.StopReason = "tool_calls"
			return &msg, nil
		}

		// After receiving loop guard feedback, LLM self-corrects and returns text
		msg.Content = []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "I see the file doesn't exist. Let me try something else."},
		}
		msg.StopReason = "stop"
		return &msg, nil
	}

	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.RecentMessages = append(agentCtx.RecentMessages, agentctx.NewUserMessage("start"))
	stream := newTestAgentEventStream()
	runInnerLoop(context.Background(), agentCtx, nil, &LoopConfig{
		MaxConsecutiveToolCalls: 2,
		MaxToolCallsPerName:     100,
	}, stream)

	// call 1-2: normal execution
	// call 3: guard triggers → feedback → LLM sees result, tries again
	// call 4: LLM self-corrects and returns text (stop)
	if callCount != 4 {
		t.Fatalf("expected 4 calls (2 normal + 1 guard feedback + 1 self-correction), got %d", callCount)
	}

	// Verify loop guard event was emitted (soft feedback, not hard abort)
	var sawFeedbackEvent bool
	var sawHardAbort bool
	var sawFeedbackInTurnEnd bool
	for item := range stream.Iterator(context.Background()) {
		if item.Done {
			break
		}
		if item.Value.Type == EventLoopGuardTriggered && item.Value.LoopGuard != nil {
			sawFeedbackEvent = true
		}
		if item.Value.Type == EventTurnEnd {
			if item.Value.Message != nil && item.Value.Message.StopReason == "aborted" {
				sawHardAbort = true
			}
			// Check if the tool results in the turn end contain loop guard feedback
			for _, tr := range item.Value.ToolResults {
				if tr.IsError && strings.Contains(tr.ExtractText(), "[Loop guard]") {
					sawFeedbackInTurnEnd = true
				}
			}
		}
	}

	if !sawFeedbackEvent {
		t.Fatal("expected loop_guard_triggered event for soft feedback")
	}
	if sawHardAbort {
		t.Fatal("expected no hard abort — LLM should self-correct after feedback")
	}
	if !sawFeedbackInTurnEnd {
		t.Fatal("expected tool result with loop guard feedback in turn_end event")
	}
}

// TestLoopGuardFeedbackResetsOnSignatureChange verifies that the feedback counter
// resets when the LLM changes its tool call signature after receiving feedback.
func TestLoopGuardFeedbackResetsOnSignatureChange(t *testing.T) {
	orig := streamAssistantResponseFn
	defer func() { streamAssistantResponseFn = orig }()

	callCount := 0
	streamAssistantResponseFn = func(
		_ context.Context,
		_ *agentctx.AgentContext,
		_ *LoopConfig,
		_ *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
	) (*agentctx.AgentMessage, error) {
		callCount++
		msg := agentctx.NewAssistantMessage()

		if callCount <= 3 {
			// First 3 calls: repeat read /tmp/a.txt (guard triggers on call 3)
			msg.Content = []agentctx.ContentBlock{
				agentctx.ToolCallContent{
					ID:        "call-read-a-" + fmt.Sprint(callCount),
					Type:      "toolCall",
					Name:      "read",
					Arguments: map[string]any{"path": "/tmp/a.txt"},
				},
			}
			msg.StopReason = "tool_calls"
			return &msg, nil
		}

		if callCount == 4 {
			// After guard feedback, LLM switches to a different file (signature changes)
			msg.Content = []agentctx.ContentBlock{
				agentctx.ToolCallContent{
					ID:        "call-read-b-4",
					Type:      "toolCall",
					Name:      "read",
					Arguments: map[string]any{"path": "/tmp/b.txt"},
				},
			}
			msg.StopReason = "tool_calls"
			return &msg, nil
		}

		if callCount <= 6 {
			// Repeat /tmp/b.txt for calls 5 and 6
			msg.Content = []agentctx.ContentBlock{
				agentctx.ToolCallContent{
					ID:        "call-read-b-" + fmt.Sprint(callCount),
					Type:      "toolCall",
					Name:      "read",
					Arguments: map[string]any{"path": "/tmp/b.txt"},
				},
			}
			msg.StopReason = "tool_calls"
			return &msg, nil
		}

		// Self-correct after second guard feedback
		msg.Content = []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "done"},
		}
		msg.StopReason = "stop"
		return &msg, nil
	}

	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.RecentMessages = append(agentCtx.RecentMessages, agentctx.NewUserMessage("start"))
	stream := newTestAgentEventStream()
	runInnerLoop(context.Background(), agentCtx, nil, &LoopConfig{
		MaxConsecutiveToolCalls: 2,
		MaxToolCallsPerName:     100,
	}, stream)

	// call 1: read a.txt (consecutive=1) — executed
	// call 2: read a.txt (consecutive=2) — executed
	// call 3: read a.txt (consecutive=3) → guard triggers feedback #1
	// call 4: read b.txt (signature reset, consecutive=1) — executed
	// call 5: read b.txt (consecutive=2) — executed
	// call 6: read b.txt (consecutive=3) → guard triggers feedback #1 (fresh counter)
	// call 7: LLM self-corrects → done
	if callCount != 7 {
		t.Fatalf("expected 7 calls with signature reset, got %d", callCount)
	}

	var guardEventCount int
	for item := range stream.Iterator(context.Background()) {
		if item.Done {
			break
		}
		if item.Value.Type == EventLoopGuardTriggered {
			guardEventCount++
		}
	}
	// Two guard events: one for /tmp/a.txt loop, one for /tmp/b.txt loop
	if guardEventCount != 2 {
		t.Fatalf("expected 2 guard events (one per signature), got %d", guardEventCount)
	}
}

func TestRunInnerLoopCompactionRecoveryOnContextLimitStopReason(t *testing.T) {
	// Test that when LLM returns stopReason=model_context_window_exceeded
	// in a ChunkDone event (NOT an error), the context_limit_recovery path
	// still triggers compaction.
	orig := streamAssistantResponseFn
	defer func() { streamAssistantResponseFn = orig }()

	compactor := &recoveryCompactor{}
	callCount := 0
	streamAssistantResponseFn = func(
		_ context.Context,
		_ *agentctx.AgentContext,
		_ *LoopConfig,
		_ *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
	) (*agentctx.AgentMessage, error) {
		callCount++
		if callCount == 1 {
			// Simulate LLM returning context limit via stopReason (not error).
			// In real code, streamAssistantResponse detects this in ChunkDone
			// and converts it to a ContextLengthExceededError.
			return nil, &llm.ContextLengthExceededError{
				Message: "LLM returned stopReason=model_context_window_exceeded indicating context window exceeded",
			}
		}

		msg := agentctx.NewAssistantMessage()
		msg.Content = []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "recovered"},
		}
		msg.StopReason = "stop"
		return &msg, nil
	}

	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.RecentMessages = append(agentCtx.RecentMessages, agentctx.NewUserMessage("hello"))

	config := &LoopConfig{
		Compactors: []Compactor{compactor},
	}

	stream := newTestAgentEventStream()
	runInnerLoop(context.Background(), agentCtx, nil, config, stream)

	var gotStart, gotEnd bool
	var finalMessages []agentctx.AgentMessage
	for item := range stream.Iterator(context.Background()) {
		if item.Done {
			break
		}
		if item.Value.Type == EventCompactionStart {
			gotStart = true
		}
		if item.Value.Type == EventCompactionEnd {
			gotEnd = true
		}
		if item.Value.Type == EventAgentEnd {
			finalMessages = item.Value.Messages
		}
	}

	if compactor.calls != 1 {
		t.Fatalf("expected compactor to be called once, got %d", compactor.calls)
	}
	if callCount != 2 {
		t.Fatalf("expected assistant streaming to be called twice (recovery), got %d", callCount)
	}
	if !gotStart || !gotEnd {
		t.Fatalf("expected compaction start/end events, got start=%v end=%v", gotStart, gotEnd)
	}
	// Verify the agent recovered and returned the second response
	if len(finalMessages) == 0 {
		t.Fatal("expected final messages after recovery")
	}
}

// TestLoopGuardHardAbortRecovery verifies that after a loop guard hard abort,
// the LLM gets one recovery turn to produce a text response instead of
// silently terminating. On the recovery turn, if the LLM produces text
// (stopReason=stop), the agent terminates normally.
func TestLoopGuardHardAbortRecovery(t *testing.T) {
	orig := streamAssistantResponseFn
	defer func() { streamAssistantResponseFn = orig }()

	callCount := 0
	streamAssistantResponseFn = func(
		_ context.Context,
		_ *agentctx.AgentContext,
		_ *LoopConfig,
		_ *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
	) (*agentctx.AgentMessage, error) {
		callCount++
		msg := agentctx.NewAssistantMessage()

		if callCount <= 5 {
			// Calls 1-2: normal execution (maxConsecutive=2)
			// Call 3: triggers soft feedback #1 (consecutive=3 > 2)
			// Call 4: triggers soft feedback #2 (consecutive=4)
			// Call 5: triggers hard abort (consecutive=5, feedback exhausted)
			msg.Content = []agentctx.ContentBlock{
				agentctx.ToolCallContent{
					ID:        fmt.Sprintf("call-%d", callCount),
					Type:      "toolCall",
					Name:      "read",
					Arguments: map[string]any{"path": "/tmp/a.txt"},
				},
			}
			msg.StopReason = "tool_calls"
			return &msg, nil
		}

		// Call 6: recovery turn — LLM responds with text instead of tool call
		msg.Content = []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "I was stuck in a loop. Here's my final answer."},
		}
		msg.StopReason = "stop"
		return &msg, nil
	}

	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.RecentMessages = append(agentCtx.RecentMessages, agentctx.NewUserMessage("start"))
	stream := newTestAgentEventStream()
	runInnerLoop(context.Background(), agentCtx, nil, &LoopConfig{
		MaxConsecutiveToolCalls: 2,
		MaxToolCallsPerName:     100,
	}, stream)

	// call 1: read (consecutive=1) — executed
	// call 2: read (consecutive=2) — executed
	// call 3: read (consecutive=3) → soft feedback #1, guard blocks
	// call 4: read (consecutive=4) → soft feedback #2, guard blocks
	// call 5: read (consecutive=5) → hard abort → sanitize → recovery turn → continue
	// call 6: LLM responds with text → stop
	if callCount != 6 {
		t.Fatalf("expected 6 calls (2 normal + 2 feedback + 1 hard abort + 1 recovery text), got %d", callCount)
	}

	// Verify we got agent_end (normal termination after recovery)
	var sawAgentEnd bool
	var sawAbortedTurnEnd int
	for item := range stream.Iterator(context.Background()) {
		if item.Done {
			break
		}
		if item.Value.Type == EventAgentEnd {
			sawAgentEnd = true
		}
		if item.Value.Type == EventTurnEnd && item.Value.Message != nil && item.Value.Message.StopReason == "aborted" {
			sawAbortedTurnEnd++
		}
	}

	if !sawAgentEnd {
		t.Error("expected EventAgentEnd after recovery turn")
	}
	// Should see exactly one aborted turn (the hard abort that triggered recovery)
	if sawAbortedTurnEnd != 1 {
		t.Errorf("expected 1 aborted turn_end, got %d", sawAbortedTurnEnd)
	}
}

// variableOutputTool is a test tool that returns incrementing output each call.
type variableOutputTool struct {
	counter int
}

func (v *variableOutputTool) Name() string               { return "poll" }
func (v *variableOutputTool) Description() string        { return "poll for status" }
func (v *variableOutputTool) Parameters() map[string]any { return nil }
func (v *variableOutputTool) Execute(_ context.Context, _ map[string]any) ([]agentctx.ContentBlock, error) {
	v.counter++
	return []agentctx.ContentBlock{
		agentctx.TextContent{Type: "text", Text: fmt.Sprintf("status: iteration %d complete", v.counter)},
	}, nil
}

// TestLoopGuardPollingDetection verifies that when tool output keeps changing
// despite identical tool calls (polling pattern), the guard suppresses hard
// abort and only issues soft feedback. This prevents killing legitimate
// polling behavior like waiting for a benchmark to complete.
func TestLoopGuardPollingDetection(t *testing.T) {
	orig := streamAssistantResponseFn
	defer func() { streamAssistantResponseFn = orig }()

	var callCount int
	pollTool := &variableOutputTool{}

	streamAssistantResponseFn = func(
		_ context.Context,
		agentCtx *agentctx.AgentContext,
		_ *LoopConfig,
		_ *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
	) (*agentctx.AgentMessage, error) {
		callCount++

		// Register the variable-output tool on first call
		if callCount == 1 {
			agentCtx.Tools = []agentctx.Tool{pollTool}
		}

		msg := agentctx.NewAssistantMessage()

		if callCount <= 8 {
			// Repeated tool calls with identical arguments but changing output
			msg.Content = []agentctx.ContentBlock{
				agentctx.ToolCallContent{
					ID:        fmt.Sprintf("call-%d", callCount),
					Type:      "toolCall",
					Name:      "poll",
					Arguments: map[string]any{"query": "status"},
				},
			}
			msg.StopReason = "tool_calls"
			return &msg, nil
		}

		// After enough polling, LLM produces final answer
		msg.Content = []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "Polling complete. All iterations done."},
		}
		msg.StopReason = "stop"
		return &msg, nil
	}

	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.RecentMessages = append(agentCtx.RecentMessages, agentctx.NewUserMessage("poll for status"))
	stream := newTestAgentEventStream()
	runInnerLoop(context.Background(), agentCtx, nil, &LoopConfig{
		MaxConsecutiveToolCalls: 3, // trigger after 3 consecutive identical calls
		MaxToolCallsPerName:     100,
		EnableCheckpoint:        false,
	}, stream)

	// With polling detection:
	// call 1-3: normal execution (consecutive 1→3), tool runs, output changes
	// call 4: guard triggers soft feedback #1 (consecutive=4), output changed → suppress hard abort
	// call 5: guard triggers soft feedback #2 (consecutive=5), output changed → suppress hard abort
	// call 6: guard triggers soft feedback #3 (consecutive=6), still changing → would be hard abort but suppressed
	// call 7: soft feedback #4...
	// Eventually LLM self-corrects and returns text
	//
	// Key assertion: no hard abort despite exceeding maxFeedbackAttempts,
	// because output keeps changing (polling).
	var sawHardAbort bool
	var feedbackCount int
	for item := range stream.Iterator(context.Background()) {
		if item.Done {
			break
		}
		if item.Value.Type == EventTurnEnd && item.Value.Message != nil && item.Value.Message.StopReason == "aborted" {
			sawHardAbort = true
		}
		if item.Value.Type == EventLoopGuardTriggered {
			feedbackCount++
		}
	}

	if sawHardAbort {
		t.Error("expected NO hard abort when output is changing (polling pattern), but got one")
	}
	if feedbackCount == 0 {
		t.Error("expected some loop guard feedback events even with polling")
	}
	if callCount < 5 {
		t.Errorf("expected at least 5 calls (polling should be allowed to continue), got %d", callCount)
	}
}

// noopCompactor returns (nil, nil) — simulating ContextManager doing no work.
type noopCompactor struct {
	calls         int
	shouldCompact bool
}

func (c *noopCompactor) ShouldCompact(_ context.Context, _ *agentctx.AgentContext) bool {
	return c.shouldCompact
}

func (c *noopCompactor) Compact(_ context.Context, _ *agentctx.AgentContext) (*agentctx.CompactionResult, error) {
	c.calls++
	return nil, nil // no-op
}

func (c *noopCompactor) CalculateDynamicThreshold() int {
	return 100000
}

// TestPerformCompaction_NoOpDoesNotBlockFallback verifies that when the first
// compactor returns (nil, nil) (no actual work), the loop falls through to the
// next compactor which can then perform real compaction.
func TestPerformCompaction_NoOpDoesNotBlockFallback(t *testing.T) {
	orig := streamAssistantResponseFn
	defer func() { streamAssistantResponseFn = orig }()

	noop := &noopCompactor{shouldCompact: true}
	real := &recoveryCompactor{shouldCompact: true}

	streamAssistantResponseFn = func(
		_ context.Context,
		_ *agentctx.AgentContext,
		_ *LoopConfig,
		_ *llm.EventStream[AgentEvent, []agentctx.AgentMessage],
	) (*agentctx.AgentMessage, error) {
		msg := agentctx.NewAssistantMessage()
		msg.Content = []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "done"}}
		msg.StopReason = "stop"
		return &msg, nil
	}

	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.RecentMessages = append(agentCtx.RecentMessages, agentctx.NewUserMessage("hello"))
	stream := newTestAgentEventStream()

	ls := &loopState{
		agentCtx: agentCtx,
		config: &LoopConfig{
			Compactors: []Compactor{noop, real},
		},
		stream: stream,
	}

	result, err := ls.performCompaction(context.Background(), "test", true, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// noopCompactor should have been called but returned nil
	if noop.calls != 1 {
		t.Fatalf("expected noop compactor to be called once, got %d", noop.calls)
	}

	// recoveryCompactor should have been called as fallback
	if real.calls != 1 {
		t.Fatalf("expected real compactor to be called once as fallback, got %d", real.calls)
	}

	// The result should come from the real compactor
	if result == nil {
		t.Fatal("expected non-nil compaction result from fallback compactor")
	}
	if result.Summary != "[summary]" {
		t.Fatalf("expected summary from real compactor, got %q", result.Summary)
	}
}

// TestPerformCompaction_AllNoOpsReturnsNil verifies that when all compactors
// return (nil, nil), the overall result is nil with no error.
func TestPerformCompaction_AllNoOpsReturnsNil(t *testing.T) {
	noop1 := &noopCompactor{shouldCompact: true}
	noop2 := &noopCompactor{shouldCompact: true}

	agentCtx := agentctx.NewAgentContext("sys")
	agentCtx.RecentMessages = append(agentCtx.RecentMessages, agentctx.NewUserMessage("hello"))
	stream := newTestAgentEventStream()

	ls := &loopState{
		agentCtx: agentCtx,
		config: &LoopConfig{
			Compactors: []Compactor{noop1, noop2},
		},
		stream: stream,
	}

	result, err := ls.performCompaction(context.Background(), "test", true, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Fatalf("expected nil result when all compactors are no-op, got %+v", result)
	}
	if noop1.calls != 1 || noop2.calls != 1 {
		t.Fatalf("expected both compactors to be called, got noop1=%d noop2=%d", noop1.calls, noop2.calls)
	}
}
