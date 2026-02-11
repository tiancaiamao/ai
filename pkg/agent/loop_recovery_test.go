package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/tiancaiamao/ai/pkg/llm"
)

type recoveryCompactor struct {
	calls int
}

func (c *recoveryCompactor) ShouldCompact(_ []AgentMessage) bool {
	return true
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
	return true
}

func (f *failingCompactor) Compact(_ []AgentMessage) ([]AgentMessage, error) {
	return nil, errors.New("compaction failed")
}
