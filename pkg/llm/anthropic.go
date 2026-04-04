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

const defaultAnthropicMaxTokens = 65536

// StreamAnthropic streams a completion from Anthropic Messages API.
func StreamAnthropic(
	ctx context.Context,
	model Model,
	llmCtx LLMContext,
	apiKey string,
	chunkIntervalTimeout time.Duration, // Timeout between chunks
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

		// Get API key from environment if not provided
		if apiKey == "" {
			apiKey = os.Getenv("ZAI_API_KEY")
		}
		if apiKey == "" {
			stream.Push(LLMErrorEvent{Error: fmt.Errorf("ZAI_API_KEY not set")})
			return
		}

		// Build request body for Anthropic Messages API
		reqBody := buildAnthropicRequest(model, llmCtx)

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

		// Build URL - Anthropic Messages API uses /v1/messages
		url := strings.TrimSuffix(model.BaseURL, "/") + "/v1/messages"

		// Create request
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonBody))
		if err != nil {
			stream.Push(LLMErrorEvent{Error: err})
			return
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+apiKey)
		req.Header.Set("anthropic-version", "2023-06-01")

// Execute request
	client := &http.Client{
			Timeout: 2 * time.Minute, // Timeout for waiting for response headers + body
		}
		resp, err := client.Do(req)
		if err != nil {
			if strings.Contains(err.Error(), "no such host") {
				stream.Push(LLMErrorEvent{Error: fmt.Errorf("DNS error: cannot resolve API host '%s'.\n\nPossible solutions:\n  1. Check your API configuration\n  2. Verify network connection and VPN settings", model.BaseURL)})
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
			stream.Push(LLMErrorEvent{Error: fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))})
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
			// Each scan should complete within chunkIntervalTimeout
			dl.SetReadDeadline(time.Now().Add(chunkIntervalTimeout))
		}

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
		chunkIndex := 0
		lastUsage := Usage{}

		for scanner.Scan() {
			// Update read deadline for each chunk
			if dl, ok := resp.Body.(deadliner); ok && chunkIntervalTimeout > 0 {
				dl.SetReadDeadline(time.Now().Add(chunkIntervalTimeout))
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
				finalMsg := partial.ToLLMMessage()
				stream.Push(LLMDoneEvent{
					Message:    &finalMsg,
					Usage:      lastUsage,
					StopReason: "stop",
				})
				return
			}

			// Parse JSON chunk
			var event struct {
				Type string `json:"type"`
			}
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				continue
			}

			switch event.Type {
			case "message_start":
				var msgEvent struct {
					Message struct {
						Usage Usage `json:"usage"`
					} `json:"message"`
				}
				if err := json.Unmarshal([]byte(data), &msgEvent); err == nil {
					lastUsage = msgEvent.Message.Usage
				}

			case "content_block_start":
				var blockEvent struct {
					Index        int `json:"index"`
					ContentBlock struct {
						Type string `json:"type"`
						ID   string `json:"id,omitempty"`
						Name string `json:"name,omitempty"`
					} `json:"content_block"`
				}
				if err := json.Unmarshal([]byte(data), &blockEvent); err == nil {
					if blockEvent.ContentBlock.Type == "tool_use" {
						// Create tool call with ID and name
						tc := &ToolCall{
							ID:   blockEvent.ContentBlock.ID,
							Type: "tool_use",
							Function: FunctionCall{
								Name: blockEvent.ContentBlock.Name,
							},
						}
						partial.AppendToolCall(blockEvent.Index, tc)
						// Send delta event so loop.go can capture ID and name
						stream.Push(LLMToolCallDeltaEvent{
							Index:    blockEvent.Index,
							ToolCall: tc,
						})
					}
				}

			case "content_block_delta":
				var deltaEvent struct {
					Index int `json:"index"`
					Delta struct {
						Type        string `json:"type"`
						Text        string `json:"text,omitempty"`
						PartialJSON string `json:"partial_json,omitempty"`
						Thinking    string `json:"thinking,omitempty"`
						Signature   string `json:"signature,omitempty"`
					} `json:"delta"`
				}
				if err := json.Unmarshal([]byte(data), &deltaEvent); err == nil {
					switch deltaEvent.Delta.Type {
					case "text_delta":
						partial.AppendText(deltaEvent.Delta.Text)
						stream.Push(LLMTextDeltaEvent{Delta: deltaEvent.Delta.Text})
					case "thinking_delta":
						partial.AppendThinking(deltaEvent.Delta.Thinking)
						stream.Push(LLMThinkingDeltaEvent{Delta: deltaEvent.Delta.Thinking})
					case "input_json_delta":
						// MiniMax returns XML-tag-style parameters in partial_json
						// Format: {"properties": "{\"   then   "path\">value"}"   then   "}"}
						// We need to convert this to standard JSON
						rawJSON := deltaEvent.Delta.PartialJSON

						// Try to detect and fix XML-tag-style format
						if strings.Contains(rawJSON, "\">") {
							convertedJSON := normalizeMiniMaxArguments(rawJSON)
							if convertedJSON != "" {
								rawJSON = convertedJSON
							}
						}

						// Create tool call with this chunk
						tc := &ToolCall{
							Function: FunctionCall{
								Arguments: rawJSON,
							},
						}
						partial.AppendToolCall(deltaEvent.Index, tc)

						// Send delta event
						partial.mu.Lock()
						if existingTC, ok := partial.ToolCalls[deltaEvent.Index]; ok {
							stream.Push(LLMToolCallDeltaEvent{
								Index:    deltaEvent.Index,
								ToolCall: existingTC,
							})
						}
						partial.mu.Unlock()
					}
				}

			case "message_delta":
				var deltaEvent struct {
					Delta struct {
						StopReason string `json:"stop_reason,omitempty"`
					} `json:"delta"`
					Usage Usage `json:"usage"`
				}
				if err := json.Unmarshal([]byte(data), &deltaEvent); err == nil {
					if deltaEvent.Usage.InputTokens > 0 {
						lastUsage = deltaEvent.Usage
					}
					if deltaEvent.Delta.StopReason != "" {
						finalMsg := partial.ToLLMMessage()
						stopReason := mapAnthropicStopReason(deltaEvent.Delta.StopReason)
						stream.Push(LLMDoneEvent{
							Message:    &finalMsg,
							Usage:      lastUsage,
							StopReason: stopReason,
						})
						return
					}
				}

			case "message_stop":
				finalMsg := partial.ToLLMMessage()
				stream.Push(LLMDoneEvent{
					Message:    &finalMsg,
					Usage:      lastUsage,
					StopReason: "stop",
				})
				return

			case "error":
				var errorEvent struct {
					Error struct {
						Type    string `json:"type"`
						Message string `json:"message"`
					} `json:"error"`
				}
				if err := json.Unmarshal([]byte(data), &errorEvent); err == nil {
					stream.Push(LLMErrorEvent{Error: fmt.Errorf("API error: %s: %s", errorEvent.Error.Type, errorEvent.Error.Message)})
					return
				}
			}
		}

		if err := scanner.Err(); err != nil {
			stream.Push(LLMErrorEvent{Error: err})
			return
		}
	}()

	return stream
}

// buildAnthropicRequest builds the request body for Anthropic Messages API
// Reference: https://github.com/badlogic/pi-mono/blob/main/packages/ai/src/providers/anthropic.ts
func buildAnthropicRequest(model Model, llmCtx LLMContext) map[string]any {
	// Convert system prompt to system blocks
	systemBlocks := []map[string]any{}
	if llmCtx.SystemPrompt != "" {
		systemBlocks = append(systemBlocks, map[string]any{
			"type": "text",
			"text": llmCtx.SystemPrompt,
		})
	}

	// Convert messages to Anthropic format
	// KEY: All consecutive tool_result messages are collected into ONE user message
	messages := []map[string]any{}
	i := 0

	for i < len(llmCtx.Messages) {
		msg := llmCtx.Messages[i]

		if msg.Role == "system" {
			// Add system prompts to system blocks
			systemBlocks = append(systemBlocks, map[string]any{
				"type": "text",
				"text": msg.Content,
			})
			i++
		} else if msg.Role == "user" {
			// Regular user message
			content := msg.Content
			if content == "" {
				content = "..." // Placeholder for empty content
			}
			messages = append(messages, map[string]any{
				"role":    "user",
				"content": content,
			})
			i++
		} else if msg.Role == "assistant" {
			// Assistant message with optional tool calls
			content := []map[string]any{}

			// Add text content if present
			if msg.Content != "" {
				content = append(content, map[string]any{
					"type": "text",
					"text": msg.Content,
				})
			}

			// Add tool calls
			for _, tc := range msg.ToolCalls {
				// Parse arguments from JSON string to object
				var argsObj map[string]any
				if tc.Function.Arguments != "" {
					// MiniMax returns nested JSON: {"properties": "{\"command\": \"...\"}"}
					// Try to parse the outer JSON
					var outerObj map[string]any
					if err := json.Unmarshal([]byte(tc.Function.Arguments), &outerObj); err == nil {
						// Check if it has "properties" field with a JSON string value
						if props, ok := outerObj["properties"].(string); ok {
							// Parse the inner JSON string
							if err := json.Unmarshal([]byte(props), &argsObj); err != nil {
								// If inner parsing fails, try XML-tag style
								if strings.Contains(props, "\">") {
									argsObj = parseXMLTagStyleArguments(tc.Function.Arguments)
								} else {
									argsObj = make(map[string]any)
								}
							}
						} else {
							argsObj = outerObj
						}
					} else {
						argsObj = make(map[string]any)
					}
				} else {
					argsObj = make(map[string]any)
				}

				content = append(content, map[string]any{
					"type":  "tool_use",
					"id":    tc.ID,
					"name":  tc.Function.Name,
					"input": argsObj,
				})
			}

			messages = append(messages, map[string]any{
				"role":    "assistant",
				"content": content,
			})
			i++
		} else if msg.Role == "tool" || msg.Role == "toolResult" {
			// Collect ALL consecutive tool_result messages into ONE user message
			// This is KEY for Anthropic API compatibility
			// Note: ConvertMessagesToLLM changes "toolResult" to "tool"
			toolResults := []map[string]any{}

			// Add current tool result (LLMMessage doesn't have IsError, default to false)
			toolResults = append(toolResults, map[string]any{
				"type":        "tool_result",
				"tool_use_id": msg.ToolCallID,
				"content":     convertToolResultContent(msg),
				"is_error":    false,
			})

			// Look ahead for more consecutive tool_result messages
			j := i + 1
			for j < len(llmCtx.Messages) && llmCtx.Messages[j].Role == "toolResult" {
				nextMsg := llmCtx.Messages[j]
				toolResults = append(toolResults, map[string]any{
					"type":        "tool_result",
					"tool_use_id": nextMsg.ToolCallID,
					"content":     convertToolResultContent(nextMsg),
					"is_error":    false,
				})
				j++
			}

			// Add single user message with all tool results
			messages = append(messages, map[string]any{
				"role":    "user",
				"content": toolResults,
			})

			// Skip all the tool_result messages we just processed
			i = j
		} else {
			i++
		}
	}

	reqBody := map[string]any{
		"model":      model.ID,
		"messages":   messages,
		"max_tokens": resolveAnthropicMaxTokens(model),
		"stream":     true,
	}

	if len(systemBlocks) > 0 {
		reqBody["system"] = systemBlocks
	}

	if len(llmCtx.Tools) > 0 {
		tools := []map[string]any{}
		for _, tool := range llmCtx.Tools {
			inputSchema := normalizeAnthropicInputSchema(tool.Function.Parameters)

			tools = append(tools, map[string]any{
				"name":         tool.Function.Name,
				"description":  tool.Function.Description,
				"input_schema": inputSchema,
			})
		}
		reqBody["tools"] = tools
		reqBody["tool_choice"] = map[string]any{
			"type": "auto",
		}
	}

	return reqBody
}

func resolveAnthropicMaxTokens(model Model) int {
	if model.MaxTokens > 0 {
		return model.MaxTokens
	}
	return defaultAnthropicMaxTokens
}

func normalizeAnthropicInputSchema(params map[string]any) map[string]any {
	if params == nil {
		return map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		}
	}

	if _, ok := params["type"]; ok {
		return params
	}
	if _, ok := params["properties"]; ok {
		return params
	}
	if _, ok := params["required"]; ok {
		return params
	}
	if _, ok := params["additionalProperties"]; ok {
		return params
	}

	return map[string]any{
		"type":       "object",
		"properties": params,
	}
}

// convertToolResultContent converts tool result content to Anthropic format
// Returns string for simple content, or array for complex content
func convertToolResultContent(msg LLMMessage) any {
	// If ContentParts exist, convert to array
	if len(msg.ContentParts) > 0 {
		blocks := make([]map[string]any, 0, len(msg.ContentParts))
		for _, part := range msg.ContentParts {
			if part.Type == "text" {
				blocks = append(blocks, map[string]any{
					"type": "text",
					"text": part.Text,
				})
			}
			// Handle image parts if needed
		}
		if len(blocks) == 1 {
			// Single text block, return as string
			if text, ok := blocks[0]["text"].(string); ok {
				return text
			}
		}
		return blocks
	}

	// Otherwise return content string
	return msg.Content
}

// mapAnthropicStopReason maps Anthropic stop reasons to our format
func mapAnthropicStopReason(reason string) string {
	switch reason {
	case "end_turn":
		return "stop"
	case "max_tokens":
		return "length"
	case "tool_use":
		return "toolUse"
	case "stop_sequence":
		return "stop"
	default:
		return reason
	}
}

// normalizeMiniMaxArguments converts MiniMax's XML-tag style partial JSON to standard JSON
// MiniMax sends chunks like: {"properties": "{\" followed by "param">value" followed by "}"}
// This converts each chunk to valid JSON that can be accumulated
func normalizeMiniMaxArguments(rawJSON string) string {
	trimmed := strings.TrimSpace(rawJSON)

	// Handle the starting chunk: {"properties": "{"
	if trimmed == `{"properties": "{\"` || trimmed == `{"properties": "{\""` {
		return "{" // Start of standard JSON object
	}

	// Handle the ending chunk: }" or }}"}
	if trimmed == "\"}" || trimmed == "}\"" {
		return "}"
	}
	if trimmed == "}" || trimmed == "\"}}}" {
		return "}"
	}

	// Handle middle chunks with "param">value" pattern
	// Pattern: "param">value or "param">value",
	// Also handle comma-delimited: "param1">value1","param2">value2
	if strings.Contains(trimmed, "\">") {
		// Check if we have multiple comma-delimited params
		if strings.Contains(trimmed, `","`) {
			// Split by comma and convert each part (WITHOUT trailing commas)
			parts := strings.Split(trimmed, `","`)
			var results []string
			for i, part := range parts {
				part = strings.TrimSpace(part)
				if strings.Contains(part, `">`) {
					converted := convertSingleParamChunkNoComma(part)
					if converted != "" {
						results = append(results, converted)
					}
				} else if i == 0 {
					// First part without ">" might be a continuation
					results = append(results, part)
				}
			}
			if len(results) > 0 {
				return strings.Join(results, ", ")
			}
		} else {
			// Single param chunk - add trailing comma
			return convertSingleParamChunk(trimmed)
		}
	}

	// Handle continuation chunks (just values or partial JSON)
	// If it looks like a value continuation, just return as-is
	// This handles cases where the value spans multiple chunks
	if !strings.HasPrefix(trimmed, `"`) && !strings.HasPrefix(trimmed, `{`) {
		return trimmed
	}

	// Default: return as-is (might be valid JSON already)
	return rawJSON
}

// convertSingleParamChunk converts a single "param">value chunk to JSON WITH trailing comma
func convertSingleParamChunk(chunk string) string {
	result := convertSingleParamChunkNoComma(chunk)
	if result != "" {
		return result + ", "
	}
	return result
}

// convertSingleParamChunkNoComma converts a single "param">value chunk to JSON WITHOUT trailing comma
func convertSingleParamChunkNoComma(chunk string) string {
	chunk = strings.TrimSpace(chunk)
	if !strings.Contains(chunk, `">`) {
		return chunk
	}

	// Split on ">"
	parts := strings.SplitN(chunk, `">`, 2)
	if len(parts) == 2 {
		key := strings.Trim(parts[0], `"`)
		value := parts[1]
		// Remove trailing markers from value:
		// - "}" -> remove both (MiniMax format ending)
		// - " -> remove only the quote (value ending)
		// - } -> remove only the brace (MiniMax format ending)
		if strings.HasSuffix(value, `"}`) {
			value = value[:len(value)-2]
		} else if strings.HasSuffix(value, `"`) {
			value = value[:len(value)-1]
		} else if strings.HasSuffix(value, `}`) {
			value = value[:len(value)-1]
		}
		// Return standard JSON format
		return fmt.Sprintf(`"%s": "%s"`, key, value)
	}

	return chunk
}

// parseXMLTagStyleArguments parses MiniMax's XML-tag style arguments
// Format: {"properties": "{\"key\">value}"}
// This converts XML-tag format to standard JSON object
func parseXMLTagStyleArguments(args string) map[string]any {
	result := make(map[string]any)
	// Remove outer {"properties": "} wrapper
	trimmed := strings.TrimSpace(args)
	if strings.HasPrefix(trimmed, `{"properties": "`) {
		inner := strings.TrimPrefix(trimmed, `{"properties": "`)
		inner = strings.TrimSuffix(inner, `"}`)
		// Now inner should be like: "{\"key\">value"
		// Remove escaped quotes
		inner = strings.ReplaceAll(inner, `\"`, `"`)
		// Now it should be: {"key">value
		// Parse the XML-tag style format
		// Format: {"key1">value1}" or {"key1">value1", "key2">value2}
		// We need to find each "key">value pattern
		parts := strings.Split(inner, `","`)
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if idx := strings.Index(part, `">`); idx > 0 {
				key := part[1:idx] // Remove leading quote
				value := part[idx+1:]
				// Remove trailing quote if present
				if len(value) > 0 && value[len(value)-1] == '"' {
					value = value[:len(value)-1]
				}
				result[key] = value
			}
		}
	}
	return result
}

func parseRetryAfterHeaderAnthropic(value string) time.Duration {
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
