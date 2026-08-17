package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/tiancaiamao/ai/pkg/traceevent"
	"golang.org/x/net/proxy"
)

// StreamOpenAIResponses streams a completion from OpenAI Responses API.
func StreamOpenAIResponses(
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
		defer func() {
			if r := recover(); r != nil {
				stream.Push(LLMErrorEvent{Error: fmt.Errorf("LLM stream panic (recovered): %v", r)})
			}
		}()

		// Get API key from environment if not provided
		if apiKey == "" {
			apiKey = os.Getenv("ZAI_API_KEY")
		}
		if apiKey == "" {
			stream.Push(LLMErrorEvent{Error: fmt.Errorf("ZAI_API_KEY not set")})
			return
		}

		// Build request body for OpenAI Responses API
		reqBody := buildOpenAIResponsesRequest(model, llmCtx)

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

		// Build URL for OpenAI Responses API
		// If the base URL already includes "/responses", use it directly.
		// Otherwise, append "/responses" to the base URL.
		url := model.BaseURL
		if !strings.HasSuffix(url, "/responses") {
			url = strings.TrimSuffix(url, "/") + "/responses"
		}

		// Create request
		req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(jsonBody))
		if err != nil {
			stream.Push(LLMErrorEvent{Error: err})
			return
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+apiKey)

		// Execute request — derive total timeout from context deadline so the HTTP client enforces
		// a hard ceiling even when SetReadDeadline is refreshed per-chunk.
		client := &http.Client{}
		if deadline, ok := ctx.Deadline(); ok {
			remaining := time.Until(deadline)
			if remaining > 0 {
				client.Timeout = remaining
			}
		}

		// Support SOCKS5 proxy via ALL_PROXY or HTTPS_PROXY environment variable
		// Example: ALL_PROXY=socks5://127.0.0.1:1180
		// Uses golang.org/x/net/proxy for proper SOCKS5 support
		if proxyURL := os.Getenv("ALL_PROXY"); proxyURL != "" {
			if socks5URL, err := parseProxyURL(proxyURL); err == nil && socks5URL.Scheme == "socks5" {
				dialer, err := proxy.SOCKS5("tcp", socks5URL.Host, nil, nil)
				if err == nil {
					transport := &http.Transport{
						DialContext: dialer.(proxy.ContextDialer).DialContext,
					}
					client.Transport = transport
				}
			} else if err == nil {
				// Non-SOCKS5 proxy (HTTP/HTTPS)
				transport := &http.Transport{
					Proxy: http.ProxyURL(socks5URL),
				}
				client.Transport = transport
			}
		} else if proxyURL := os.Getenv("HTTPS_PROXY"); proxyURL != "" {
			if socks5URL, err := parseProxyURL(proxyURL); err == nil && socks5URL.Scheme == "socks5" {
				dialer, err := proxy.SOCKS5("tcp", socks5URL.Host, nil, nil)
				if err == nil {
					transport := &http.Transport{
						DialContext: dialer.(proxy.ContextDialer).DialContext,
					}
					client.Transport = transport
				}
			} else if err == nil {
				// Non-SOCKS5 proxy (HTTP/HTTPS)
				transport := &http.Transport{
					Proxy: http.ProxyURL(socks5URL),
				}
				client.Transport = transport
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
			stream.Push(LLMErrorEvent{Error: fmt.Errorf("API error (%d): %s", resp.StatusCode, string(body))})
			return
		}

		// Parse SSE stream
		scanner := bufio.NewScanner(resp.Body)
		// Increase buffer size for large responses (default 64KB, max 1MB)
		const maxTokenSize = 1024 * 1024
		buf := make([]byte, maxTokenSize)
		scanner.Buffer(buf, maxTokenSize)

		var fullContent strings.Builder
		var finishReason string
		var lastChunkTime time.Time

		// Initialize chunk timer
		lastChunkTime = time.Now()

		for scanner.Scan() {
			line := scanner.Text()

			// Check if chunk interval timeout has been exceeded
			if chunkIntervalTimeout > 0 && time.Since(lastChunkTime) > chunkIntervalTimeout {
				stream.Push(LLMErrorEvent{Error: fmt.Errorf("chunk interval timeout: no data received for %v", chunkIntervalTimeout)})
				return
			}

			// Skip empty lines and SSE comments
			if line == "" || strings.HasPrefix(line, ":") {
				continue
			}

			// Parse SSE data line
			if strings.HasPrefix(line, "data: ") {
				data := strings.TrimPrefix(line, "data: ")

				// Parse chunk for OpenAI Responses API format
				var chunk struct {
					Type  string `json:"type"`
					Delta string `json:"delta"`
					Item  struct {
						Content []struct {
							Type string `json:"type"`
							Text string `json:"text"`
						} `json:"content"`
					} `json:"item"`
					Response struct {
						Status string `json:"status"`
					} `json:"response"`
				}

				if err := json.Unmarshal([]byte(data), &chunk); err != nil {
					continue // Skip malformed chunks
				}

				// Handle different event types
				switch chunk.Type {
				case "response.output_text.delta":
					// Accumulate content from delta
					if chunk.Delta != "" {
						fullContent.WriteString(chunk.Delta)
						// Push text delta event for TUI streaming display
						stream.Push(LLMTextDeltaEvent{Delta: chunk.Delta})
					}
				case "response.output_text.done":
					// Final text is in the item's content
					if len(chunk.Item.Content) > 0 {
						fullContent.Reset()
						fullContent.WriteString(chunk.Item.Content[0].Text)
					}
				case "response.completed":
					// Response completed
					finishReason = "stop"
				case "response.incomplete":
					// Response incomplete
					finishReason = "length"
				}

				// Update last chunk time
				lastChunkTime = time.Now()
			}
		}

		if err := scanner.Err(); err != nil {
			stream.Push(LLMErrorEvent{Error: fmt.Errorf("error reading stream: %w", err)})
			return
		}

		// Build final message
		content := fullContent.String()
		msg := LLMMessage{
			Role:    "assistant",
			Content: content,
		}

		// Parse tool calls if present (simplified - full implementation would parse tool_call chunks)
		// For now, we just return the content

		// Set stop reason based on finish_reason
		stopReason := "stop"
		switch finishReason {
		case "stop":
			stopReason = "stop"
		case "length":
			stopReason = "length"
		case "tool_calls":
			stopReason = "tool_use"
		}

		stream.Push(LLMDoneEvent{Message: &msg, StopReason: stopReason})
	}()

	return stream
}

// buildOpenAIResponsesRequest builds the request body for OpenAI Responses API.
func buildOpenAIResponsesRequest(model Model, llmCtx LLMContext) map[string]any {
	// Convert messages to input format for Responses API
	input := make([]map[string]any, 0)

	// Add system prompt if provided
	if llmCtx.SystemPrompt != "" {
		// Use "developer" role for reasoning models, "system" for others
		role := "system"
		if model.Reasoning {
			role = "developer"
		}
		input = append(input, map[string]any{
			"role":    role,
			"content": llmCtx.SystemPrompt,
		})
	}

	// Add conversation messages
	for _, msg := range llmCtx.Messages {
		if msg.Role == "user" {
			// User messages need special format with input_text content
			input = append(input, map[string]any{
				"role": "user",
				"content": []map[string]any{
					{
						"type": "input_text",
						"text": msg.Content,
					},
				},
			})
		} else if msg.Role == "assistant" {
			// Assistant messages can be passed as-is for context
			input = append(input, map[string]any{
				"role":    "assistant",
				"content": msg.Content,
			})
		} else if msg.Role == "tool" {
			// Tool results need special format
			input = append(input, map[string]any{
				"type":    "function_call_output",
				"call_id": msg.ToolCallID,
				"output":  msg.Content,
			})
		}
	}

	reqBody := map[string]any{
		"model":  model.ID,
		"input":  input,
		"stream": true,
		"store":  false,
	}

	// Set max_output_tokens if the model specifies one
	if model.MaxTokens > 0 {
		reqBody["max_output_tokens"] = model.MaxTokens
	}

	// Add tools if present
	// Responses API uses flat tool format: {"type": "function", "name": ..., "parameters": ...}
	// NOT nested: {"type": "function", "function": {"name": ..., "parameters": ...}}
	if len(llmCtx.Tools) > 0 {
		tools := make([]map[string]any, 0, len(llmCtx.Tools))
		for _, tool := range llmCtx.Tools {
			toolMap := map[string]any{
				"type":        "function",
				"name":        tool.Function.Name,
				"description": tool.Function.Description,
			}
			if tool.Function.Parameters != nil {
				toolMap["parameters"] = tool.Function.Parameters
			}
			tools = append(tools, toolMap)
		}
		reqBody["tools"] = tools
	}

	// Add reasoning parameters if the model supports it
	if model.Reasoning {
		reqBody["reasoning"] = map[string]any{
			"effort": "medium",
		}
	}

	return reqBody
}

// parseProxyURL parses a proxy URL string and returns a *url.URL.
// Supports socks5://, http://, and https:// schemes.
func parseProxyURL(proxyURL string) (*url.URL, error) {
	// Add scheme if not present
	if !strings.Contains(proxyURL, "://") {
		proxyURL = "http://" + proxyURL
	}
	return url.Parse(proxyURL)
}
