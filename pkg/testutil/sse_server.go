package testutil

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
)

// SSEBuilder constructs an SSE response body for OpenAI-completions API.
// Build up the response by calling Text, ToolCall, Thinking, then Done.
// Finally call Build() to get the full SSE response string.
//
// Usage:
//
//	response := NewSSEBuilder().Text("hello").Done("stop", UsageFields{Prompt: 10, Completion: 5})
//	server := LLMServer(response)
type SSEBuilder struct {
	chunks []string
}

// UsageFields holds token usage for an SSE response.
type UsageFields struct {
	Prompt     int
	Completion int
}

// NewSSEBuilder creates a new SSEBuilder.
func NewSSEBuilder() *SSEBuilder {
	return &SSEBuilder{}
}

// Text appends a text content delta chunk.
func (b *SSEBuilder) Text(content string) *SSEBuilder {
	b.chunks = append(b.chunks, fmt.Sprintf(
		`{"choices":[{"delta":{"content":%q}}]}`,
		content,
	))
	return b
}

// ToolCall appends a tool_call delta chunk. The arguments are JSON-serialized
// and double-encoded to match the OpenAI wire format where function.arguments
// is a string containing JSON.
func (b *SSEBuilder) ToolCall(id, name string, args map[string]any) *SSEBuilder {
	argsJSON, _ := json.Marshal(args)
	escapedArgs, _ := json.Marshal(string(argsJSON))
	b.chunks = append(b.chunks, fmt.Sprintf(
		`{"choices":[{"delta":{"tool_calls":[{"index":%d,"id":"%s","type":"function","function":{"name":"%s","arguments":%s}}]}}]}`,
		len(b.chunks), id, name, string(escapedArgs),
	))
	return b
}

// Thinking appends a reasoning_content delta chunk (for thinking/reasoning models).
func (b *SSEBuilder) Thinking(content string) *SSEBuilder {
	b.chunks = append(b.chunks, fmt.Sprintf(
		`{"choices":[{"delta":{"reasoning_content":%q}}]}`,
		content,
	))
	return b
}

// Finish appends the final chunk with finish_reason and usage, then returns
// the complete SSE response string ready to serve via HTTP.
func (b *SSEBuilder) Finish(stopReason string, usage UsageFields) string {
	b.chunks = append(b.chunks, fmt.Sprintf(
		`{"choices":[{"delta":{},"finish_reason":"%s"}],"usage":{"prompt_tokens":%d,"completion_tokens":%d,"total_tokens":%d}}`,
		stopReason, usage.Prompt, usage.Completion, usage.Prompt+usage.Completion,
	))

	var sb strings.Builder
	for _, chunk := range b.chunks {
		sb.WriteString("data: ")
		sb.WriteString(chunk)
		sb.WriteString("\n\n")
	}
	sb.WriteString("data: [DONE]\n\n")
	return sb.String()
}

// TextResponse is a shorthand for a simple text-only SSE response.
func TextResponse(text string) string {
	return NewSSEBuilder().Text(text).Finish("stop", UsageFields{Prompt: 10, Completion: 5})
}

// ToolCallResponse is a shorthand for an SSE response with tool calls.
func ToolCallResponse(id, name string, args map[string]any, stopReason string) string {
	return NewSSEBuilder().ToolCall(id, name, args).Finish(stopReason, UsageFields{Prompt: 10, Completion: 5})
}

// LLMServer creates an httptest.Server that serves the given SSE responses
// sequentially, one per HTTP request. Each call to the server pops the next
// response from the queue. Panics if the queue is exhausted.
//
// Usage:
//
//	srv := LLMServer(TextResponse("hello"), ToolCallResponse("c1", "bash", args, "tool_calls"))
//	defer srv.Close()
//	// use srv.URL as BaseURL in Model
func LLMServer(responses ...string) *httptest.Server {
	mu := sync.Mutex{}
	idx := 0

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if idx >= len(responses) {
			mu.Unlock()
			w.WriteHeader(http.StatusInternalServerError)
			io.WriteString(w, "no more mock responses")
			return
		}
		resp := responses[idx]
		idx++
		mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, resp)
	}))
}

// LLMServerFactory creates an httptest.Server where each request is handled by
// calling the provided function with the request index (0-based). This is
// useful when you need dynamic responses based on request content or order.
func LLMServerFactory(fn func(callIndex int, r *http.Request) string) *httptest.Server {
	mu := sync.Mutex{}
	idx := 0

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		i := idx
		idx++
		mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, fn(i, r))
	}))
}
