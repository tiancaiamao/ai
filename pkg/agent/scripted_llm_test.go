package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/tiancaiamao/ai/pkg/llm"
)

// ScriptedResponse defines a single scripted LLM response.
type ScriptedResponse struct {
	Text       string
	ToolCalls  []ToolCallResponse
	StopReason string
	Usage      *UsageResponse
}

// ToolCallResponse defines a tool call in a scripted response.
type ToolCallResponse struct {
	ID   string
	Name string
	Args map[string]any
}

// UsageResponse defines usage info for a scripted response.
type UsageResponse struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
}

// CapturedRequest records a single LLM call for test assertions.
type CapturedRequest struct {
	Messages []llm.LLMMessage
	Tools    []llm.LLMTool
}

// RespondWithText creates a scripted response that returns plain text.
func RespondWithText(text string) ScriptedResponse {
	return ScriptedResponse{
		Text:       text,
		StopReason: "stop",
		Usage: &UsageResponse{
			InputTokens:  100,
			OutputTokens: 50,
			TotalTokens:  150,
		},
	}
}

// RespondWithToolCall creates a scripted response that returns a single tool call.
func RespondWithToolCall(name string, args map[string]any) ScriptedResponse {
	return RespondWithToolCalls(ToolCallResponse{
		ID:   fmt.Sprintf("call_%s", name),
		Name: name,
		Args: args,
	})
}

// RespondWithToolCalls creates a scripted response that returns multiple tool calls.
func RespondWithToolCalls(calls ...ToolCallResponse) ScriptedResponse {
	return ScriptedResponse{
		ToolCalls:  calls,
		StopReason: "tool_calls",
		Usage: &UsageResponse{
			InputTokens:  100,
			OutputTokens: 50,
			TotalTokens:  150,
		},
	}
}

// ScriptedLLM implements LLMCaller by pushing scripted responses as SSE events.
// It also captures all incoming requests for test assertions.
type ScriptedLLM struct {
	responses []ScriptedResponse
	index     int
	mu        sync.Mutex
	requests  []CapturedRequest
}

// NewScriptedLLM creates a ScriptedLLM that returns the given responses in order.
// If responses are exhausted, subsequent calls return an error.
func NewScriptedLLM(responses ...ScriptedResponse) *ScriptedLLM {
	return &ScriptedLLM{
		responses: responses,
	}
}

// Call implements LLMCaller. It simulates an SSE stream by pushing events
// into an EventStream that processLLMResponse can consume.
func (s *ScriptedLLM) Call(
	ctx context.Context,
	model llm.Model,
	llmCtx llm.LLMContext,
	apiKey string,
	timeout time.Duration,
) *llm.EventStream[llm.LLMEvent, llm.LLMMessage] {
	stream := llm.NewEventStream[llm.LLMEvent, llm.LLMMessage](
		func(e llm.LLMEvent) bool {
			return e.GetEventType() == "done" || e.GetEventType() == "error"
		},
		func(e llm.LLMEvent) llm.LLMMessage {
			if done, ok := e.(llm.LLMDoneEvent); ok && done.Message != nil {
				return *done.Message
			}
			return llm.LLMMessage{}
		},
	)

	// Capture the request for assertions
	s.mu.Lock()
	s.requests = append(s.requests, CapturedRequest{
		Messages: llmCtx.Messages,
		Tools:    llmCtx.Tools,
	})
	respIdx := s.index
	s.index++
	s.mu.Unlock()

	// Push events asynchronously (same pattern as llm.StreamLLM)
	go func() {
		defer stream.End(llm.LLMMessage{})

		if respIdx >= len(s.responses) {
			stream.Push(llm.LLMErrorEvent{
				Error: fmt.Errorf("ScriptedLLM: no more scripted responses (index=%d, total=%d)", respIdx, len(s.responses)),
			})
			return
		}

		resp := s.responses[respIdx]
		partial := llm.NewPartialMessage()

		// Push start event
		stream.Push(llm.LLMStartEvent{Partial: partial})

		// Push text deltas
		if resp.Text != "" {
			partial.AppendText(resp.Text)
			stream.Push(llm.LLMTextDeltaEvent{Delta: resp.Text, Index: 0})
		}

		// Push tool call deltas
		for i, tc := range resp.ToolCalls {
			id := tc.ID
			if id == "" {
				id = fmt.Sprintf("call_scripted_%d_%d", respIdx, i)
			}

			// Build arguments JSON
			argsJSON := "{}"
			if tc.Args != nil {
				if bytes, err := json.Marshal(tc.Args); err == nil {
					argsJSON = string(bytes)
				}
			}

			// Register tool call in partial message
			partial.AppendToolCall(i, &llm.ToolCall{
				ID:   id,
				Type: "function",
				Function: llm.FunctionCall{
					Name:      tc.Name,
					Arguments: "",
				},
			})
			stream.Push(llm.LLMToolCallDeltaEvent{Index: i, ToolCall: &llm.ToolCall{
				ID:   id,
				Type: "function",
				Function: llm.FunctionCall{
					Name: tc.Name,
				},
			}})

			// Push arguments delta separately
			partial.AppendToolCall(i, &llm.ToolCall{
				Function: llm.FunctionCall{
					Arguments: argsJSON,
				},
			})
			stream.Push(llm.LLMToolCallDeltaEvent{Index: i, ToolCall: &llm.ToolCall{
				Function: llm.FunctionCall{
					Arguments: argsJSON,
				},
			}})
		}

		// Build final message from partial
		finalMsg := partial.ToLLMMessage()
		usage := llm.Usage{}
		if resp.Usage != nil {
			usage.InputTokens = resp.Usage.InputTokens
			usage.OutputTokens = resp.Usage.OutputTokens
			usage.TotalTokens = resp.Usage.TotalTokens
		}

		stopReason := resp.StopReason
		if stopReason == "" {
			if len(resp.ToolCalls) > 0 {
				stopReason = "tool_calls"
			} else {
				stopReason = "stop"
			}
		}

		stream.Push(llm.LLMDoneEvent{
			Message:    &finalMsg,
			Usage:      usage,
			StopReason: stopReason,
		})
	}()

	return stream
}

// CapturedRequests returns all captured LLM requests for test assertions.
func (s *ScriptedLLM) CapturedRequests() []CapturedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]CapturedRequest, len(s.requests))
	copy(result, s.requests)
	return result
}

// CallCount returns the number of LLM calls made so far.
func (s *ScriptedLLM) CallCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.requests)
}