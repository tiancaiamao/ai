package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStreamLLMEmitsDoneOnBareDoneFrame(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("unexpected authorization header: %q", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	model := Model{
		ID:       "test-model",
		Provider: "test",
		BaseURL:  server.URL,
		API:      "openai-completions",
	}
	llmCtx := LLMContext{
		Messages: []LLMMessage{
			{Role: "user", Content: "ping"},
		},
	}

	stream := StreamLLM(context.Background(), model, llmCtx, "test-key", 0)

	var sawDone bool
	for item := range stream.Iterator(context.Background()) {
		switch event := item.Value.(type) {
		case LLMDoneEvent:
			sawDone = true
			if event.StopReason != "stop" {
				t.Fatalf("expected synthetic stop reason, got %q", event.StopReason)
			}
			if event.Message == nil {
				t.Fatal("expected done event to include message")
			}
			if got := event.Message.Content; got != "hello" {
				t.Fatalf("unexpected message content: %q", got)
			}
		case LLMErrorEvent:
			t.Fatalf("unexpected error event: %v", event.Error)
		}
	}

	if !sawDone {
		t.Fatal("expected done event when stream ends with [DONE]")
	}
}

func TestStreamLLMHandlesLargeSSELine(t *testing.T) {
	largeText := strings.Repeat("x", 70*1024)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", largeText)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	model := Model{
		ID:       "test-model",
		Provider: "test",
		BaseURL:  server.URL,
		API:      "openai-completions",
	}
	llmCtx := LLMContext{
		Messages: []LLMMessage{
			{Role: "user", Content: "ping"},
		},
	}

	stream := StreamLLM(context.Background(), model, llmCtx, "test-key", 0)

	var doneContent string
	for item := range stream.Iterator(context.Background()) {
		switch event := item.Value.(type) {
		case LLMDoneEvent:
			if event.Message == nil {
				t.Fatal("expected done event to include message")
			}
			doneContent = event.Message.Content
		case LLMErrorEvent:
			t.Fatalf("unexpected error event for large SSE line: %v", event.Error)
		}
	}

	if len(doneContent) != len(largeText) {
		t.Fatalf("unexpected done content length: got %d want %d", len(doneContent), len(largeText))
	}
}

func TestStreamLLMErrorOnEmptySSEStream(t *testing.T) {
	// Server returns HTTP 200 with empty body (no SSE data chunks at all).
	// This simulates the LLM server closing the connection prematurely.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// Write nothing — just close the connection.
	}))
	defer server.Close()

	model := Model{
		ID:       "test-model",
		Provider: "test",
		BaseURL:  server.URL,
		API:      "openai-completions",
	}
	llmCtx := LLMContext{
		Messages: []LLMMessage{
			{Role: "user", Content: "ping"},
		},
	}

	stream := StreamLLM(context.Background(), model, llmCtx, "test-key", 0)

	var sawError bool
	var sawDone bool
	for item := range stream.Iterator(context.Background()) {
		switch event := item.Value.(type) {
		case LLMErrorEvent:
			sawError = true
			if event.Error == nil {
				t.Fatal("expected non-nil error in LLMErrorEvent")
			}
			errMsg := event.Error.Error()
			if !strings.Contains(errMsg, "without any data chunks") {
				t.Fatalf("expected error about no data chunks, got: %s", errMsg)
			}
		case LLMDoneEvent:
			sawDone = true
		default:
			// LLMStartEvent is expected
		}
	}

	if !sawError {
		t.Fatal("expected LLMErrorEvent when stream has zero data chunks")
	}
	if sawDone {
		t.Fatal("should not see LLMDoneEvent when stream has zero data chunks")
	}
}

func TestStreamLLMSyntheticDoneOnTruncatedStream(t *testing.T) {
	// Server sends some content chunks but closes without finish_reason or [DONE].
	// The client should synthesize a DoneEvent with the accumulated content.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello world\"}}]}\n\n")
		// No finish_reason, no [DONE] — stream just ends.
	}))
	defer server.Close()

	model := Model{
		ID:       "test-model",
		Provider: "test",
		BaseURL:  server.URL,
		API:      "openai-completions",
	}
	llmCtx := LLMContext{
		Messages: []LLMMessage{
			{Role: "user", Content: "ping"},
		},
	}

	stream := StreamLLM(context.Background(), model, llmCtx, "test-key", 0)

	var sawDone bool
	var sawError bool
	for item := range stream.Iterator(context.Background()) {
		switch event := item.Value.(type) {
		case LLMDoneEvent:
			sawDone = true
			if event.StopReason != "stop" {
				t.Fatalf("expected synthetic stop reason, got %q", event.StopReason)
			}
			if event.Message == nil {
				t.Fatal("expected done event to include message")
			}
			if got := event.Message.Content; got != "hello world" {
				t.Fatalf("unexpected message content: %q", got)
			}
		case LLMErrorEvent:
			sawError = true
			t.Fatalf("unexpected error event: %v", event.Error)
		default:
			// LLMStartEvent, LLMTextDeltaEvent are expected
		}
	}

	if !sawDone {
		t.Fatal("expected LLMDoneEvent when stream has data chunks but no finish_reason")
	}
	if sawError {
		t.Fatal("should not see LLMErrorEvent when stream has data chunks")
	}
}

func TestStreamLLMToolCallDeltaNoDataRace(t *testing.T) {
	// Simulate streaming tool call deltas where arguments accumulate over
	// multiple chunks. This exercises the data race scenario where
	// AppendToolCall modifies the *ToolCall while the event consumer
	// concurrently reads it.
	//
	// Before the fix, the same *ToolCall pointer was shared between
	// AppendToolCall (SSE reader goroutine) and the LLMToolCallDeltaEvent
	// (consumer goroutine), causing a data race.
	//
	// With -race, the race detector will detect the shared pointer access
	// if the fix is missing.

	mu := sync.Mutex{}
	var toolCallEvents []LLMToolCallDeltaEvent
	var doneEvent *LLMDoneEvent
	var errEvent error

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, canFlush := w.(http.Flusher)

		write := func(s string) {
			fmt.Fprint(w, s)
			if canFlush {
				flusher.Flush()
			}
		}

		// First delta: creates the tool call with name and empty arguments
		write("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"test_tool\",\"arguments\":\"\"}}]},\"finish_reason\":null}]}\n\n")
		// Accumulate arguments over multiple deltas
		write("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"key\\\":\"}}]},\"finish_reason\":null}]}\n\n")
		write("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\" \\\"value\\\"\"}}]},\"finish_reason\":null}]}\n\n")
		write("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"}\"}}]},\"finish_reason\":null}]}\n\n")

		// Second tool call at index 1
		write("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":1,\"id\":\"call_2\",\"type\":\"function\",\"function\":{\"name\":\"other_tool\",\"arguments\":\"\"}}]},\"finish_reason\":null}]}\n\n")
		write("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":1,\"function\":{\"arguments\":\"{\\\"a\\\":\"}}]},\"finish_reason\":null}]}\n\n")
		write("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":1,\"function\":{\"arguments\":\" 1}\"}}]},\"finish_reason\":null}]}\n\n")

		// Finish
		write("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n")
		write("data: [DONE]\n\n")
	}))
	defer server.Close()

	model := Model{
		ID:       "test-model",
		Provider: "test",
		BaseURL:  server.URL,
		API:      "openai-completions",
	}
	llmCtx := LLMContext{
		Messages: []LLMMessage{
			{Role: "user", Content: "call the tool"},
		},
	}

	stream := StreamLLM(context.Background(), model, llmCtx, "test-key", 0)

	// Iterate in the test goroutine — the iterator runs its own goroutine,
	// and the SSE reading happens in another goroutine. Three goroutines
	// are involved, giving the race detector ample opportunity to detect
	// unsynchronized pointer access.
	for item := range stream.Iterator(context.Background()) {
		switch event := item.Value.(type) {
		case LLMToolCallDeltaEvent:
			// Reading ToolCall pointer while SSE goroutine may be writing
			mu.Lock()
			toolCallEvents = append(toolCallEvents, event)
			mu.Unlock()
		case LLMDoneEvent:
			doneEvent = &event
		case LLMErrorEvent:
			errEvent = event.Error
		}
	}

	if errEvent != nil {
		t.Fatalf("unexpected error: %v", errEvent)
	}

	if doneEvent == nil {
		t.Fatal("expected done event")
	}
	if doneEvent.Message == nil {
		t.Fatal("expected final message from done event")
	}
	if len(doneEvent.Message.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls in final message, got %d", len(doneEvent.Message.ToolCalls))
	}
	if doneEvent.Message.ToolCalls[0].Function.Name != "test_tool" {
		t.Fatalf("expected first tool call name test_tool, got %q", doneEvent.Message.ToolCalls[0].Function.Name)
	}
	if doneEvent.Message.ToolCalls[1].Function.Name != "other_tool" {
		t.Fatalf("expected second tool call name other_tool, got %q", doneEvent.Message.ToolCalls[1].Function.Name)
	}
	if doneEvent.Message.ToolCalls[0].Function.Arguments != `{"key": "value"}` {
		t.Fatalf("unexpected arguments for tool call 0: %q", doneEvent.Message.ToolCalls[0].Function.Arguments)
	}
	if doneEvent.Message.ToolCalls[1].Function.Arguments != `{"a": 1}` {
		t.Fatalf("unexpected arguments for tool call 1: %q", doneEvent.Message.ToolCalls[1].Function.Arguments)
	}

	// Also verify each delta event has a non-nil ToolCall pointer
	mu.Lock()
	for i, ev := range toolCallEvents {
		if ev.ToolCall == nil {
			t.Fatalf("tool call delta event %d has nil ToolCall", i)
		}
		// Name should only be set on index 0 deltas
		if (ev.Index == 0 || ev.Index == 1) && ev.ToolCall == nil {
			t.Fatalf("tool call delta event %d (index %d) has nil ToolCall after fix", i, ev.Index)
		}
	}
	mu.Unlock()
}

func TestParseRetryAfterHeader(t *testing.T) {
	if got := parseRetryAfterHeader("5"); got != 5*time.Second {
		t.Fatalf("expected 5s from integer header, got %v", got)
	}
	future := time.Now().Add(2 * time.Second).UTC().Format(http.TimeFormat)
	if got := parseRetryAfterHeader(future); got <= 0 {
		t.Fatalf("expected positive duration from http-date header, got %v", got)
	}
	if got := parseRetryAfterHeader("invalid"); got != 0 {
		t.Fatalf("expected 0 from invalid header, got %v", got)
	}
}
