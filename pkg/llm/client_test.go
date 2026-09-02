package llm

import (
	"context"
	"encoding/json"
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

func TestStreamLLMAddsConfiguredReasoningContext(t *testing.T) {
	// The gateway requirement is declared per-model ("reasoningContext" in
	// models.json), NOT inferred from the provider name — renaming the
	// provider must not change behavior.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		reasoning, ok := body["reasoning"].(map[string]any)
		if !ok || reasoning["context"] != "all_turns" {
			t.Fatalf("reasoning = %#v, want context all_turns", body["reasoning"])
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	stream := StreamLLM(context.Background(), Model{
		ID:               "gpt-5.6-luna",
		Provider:         "renamed-provider",
		BaseURL:          server.URL,
		API:              "",
		ReasoningContext: "all_turns",
	}, LLMContext{Messages: []LLMMessage{{Role: "user", Content: "hello"}}}, "test-key", 0)

	for item := range stream.Iterator(context.Background()) {
		if event, ok := item.Value.(LLMErrorEvent); ok {
			t.Fatalf("unexpected error: %v", event.Error)
		}
	}
}

func TestStreamLLMOmitsReasoningContextByDefault(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if reasoning, ok := body["reasoning"].(map[string]any); ok {
			if _, has := reasoning["context"]; has {
				t.Fatalf("reasoning.context = %v, want absent when ReasoningContext is not configured", reasoning["context"])
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	stream := StreamLLM(context.Background(), Model{
		ID:       "test-model",
		Provider: "test",
		BaseURL:  server.URL,
		API:      "",
	}, LLMContext{Messages: []LLMMessage{{Role: "user", Content: "hello"}}}, "test-key", 0)

	for item := range stream.Iterator(context.Background()) {
		if event, ok := item.Value.(LLMErrorEvent); ok {
			t.Fatalf("unexpected error: %v", event.Error)
		}
	}
}

func TestStreamLLMHandlesReasoningField(t *testing.T) {
	// Ollama's OpenAI-compatible endpoint sends `reasoning` (not
	// reasoning_content nor thinking) in the streaming delta.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, canFlush := w.(http.Flusher)

		write := func(s string) {
			fmt.Fprint(w, s)
			if canFlush {
				flusher.Flush()
			}
		}

		// Stream: reasoning content first, then text content.
		write("data: {\"choices\":[{\"delta\":{\"reasoning\":\"thinking step 1\"}}]}\n\n")
		write("data: {\"choices\":[{\"delta\":{\"reasoning\":\" thinking step 2\"}}]}\n\n")
		write("data: {\"choices\":[{\"delta\":{\"content\":\"final answer\"}}]}\n\n")
		write("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
		write("data: [DONE]\n\n")
	}))
	defer server.Close()

	model := Model{
		ID:       "test-model",
		Provider: "ollama",
		BaseURL:  server.URL,
		API:      "openai-completions",
	}
	llmCtx := LLMContext{
		Messages: []LLMMessage{
			{Role: "user", Content: "test"},
		},
	}

	stream := StreamLLM(context.Background(), model, llmCtx, "test-key", 0)

	var thinkingDeltas []string
	var textDeltas []string
	var doneEvent *LLMDoneEvent

	for item := range stream.Iterator(context.Background()) {
		switch event := item.Value.(type) {
		case LLMThinkingDeltaEvent:
			thinkingDeltas = append(thinkingDeltas, event.Delta)
		case LLMTextDeltaEvent:
			textDeltas = append(textDeltas, event.Delta)
		case LLMDoneEvent:
			doneEvent = &event
		case LLMErrorEvent:
			t.Fatalf("unexpected error: %v", event.Error)
		}
	}

	// Verify thinking/reasoning deltas were captured
	if len(thinkingDeltas) != 2 {
		t.Fatalf("expected 2 thinking deltas, got %d: %v", len(thinkingDeltas), thinkingDeltas)
	}
	if thinkingDeltas[0] != "thinking step 1" {
		t.Fatalf("unexpected first thinking delta: %q", thinkingDeltas[0])
	}
	if thinkingDeltas[1] != " thinking step 2" {
		t.Fatalf("unexpected second thinking delta: %q", thinkingDeltas[1])
	}

	// Verify text content
	if len(textDeltas) != 1 {
		t.Fatalf("expected 1 text delta, got %d: %v", len(textDeltas), textDeltas)
	}
	if textDeltas[0] != "final answer" {
		t.Fatalf("unexpected text delta: %q", textDeltas[0])
	}

	// Verify final message
	if doneEvent == nil {
		t.Fatal("expected done event")
	}
	if doneEvent.Message == nil {
		t.Fatal("expected final message")
	}
	if doneEvent.Message.Content != "final answer" {
		t.Fatalf("unexpected final content: %q", doneEvent.Message.Content)
	}
	if doneEvent.Message.Thinking != "thinking step 1 thinking step 2" {
		t.Fatalf("unexpected final thinking: %q", doneEvent.Message.Thinking)
	}
}

func TestStreamLLMHandlesAllReasoningFields(t *testing.T) {
	// Verify all three reasoning-related delta fields work:
	//   reasoning_content (Z.AI / DeepSeek)
	//   thinking          (Anthropic)
	//   reasoning         (Ollama)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"from reasoning_content\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"thinking\":\"from thinking\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning\":\"from reasoning\"}}]}\n\n")
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
			{Role: "user", Content: "test"},
		},
	}

	stream := StreamLLM(context.Background(), model, llmCtx, "test-key", 0)

	var thinkingDeltas []string
	var doneEvent *LLMDoneEvent

	for item := range stream.Iterator(context.Background()) {
		switch event := item.Value.(type) {
		case LLMThinkingDeltaEvent:
			thinkingDeltas = append(thinkingDeltas, event.Delta)
		case LLMDoneEvent:
			doneEvent = &event
		case LLMErrorEvent:
			t.Fatalf("unexpected error: %v", event.Error)
		}
	}

	if len(thinkingDeltas) != 3 {
		t.Fatalf("expected 3 thinking deltas (one per field), got %d: %v", len(thinkingDeltas), thinkingDeltas)
	}
	if thinkingDeltas[0] != "from reasoning_content" {
		t.Fatalf("expected reasoning_content first, got %q", thinkingDeltas[0])
	}
	if thinkingDeltas[1] != "from thinking" {
		t.Fatalf("expected thinking second, got %q", thinkingDeltas[1])
	}
	if thinkingDeltas[2] != "from reasoning" {
		t.Fatalf("expected reasoning third, got %q", thinkingDeltas[2])
	}
	if doneEvent == nil {
		t.Fatal("expected done event")
	}
	if doneEvent.Message.Thinking != "from reasoning_contentfrom thinkingfrom reasoning" {
		t.Fatalf("unexpected accumulated thinking: %q", doneEvent.Message.Thinking)
	}
}
