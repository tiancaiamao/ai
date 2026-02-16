package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tiancaiamao/ai/pkg/llm"
)

type recoveryCompactor struct {
	calls         int
	shouldCompact bool
}

func (c *recoveryCompactor) ShouldCompact(_ []AgentMessage) bool {
	return c.shouldCompact
}

func (c *recoveryCompactor) Compact(messages []AgentMessage) ([]AgentMessage, error) {
	c.calls++
	return []AgentMessage{
		NewUserMessage("[summary]"),
		NewUserMessage("latest request"),
	}, nil
}

func newTestAgentEventStream() *llm.EventStream[AgentEvent, []AgentMessage] {
	return llm.NewEventStream[AgentEvent, []AgentMessage](
		func(e AgentEvent) bool { return e.Type == EventAgentEnd },
		func(e AgentEvent) []AgentMessage { return e.Messages },
	)
}

func TestRunInnerLoopCompactionRecoveryOnContextLengthError(t *testing.T) {
	orig := streamAssistantResponseFn
	defer func() { streamAssistantResponseFn = orig }()

	compactor := &recoveryCompactor{}
	callCount := 0
	streamAssistantResponseFn = func(
		_ context.Context,
		_ *AgentContext,
		_ *LoopConfig,
		_ *llm.EventStream[AgentEvent, []AgentMessage],
	) (*AgentMessage, error) {
		callCount++
		if callCount == 1 {
			return nil, &llm.ContextLengthExceededError{Message: "maximum context length exceeded"}
		}

		msg := NewAssistantMessage()
		msg.Content = []ContentBlock{
			TextContent{Type: "text", Text: "done"},
		}
		msg.StopReason = "stop"
		return &msg, nil
	}

	agentCtx := NewAgentContext("sys")
	agentCtx.Messages = append(agentCtx.Messages, NewUserMessage("hello"))

	config := &LoopConfig{
		Compactor: compactor,
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
		_ *AgentContext,
		_ *LoopConfig,
		_ *llm.EventStream[AgentEvent, []AgentMessage],
	) (*AgentMessage, error) {
		callCount++
		msg := NewAssistantMessage()
		msg.Content = []ContentBlock{TextContent{Type: "text", Text: "done"}}
		msg.StopReason = "stop"
		return &msg, nil
	}

	agentCtx := NewAgentContext("sys")
	agentCtx.Messages = append(agentCtx.Messages, NewUserMessage("hello"))
	stream := newTestAgentEventStream()
	runInnerLoop(context.Background(), agentCtx, nil, &LoopConfig{Compactor: compactor}, stream)

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
		_ *AgentContext,
		_ *LoopConfig,
		_ *llm.EventStream[AgentEvent, []AgentMessage],
	) (*AgentMessage, error) {
		callCount++
		msg := NewAssistantMessage()
		msg.Content = []ContentBlock{
			ToolCallContent{
				ID:        "call-repeat",
				Type:      "toolCall",
				Name:      "read",
				Arguments: map[string]any{"path": "/tmp/a.txt"},
			},
		}
		msg.StopReason = "tool_calls"
		return &msg, nil
	}

	agentCtx := NewAgentContext("sys")
	agentCtx.Messages = append(agentCtx.Messages, NewUserMessage("start"))
	stream := newTestAgentEventStream()
	runInnerLoop(context.Background(), agentCtx, nil, &LoopConfig{
		MaxConsecutiveToolCalls: 2,
		MaxToolCallsPerName:     100,
	}, stream)

	if callCount != 3 {
		t.Fatalf("expected loop guard to stop on third repeated call, got %d calls", callCount)
	}

	var sawGuardedTurn bool
	var sawGuardEvent bool
	for item := range stream.Iterator(context.Background()) {
		if item.Done {
			break
		}
		if item.Value.Type == EventLoopGuardTriggered && item.Value.LoopGuard != nil && strings.TrimSpace(item.Value.LoopGuard.Reason) != "" {
			sawGuardEvent = true
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
}

func TestRunInnerLoopPersistsAssistantMessagesInContext(t *testing.T) {
	orig := streamAssistantResponseFn
	defer func() { streamAssistantResponseFn = orig }()

	callCount := 0
	streamAssistantResponseFn = func(
		_ context.Context,
		_ *AgentContext,
		_ *LoopConfig,
		_ *llm.EventStream[AgentEvent, []AgentMessage],
	) (*AgentMessage, error) {
		callCount++
		if callCount == 1 {
			msg := NewAssistantMessage()
			msg.Content = []ContentBlock{
				ToolCallContent{
					ID:        "call-1",
					Type:      "toolCall",
					Name:      "read",
					Arguments: map[string]any{"path": "/tmp/a.txt"},
				},
			}
			msg.StopReason = "tool_calls"
			return &msg, nil
		}

		msg := NewAssistantMessage()
		msg.Content = []ContentBlock{
			TextContent{Type: "text", Text: "done"},
		}
		msg.StopReason = "stop"
		return &msg, nil
	}

	agentCtx := NewAgentContext("sys")
	agentCtx.Messages = append(agentCtx.Messages, NewUserMessage("start"))
	stream := newTestAgentEventStream()
	runInnerLoop(context.Background(), agentCtx, nil, &LoopConfig{}, stream)

	if callCount != 2 {
		t.Fatalf("expected two assistant turns, got %d", callCount)
	}

	var sawToolCallAssistant bool
	var sawFinalAssistant bool
	for _, msg := range agentCtx.Messages {
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
		_ *AgentContext,
		_ *LoopConfig,
		_ *llm.EventStream[AgentEvent, []AgentMessage],
	) (*AgentMessage, error) {
		callCount++
		return nil, &llm.ContextLengthExceededError{Message: "context window exceeded"}
	}

	stream := newTestAgentEventStream()
	_, err := streamAssistantResponseWithRetry(
		context.Background(),
		NewAgentContext("sys"),
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

func TestRunInnerLoopCompactionRecoveryFailureFallsBackToError(t *testing.T) {
	orig := streamAssistantResponseFn
	defer func() { streamAssistantResponseFn = orig }()

	streamAssistantResponseFn = func(
		_ context.Context,
		_ *AgentContext,
		_ *LoopConfig,
		_ *llm.EventStream[AgentEvent, []AgentMessage],
	) (*AgentMessage, error) {
		return nil, &llm.ContextLengthExceededError{Message: "context window exceeded"}
	}

	brokenCompactor := &failingCompactor{}
	stream := newTestAgentEventStream()
	agentCtx := NewAgentContext("sys")
	agentCtx.Messages = append(agentCtx.Messages, NewUserMessage("hello"))

	runInnerLoop(context.Background(), agentCtx, nil, &LoopConfig{Compactor: brokenCompactor}, stream)

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

type failingCompactor struct{}

func (f *failingCompactor) ShouldCompact(_ []AgentMessage) bool {
	return false
}

func (f *failingCompactor) Compact(_ []AgentMessage) ([]AgentMessage, error) {
	return nil, errors.New("compaction failed")
}
