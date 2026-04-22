package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tiancaiamao/ai/pkg/traceevent"
)

// StreamLLM streams a completion from LLM.
func StreamLLM(
	ctx context.Context,
	model Model,
	llmCtx LLMContext,
	apiKey string,
	chunkIntervalTimeout time.Duration, // Timeout between chunks (e.g., 2min)
) *EventStream[LLMEvent, LLMMessage] {
	// Route to Anthropic API if requested
	if model.API == "anthropic-messages" {
		return StreamAnthropic(ctx, model, llmCtx, apiKey, chunkIntervalTimeout)
	}

	stream := NewEventStream[LLMEvent, LLMMessage](
		func(e LLMEvent) bool {
			return e.GetEventType() == "done" || e.GetEventType() == "error"
		},
		func(e LLMEvent) LLMMessage {
			if done, ok := e.(LLMDoneEvent); ok && done.Message != nil {
				return *done.Message
			}
			return LLMMessage{}
		},
	)

	go func() {
		defer stream.End(LLMMessage{})

		// Get API key from environment if not provided
		if apiKey == "" {
			apiKey = os.Getenv("ZAI_API_KEY")
		}
		if apiKey == "" {
			stream.Push(LLMErrorEvent{Error: fmt.Errorf("ZAI_API_KEY not set")})
			return
		}

		// Build messages array
		messages := llmCtx.Messages

		// Prepend system prompt as first message if provided
		if llmCtx.SystemPrompt != "" {
			systemMsg := LLMMessage{
				Role:    "system",
				Content: llmCtx.SystemPrompt,
			}
			messages = append([]LLMMessage{systemMsg}, llmCtx.Messages...)
		}

		// Build request body
		reqBody := map[string]any{
			"model":    model.ID,
			"messages": messages,
			"stream":   true,
		}

		if len(llmCtx.Tools) > 0 {
			reqBody["tools"] = llmCtx.Tools
			reqBody["tool_choice"] = "auto"
		}

		jsonBody, err := json.Marshal(reqBody)
		if err != nil {
			stream.Push(LLMErrorEvent{Error: err})
			return
		}
		traceevent.Log(ctx, traceevent.CategoryLLM, "llm_request_json",
			traceevent.Field{Key: "model", Value: model.ID},
			traceevent.Field{Key: "provider", Value: model.Provider},
			traceevent.Field{Key: "api", Value: model.API},
			traceevent.Field{Key: "bytes", Value: len(jsonBody)},
			traceevent.Field{Key: "json", Value: string(jsonBody)},
		)

		// Build URL
		url := model.BaseURL + "/chat/completions"

		// Create request
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonBody))
		if err != nil {
			stream.Push(LLMErrorEvent{Error: err})
			return
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+apiKey)

		// Execute request — derive total timeout from context deadline so the
		// HTTP client enforces a hard ceiling even when SetReadDeadline is
		// refreshed per-chunk (which can otherwise bypass the context deadline).
		client := &http.Client{}
		if deadline, ok := ctx.Deadline(); ok {
			remaining := time.Until(deadline)
			if remaining > 0 {
				client.Timeout = remaining
			}
		}
		resp, err := client.Do(req)
		if err != nil {
			// Provide helpful error message for DNS/connection issues
			if strings.Contains(err.Error(), "no such host") {
				stream.Push(LLMErrorEvent{Error: fmt.Errorf("DNS error: cannot resolve API host '%s'.\n\nPossible solutions:\n  1. Check your ZAI_BASE_URL environment variable\n  2. Try standard OpenAI API: export ZAI_BASE_URL=https://api.openai.com/v1\n  3. Verify network connection and VPN settings", model.BaseURL)})
			} else {
				stream.Push(LLMErrorEvent{Error: fmt.Errorf("connection error: %w", err)})
			}
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			traceevent.Log(ctx, traceevent.CategoryLLM, "llm_response_json",
				traceevent.Field{Key: "status_code", Value: resp.StatusCode},
				traceevent.Field{Key: "http_error", Value: true},
				traceevent.Field{Key: "json", Value: string(body)},
			)
			retryAfter := parseRetryAfterHeader(resp.Header.Get("Retry-After"))
			stream.Push(LLMErrorEvent{Error: ClassifyAPIErrorWithRetryAfter(resp.StatusCode, string(body), retryAfter)})
			return
		}

		// Parse SSE stream
		partial := NewPartialMessage()
		stream.Push(LLMStartEvent{Partial: partial})

		// Set read deadline for chunk interval timeout
		type deadliner interface {
			SetReadDeadline(time.Time) error
		}
		if dl, ok := resp.Body.(deadliner); ok && chunkIntervalTimeout > 0 {
			// Each scan should complete within chunkIntervalTimeout, but never
			// extend beyond the context deadline (which acts as the hard total limit).
			nextDeadline := time.Now().Add(chunkIntervalTimeout)
			if ctxDeadline, ok := ctx.Deadline(); ok && nextDeadline.After(ctxDeadline) {
				nextDeadline = ctxDeadline
			}
			dl.SetReadDeadline(nextDeadline)
		}

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
		chunkIndex := 0
		lastUsage := Usage{}

		for scanner.Scan() {
			// Update read deadline for each chunk, capped by context deadline.
			if dl, ok := resp.Body.(deadliner); ok && chunkIntervalTimeout > 0 {
				nextDeadline := time.Now().Add(chunkIntervalTimeout)
				if ctxDeadline, ok := ctx.Deadline(); ok && nextDeadline.After(ctxDeadline) {
					nextDeadline = ctxDeadline
				}
				dl.SetReadDeadline(nextDeadline)
			}

			// Check parent context cancellation
			select {
			case <-ctx.Done():
				stream.Push(LLMErrorEvent{Error: ctx.Err()})
				return
			default:
			}

			line := scanner.Text()

			// SSE format: "data: {...}"
			if !strings.HasPrefix(line, "data: ") {
				continue
			}

			data := strings.TrimPrefix(line, "data: ")
			traceevent.Log(ctx, traceevent.CategoryLLM, "llm_response_json",
				traceevent.Field{Key: "chunk_index", Value: chunkIndex},
				traceevent.Field{Key: "json", Value: data},
			)
			chunkIndex++
			if data == "[DONE]" {
				// Some providers may terminate with [DONE] without emitting a final
				// chunk that includes finish_reason. Emit a synthetic done event so
				// upper layers can continue the loop instead of ending silently.
				finalMsg := partial.ToLLMMessage()
				stream.Push(LLMDoneEvent{
					Message:    &finalMsg,
					Usage:      lastUsage,
					StopReason: "stop",
				})
				return
			}

			// Parse JSON chunk
			var chunk struct {
				Choices []struct {
					Delta struct {
						Content          string            `json:"content,omitempty"`
						ReasoningContent string            `json:"reasoning_content,omitempty"`
						Thinking         string            `json:"thinking,omitempty"`
						ToolCalls        []json.RawMessage `json:"tool_calls,omitempty"`
					} `json:"delta"`
					FinishReason *string `json:"finish_reason"`
				} `json:"choices"`
				Usage *Usage `json:"usage"`
				Error *struct {
					Message string `json:"message,omitempty"`
					Type    string `json:"type,omitempty"`
				} `json:"error,omitempty"`
			}

			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				continue
			}

			if chunk.Error != nil {
				msg := strings.TrimSpace(chunk.Error.Message)
				if msg == "" {
					msg = strings.TrimSpace(chunk.Error.Type)
				}
				stream.Push(LLMErrorEvent{Error: ClassifyAPIError(resp.StatusCode, msg)})
				return
			}

			if len(chunk.Choices) == 0 {
				continue
			}

			choice := chunk.Choices[0]
			if chunk.Usage != nil {
				lastUsage = *chunk.Usage
			}

			// Text delta
			if choice.Delta.Content != "" {
				partial.AppendText(choice.Delta.Content)
				stream.Push(LLMTextDeltaEvent{Delta: choice.Delta.Content})
			}

			// Reasoning delta (Z.AI uses reasoning_content)
			if choice.Delta.ReasoningContent != "" {
				partial.AppendThinking(choice.Delta.ReasoningContent)
				stream.Push(LLMThinkingDeltaEvent{Delta: choice.Delta.ReasoningContent})
			}

			// Thinking delta
			if choice.Delta.Thinking != "" {
				partial.AppendThinking(choice.Delta.Thinking)
				stream.Push(LLMThinkingDeltaEvent{Delta: choice.Delta.Thinking})
			}

			// Tool calls
			if len(choice.Delta.ToolCalls) > 0 {
				for _, tcRaw := range choice.Delta.ToolCalls {
					var tcDelta struct {
						Index    int    `json:"index"`
						ID       string `json:"id,omitempty"`
						Type     string `json:"type,omitempty"`
						Function struct {
							Name      string `json:"name,omitempty"`
							Arguments string `json:"arguments,omitempty"`
						} `json:"function,omitempty"`
					}

					if err := json.Unmarshal(tcRaw, &tcDelta); err != nil {
						continue
					}

					toolCall := &ToolCall{
						ID:   tcDelta.ID,
						Type: tcDelta.Type,
						Function: FunctionCall{
							Name:      tcDelta.Function.Name,
							Arguments: tcDelta.Function.Arguments,
						},
					}

					partial.AppendToolCall(tcDelta.Index, toolCall)
					stream.Push(LLMToolCallDeltaEvent{Index: tcDelta.Index, ToolCall: toolCall})
				}
			}

			// Finish
			if choice.FinishReason != nil {
				finalMsg := partial.ToLLMMessage()
				usage := lastUsage

				stream.Push(LLMDoneEvent{
					Message:    &finalMsg,
					Usage:      usage,
					StopReason: *choice.FinishReason,
				})
				return
			}
		}

		if err := scanner.Err(); err != nil {
			stream.Push(LLMErrorEvent{Error: fmt.Errorf("LLM stream read error: %w", err)})
			return
		}

		// Clean EOF but no SSE data chunks were received — the server closed
		// the connection prematurely without sending any response data.
		if chunkIndex == 0 {
			stream.Push(LLMErrorEvent{Error: fmt.Errorf("LLM stream ended without any data chunks (server closed connection prematurely)")})
			return
		}

		// Clean EOF with some chunks but no DoneEvent was ever pushed. This
		// means the stream was truncated before the finish_reason was sent.
		finalMsg := partial.ToLLMMessage()
		stream.Push(LLMDoneEvent{
			Message:    &finalMsg,
			Usage:      lastUsage,
			StopReason: "stop",
		})
	}()

	return stream
}

func parseRetryAfterHeader(value string) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil {
		d := time.Until(at)
		if d > 0 {
			return d
		}
	}
	return 0
}
