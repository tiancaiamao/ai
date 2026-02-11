package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
)

// StreamLLM streams a completion from the LLM.
func StreamLLM(
	ctx context.Context,
	model Model,
	llmCtx LLMContext,
	apiKey string,
) *EventStream[LLMEvent, LLMMessage] {
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

		debugEnabled := slog.Default().Enabled(ctx, slog.LevelDebug)
		logChunks := debugEnabled && os.Getenv("AI_LOG_LLM_CHUNKS") == "1"
		logRequests := debugEnabled
		if val := strings.TrimSpace(os.Getenv("AI_LOG_LLM_REQUESTS")); val != "" {
			logRequests = val == "1"
		}

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
		if logRequests {
			slog.Debug("[LLM] request", "model", model.ID, "provider", model.Provider, "bytes", len(jsonBody), "json", truncateLine(string(jsonBody), 65536))
		}

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

		// Execute request
		client := &http.Client{}
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
			stream.Push(LLMErrorEvent{Error: ClassifyAPIError(resp.StatusCode, string(body))})
			return
		}

		// Parse SSE stream
		partial := NewPartialMessage()
		stream.Push(LLMStartEvent{Partial: partial})

		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()

			// SSE format: "data: {...}"
			if !strings.HasPrefix(line, "data: ") {
				continue
			}

			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				break
			}
			if logChunks {
				slog.Debug("[LLM] stream chunk", "json", truncateLine(data, 8192), "bytes", len(data))
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
				usage := Usage{}
				if chunk.Usage != nil {
					usage = *chunk.Usage
				}

				stream.Push(LLMDoneEvent{
					Message:    &finalMsg,
					Usage:      usage,
					StopReason: *choice.FinishReason,
				})
				return
			}
		}

		if err := scanner.Err(); err != nil {
			stream.Push(LLMErrorEvent{Error: err})
			return
		}
	}()

	return stream
}

func truncateLine(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	if limit <= 3 {
		return value[:limit]
	}
	return value[:limit-3] + "..."
}
