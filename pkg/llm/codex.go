package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/tiancaiamao/ai/pkg/auth"
	"github.com/tiancaiamao/ai/pkg/netutil"
)

const defaultCodexBaseURL = "https://chatgpt.com/backend-api"

// StreamCodex streams a completion from the OpenAI Codex Responses API.
// It uses the Responses API format (not Chat Completions) and authenticates
// via OAuth tokens from the ChatGPT subscription.
func StreamCodex(
	ctx context.Context,
	model Model,
	llmCtx LLMContext,
	apiKey string,
	chunkIntervalTimeout time.Duration,
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

		// Resolve credentials: apiKey can be the OAuth access token passed in,
		// or we load it from auth.json automatically for the codex provider.
		accessToken := apiKey
		var accountID string

		if accessToken == "" {
			// Try loading from auth.json
			creds, err := auth.LoadCodexCredentialsWithProxy(model.Proxy)
			if err != nil {
				stream.Push(LLMErrorEvent{Error: fmt.Errorf("no Codex credentials: %w (run 'ai --login-codex' to authenticate)", err)})
				return
			}
			accessToken = creds.Access
			accountID = creds.AccountID
		} else {
			// Extract account ID from JWT token
			id, err := auth.ExtractAccountID(accessToken)
			if err != nil {
				// Non-fatal: some tokens might not have it
				accountID = ""
			} else {
				accountID = id
			}
		}

		// Build request body in OpenAI Responses API format
		reqBody := buildCodexRequestBody(model, llmCtx)
		bodyJson, err := json.Marshal(reqBody)
		if err != nil {
			stream.Push(LLMErrorEvent{Error: fmt.Errorf("marshal request: %w", err)})
			return
		}

		// Resolve URL: baseUrl/codex/responses
		endpoint := resolveCodexURL(model.BaseURL)

		// Build request
		req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewReader(bodyJson))
		if err != nil {
			stream.Push(LLMErrorEvent{Error: fmt.Errorf("create request: %w", err)})
			return
		}

		// Set headers
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+accessToken)
		req.Header.Set("OpenAI-Beta", "responses=experimental")
		req.Header.Set("accept", "text/event-stream")
		if accountID != "" {
			req.Header.Set("openai-account-id", accountID)
			req.Header.Set("chatgpt-account-id", accountID)
		}

		// Execute request with retry
		var resp *http.Response
		var lastErr error
		const maxRetries = 3
		const baseDelay = 500 * time.Millisecond

		for attempt := 0; attempt <= maxRetries; attempt++ {
			if ctx.Err() != nil {
				stream.Push(LLMErrorEvent{Error: ctx.Err()})
				return
			}

			// Reset body for retry
			req.Body = io.NopCloser(bytes.NewReader(bodyJson))
			req.GetBody = func() (io.ReadCloser, error) {
				return io.NopCloser(bytes.NewReader(bodyJson)), nil
			}

			httpClient, err := netutil.NewHTTPClient(model.Proxy)
			if err != nil {
				stream.Push(LLMErrorEvent{Error: fmt.Errorf("configure model proxy: %w", err)})
				return
			}
			resp, lastErr = httpClient.Do(req)
			if lastErr != nil {
				if attempt < maxRetries {
					delay := baseDelay * time.Duration(1<<uint(attempt))
					select {
					case <-time.After(delay):
					case <-ctx.Done():
						stream.Push(LLMErrorEvent{Error: ctx.Err()})
						return
					}
					continue
				}
				stream.Push(LLMErrorEvent{Error: fmt.Errorf("request failed after retries: %w", lastErr)})
				return
			}

			if resp.StatusCode == 200 {
				break
			}

			// Read error body
			errBody, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			errMsg := string(errBody)

			if isRetryableStatus(resp.StatusCode) && attempt < maxRetries {
				delay := baseDelay * time.Duration(1<<uint(attempt))
				if ra := resp.Header.Get("Retry-After"); ra != "" {
					if d := parseRetryAfterHeaderCodex(ra); d > 0 {
						delay = d
					}
				}
				if raMs := resp.Header.Get("Retry-After-Ms"); raMs != "" {
					if ms, err := strconv.Atoi(raMs); err == nil && ms > 0 {
						delay = time.Duration(ms) * time.Millisecond
					}
				}
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					stream.Push(LLMErrorEvent{Error: ctx.Err()})
					return
				}
				continue
			}

			stream.Push(LLMErrorEvent{Error: ClassifyAPIError(resp.StatusCode, errMsg)})
			return
		}

		if resp == nil {
			stream.Push(LLMErrorEvent{Error: fmt.Errorf("no response after retries")})
			return
		}
		defer resp.Body.Close()

		// Process the shared Responses API stream.
		processResponsesSSE(ctx, resp.Body, stream, chunkIntervalTimeout)
	}()

	return stream
}

// codexRequestBody represents the OpenAI Responses API request body.
type codexRequestBody struct {
	Model             string          `json:"model"`
	Stream            bool            `json:"stream"`
	Store             bool            `json:"store"`
	Instructions      string          `json:"instructions,omitempty"`
	Input             []any           `json:"input"`
	Tools             []codexTool     `json:"tools,omitempty"`
	ToolChoice        string          `json:"tool_choice,omitempty"`
	ParallelToolCalls bool            `json:"parallel_tool_calls"`
	Reasoning         *codexReasoning `json:"reasoning,omitempty"`
	Text              *codexText      `json:"text,omitempty"`
	Include           []string        `json:"include,omitempty"`
}

type codexReasoning struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"`
}

type codexText struct {
	Verbosity string `json:"verbosity,omitempty"`
}

type codexTool struct {
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
	Strict      bool           `json:"strict"`
}

// buildCodexRequestBody converts LLMContext to the Responses API request format.
func buildCodexRequestBody(model Model, llmCtx LLMContext) codexRequestBody {
	input := buildCodexInput(llmCtx)
	tools := buildCodexTools(llmCtx.Tools)

	body := codexRequestBody{
		Model:             model.ID,
		Stream:            true,
		Store:             false,
		Instructions:      llmCtx.SystemPrompt,
		Input:             input,
		Tools:             tools,
		ToolChoice:        "auto",
		ParallelToolCalls: true,
		Text:              &codexText{Verbosity: "low"},
		Include:           []string{"reasoning.encrypted_content"},
	}

	// Default reasoning effort for reasoning-capable models
	body.Reasoning = &codexReasoning{
		Effort:  "high",
		Summary: "auto",
	}

	return body
}

// buildCodexInput converts LLMMessages to the Responses API input format.
func buildCodexInput(llmCtx LLMContext) []any {
	var input []any

	for _, msg := range llmCtx.Messages {
		switch msg.Role {
		case "system", "developer":
			// System prompts go into the "instructions" field, not input
			continue
		case "user":
			input = append(input, map[string]any{
				"role": "user",
				"content": []map[string]any{
					{"type": "input_text", "text": msg.Content},
				},
			})
		case "assistant":
			entry := map[string]any{
				"type": "message",
				"role": "assistant",
			}
			var content []map[string]any
			if msg.Content != "" {
				content = append(content, map[string]any{
					"type": "output_text",
					"text": msg.Content,
				})
			}
			if len(msg.ToolCalls) > 0 {
				for _, tc := range msg.ToolCalls {
					input = append(input, map[string]any{
						"type":      "function_call",
						"call_id":   tc.ID,
						"name":      tc.Function.Name,
						"arguments": tc.Function.Arguments,
					})
				}
			}
			if len(content) > 0 {
				entry["content"] = content
				entry["status"] = "completed"
				input = append(input, entry)
			}
		case "tool":
			// Tool results are function_call_output items
			input = append(input, map[string]any{
				"type":    "function_call_output",
				"call_id": msg.ToolCallID,
				"output":  msg.Content,
			})
		}
	}

	return input
}

// buildCodexTools converts LLMTools to the Responses API tool format.
func buildCodexTools(tools []LLMTool) []codexTool {
	if len(tools) == 0 {
		return nil
	}

	result := make([]codexTool, 0, len(tools))
	for _, t := range tools {
		result = append(result, codexTool{
			Type:        "function",
			Name:        t.Function.Name,
			Description: t.Function.Description,
			Parameters:  t.Function.Parameters,
			Strict:      false,
		})
	}
	return result
}

// resolveCodexURL resolves the Codex API endpoint URL.
func resolveCodexURL(baseURL string) string {
	raw := strings.TrimSpace(baseURL)
	if raw == "" {
		raw = defaultCodexBaseURL
	}
	raw = strings.TrimRight(raw, "/")
	if strings.HasSuffix(raw, "/codex/responses") {
		return raw
	}
	if strings.HasSuffix(raw, "/codex") {
		return raw + "/responses"
	}
	return raw + "/codex/responses"
}

func isRetryableStatus(status int) bool {
	return status == 429 || status == 500 || status == 502 || status == 503 || status == 504
}

func parseRetryAfterHeaderCodex(value string) time.Duration {
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
