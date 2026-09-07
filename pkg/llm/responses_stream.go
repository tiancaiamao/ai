package llm

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// processResponsesSSE parses the shared Responses API event stream. Providers
// may use different authentication, endpoints, and request bodies, but the
// response event protocol is the same.
func processResponsesSSE(ctx context.Context, body io.Reader, stream *EventStream[LLMEvent, LLMMessage], chunkIntervalTimeout time.Duration) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	type deadliner interface {
		SetReadDeadline(time.Time) error
	}
	setReadDeadline := func() {
		if dl, ok := body.(deadliner); ok && chunkIntervalTimeout > 0 {
			next := time.Now().Add(chunkIntervalTimeout)
			if deadline, ok := ctx.Deadline(); ok && next.After(deadline) {
				next = deadline
			}
			_ = dl.SetReadDeadline(next)
		}
	}
	setReadDeadline()

	parser := newResponsesParser()
	stream.Push(LLMStartEvent{Partial: NewPartialMessage()})
	sawData := false
	terminal := false
	var lastEventType string

	for scanner.Scan() {
		setReadDeadline()
		select {
		case <-ctx.Done():
			stream.Push(LLMErrorEvent{Error: ctx.Err()})
			return
		default:
		}

		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, ":") || !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		sawData = true
		if data == "[DONE]" {
			terminal = true
			break
		}

		var chunk responsesEventChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			stream.Push(LLMErrorEvent{Error: fmt.Errorf("invalid Responses SSE event (last_event=%q): %w", lastEventType, err)})
			return
		}
		if chunk.Type == "" {
			stream.Push(LLMErrorEvent{Error: fmt.Errorf("Responses SSE event missing type (payload=%q)", truncateSSEPayload(data))})
			return
		}
		lastEventType = chunk.Type
		if chunk.Type == "response.failed" && chunk.Response != nil && chunk.Response.Error != nil {
			failure := chunk.Response.Error
			code := strings.TrimSpace(failure.Code)
			message := strings.TrimSpace(failure.Message)
			if code == "" {
				code = "unknown"
			}
			if message == "" {
				message = "no message"
			}
			stream.Push(LLMErrorEvent{Error: fmt.Errorf("Responses response.failed (%s): %s", code, message)})
			return
		}

		switch chunk.Type {
		case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			stream.Push(LLMThinkingDeltaEvent{Delta: chunk.Delta, Index: chunk.OutputIndex})
		case "response.reasoning_summary_part.done":
			stream.Push(LLMThinkingDeltaEvent{Delta: "\n\n", Index: chunk.OutputIndex})
		case "response.output_text.delta", "response.refusal.delta":
			stream.Push(LLMTextDeltaEvent{Delta: chunk.Delta, Index: chunk.OutputIndex})
		case "response.function_call_arguments.delta":
			stream.Push(LLMToolCallDeltaEvent{
				Index:    chunk.OutputIndex,
				ToolCall: &ToolCall{Type: "function", Function: FunctionCall{Arguments: chunk.Delta}},
			})
		case "response.output_item.added":
			if chunk.Item != nil && chunk.Item.Type == "function_call" {
				stream.Push(LLMToolCallDeltaEvent{
					Index: chunk.OutputIndex,
					ToolCall: &ToolCall{
						ID: chunk.Item.CallID, Type: "function",
						Function: FunctionCall{Name: chunk.Item.Name},
					},
				})
			}
		}

		stopReason, err := parser.handle(chunk)
		if err != nil {
			stream.Push(LLMErrorEvent{Error: err})
			return
		}
		if stopReason == "" {
			continue
		}

		msg := parser.buildMessage()
		if len(msg.ToolCalls) > 0 && stopReason == "stop" {
			stopReason = "tool_calls"
		}
		stream.Push(LLMDoneEvent{
			Message:    &msg,
			Usage:      extractResponsesUsage(chunk),
			StopReason: stopReason,
		})
		return
	}

	if err := scanner.Err(); err != nil {
		stream.Push(LLMErrorEvent{Error: fmt.Errorf("error reading Responses stream (last_event=%q): %w", lastEventType, err)})
		return
	}
	if !terminal {
		reason := "stream ended before completion"
		if !sawData {
			reason = "stream ended without any data events"
		}
		stream.Push(LLMErrorEvent{Error: fmt.Errorf("Responses %s (last_event=%q)", reason, lastEventType)})
		return
	}

	msg := parser.buildMessage()
	stream.Push(LLMDoneEvent{Message: &msg, StopReason: "stop"})
}

func truncateSSEPayload(payload string) string {
	const max = 512
	payload = strings.TrimSpace(payload)
	if len(payload) <= max {
		return payload
	}
	return payload[:max] + "..."
}
