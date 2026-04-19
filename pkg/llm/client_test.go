package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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
