package llm

import (
	"bufio"
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
			creds, err := auth.LoadCodexCredentials()
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

			httpClient := &http.Client{}
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

		// Process SSE stream
				processCodexSSE(ctx, resp.Body, stream, chunkIntervalTimeout)
	}()

	return stream
}

// codexRequestBody represents the OpenAI Responses API request body.
type codexRequestBody struct {
	Model             string           `json:"model"`
	Stream            bool             `json:"stream"`
	Store             bool             `json:"store"`
	Instructions      string           `json:"instructions,omitempty"`
	Input             []any            `json:"input"`
	Tools             []codexTool      `json:"tools,omitempty"`
	ToolChoice        string           `json:"tool_choice,omitempty"`
	ParallelToolCalls bool             `json:"parallel_tool_calls"`
	Reasoning         *codexReasoning  `json:"reasoning,omitempty"`
	Text              *codexText       `json:"text,omitempty"`
	Include           []string         `json:"include,omitempty"`
}

type codexReasoning struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"`
}

type codexText struct {
	Verbosity string `json:"verbosity,omitempty"`
}

type codexTool struct {
	Type     string       `json:"type"`
	Name     string       `json:"name,omitempty"`
	Function *codexFunc   `json:"function,omitempty"`
}

type codexFunc struct {
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
				"type":  "function_call_output",
				"call_id": msg.ToolCallID,
				"output": msg.Content,
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
			Type: "function",
			Function: &codexFunc{
				Name:        t.Function.Name,
				Description: t.Function.Description,
				Parameters:  t.Function.Parameters,
				Strict:      false,
			},
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

// processCodexSSE reads the SSE stream from the Codex Responses API and emits events.
func processCodexSSE(ctx context.Context, body io.Reader, stream *EventStream[LLMEvent, LLMMessage], chunkIntervalTimeout time.Duration) {
	partial := NewPartialMessage()
	var lastUsage Usage
	chunkIndex := 0
	var stopReason string

	// Track current function call for streaming argument accumulation
	var currentToolCallID string
	var currentToolCallName string
	var currentToolCallArgs strings.Builder

	// Track current message output
	var currentTextIndex int
	textStarted := false
	thinkingStarted := false

		scanner := bufio.NewScanner(body)
	// Increase buffer size for large SSE events
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	// Set read deadline for chunk interval timeout (same pattern as client.go/anthropic.go)
	type deadliner interface {
		SetReadDeadline(time.Time) error
	}

	if dl, ok := body.(deadliner); ok && chunkIntervalTimeout > 0 {
		nextDeadline := time.Now().Add(chunkIntervalTimeout)
		if ctxDeadline, ok := ctx.Deadline(); ok && nextDeadline.After(ctxDeadline) {
			nextDeadline = ctxDeadline
		}
		dl.SetReadDeadline(nextDeadline)
	}

	stream.Push(LLMStartEvent{Partial: partial})

	for scanner.Scan() {
		// Update read deadline for each chunk, capped by context deadline.
		if dl, ok := body.(deadliner); ok && chunkIntervalTimeout > 0 {
			nextDeadline := time.Now().Add(chunkIntervalTimeout)
			if ctxDeadline, ok := ctx.Deadline(); ok && nextDeadline.After(ctxDeadline) {
				nextDeadline = ctxDeadline
			}
			dl.SetReadDeadline(nextDeadline)
		}

				line := scanner.Text()

		// Check parent context cancellation
		select {
		case <-ctx.Done():
			stream.Push(LLMErrorEvent{Error: ctx.Err()})
			return
		default:
		}

		// SSE data lines
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var event map[string]any
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		eventType, _ := event["type"].(string)

		switch eventType {
		// --- Text output events ---
		case "response.output_text.delta":
			delta, _ := event["delta"].(string)
			if delta != "" {
				partial.AppendText(delta)
				if !textStarted {
					textStarted = true
				}
				stream.Push(LLMTextDeltaEvent{Delta: delta, Index: currentTextIndex})
				chunkIndex++
			}

		case "response.output_text.done":
			// Text block completed, nothing special to do

		// --- Reasoning/thinking events ---
		case "response.reasoning_summary_text.delta":
			delta, _ := event["delta"].(string)
			if delta != "" {
				partial.AppendThinking(delta)
				if !thinkingStarted {
					thinkingStarted = true
				}
				stream.Push(LLMThinkingDeltaEvent{Delta: delta, Index: 0})
				chunkIndex++
			}

		// --- Function call events ---
		case "response.function_call_arguments.delta":
			delta, _ := event["delta"].(string)
			if delta != "" {
				currentToolCallArgs.WriteString(delta)
				chunkIndex++
			}

		case "response.output_item.added":
			item, _ := event["item"].(map[string]any)
			if item != nil {
				itemType, _ := item["type"].(string)
				switch itemType {
				case "function_call":
					currentToolCallID, _ = item["call_id"].(string)
					currentToolCallName, _ = item["name"].(string)
					currentToolCallArgs.Reset()
					// Pre-populate with any arguments in the added event
					if args, ok := item["arguments"].(string); ok {
						currentToolCallArgs.WriteString(args)
					}
				case "message":
					// New message output item
					currentTextIndex++
				}
			}

		case "response.output_item.done":
			item, _ := event["item"].(map[string]any)
			if item != nil {
				itemType, _ := item["type"].(string)
				if itemType == "function_call" {
					// Finalize the tool call
					callID := currentToolCallID
					if id, ok := item["call_id"].(string); ok && id != "" {
						callID = id
					}
					name := currentToolCallName
					if n, ok := item["name"].(string); ok && n != "" {
						name = n
					}
					args := currentToolCallArgs.String()
					if a, ok := item["arguments"].(string); ok && a != "" {
						args = a
					}

					// Find the tool call index
					tcIndex := len(partial.ToolCalls)

					toolCall := &ToolCall{
						ID:   callID,
						Type: "function",
						Function: FunctionCall{
							Name:      name,
							Arguments: args,
						},
					}
					partial.AppendToolCall(tcIndex, toolCall)
					stream.Push(LLMToolCallDeltaEvent{Index: tcIndex, ToolCall: toolCall})

					// Reset state
					currentToolCallID = ""
					currentToolCallName = ""
					currentToolCallArgs.Reset()
				}
			}

		// --- Completion events ---
		case "response.completed", "response.done", "response.incomplete":
			resp, _ := event["response"].(map[string]any)
			if resp != nil {
				// Extract stop reason from status
				status, _ := resp["status"].(string)
				switch status {
				case "completed":
					stopReason = "stop"
				case "incomplete":
					stopReason = "length"
				case "failed":
					stopReason = "error"
					if errObj, ok := resp["error"].(map[string]any); ok {
						if msg, ok := errObj["message"].(string); ok {
							stream.Push(LLMErrorEvent{Error: fmt.Errorf("codex response failed: %s", msg)})
							return
						}
					}
				default:
					stopReason = "stop"
				}

				// Extract usage
				if usageRaw, ok := resp["usage"].(map[string]any); ok {
					lastUsage = extractCodexUsage(usageRaw)
				}

				// Extract output items (text, tool_calls) if not streamed
				if outputItems, ok := resp["output"].([]any); ok {
					processCodexOutputItems(outputItems, partial, stream)
				}
			}

		// --- Error events ---
		case "error":
			errMsg, _ := event["message"].(string)
			if errMsg == "" {
				errMsg = fmt.Sprintf("codex error: %v", event)
			}
			stream.Push(LLMErrorEvent{Error: fmt.Errorf("%s", errMsg)})
			return
		}
	}

	if err := scanner.Err(); err != nil {
		if chunkIndex == 0 {
			stream.Push(LLMErrorEvent{Error: fmt.Errorf("codex stream read error: %w", err)})
			return
		}
		// Partial read - return what we have
	}

	// If no stop reason was set from events, default to "stop"
	if stopReason == "" {
		stopReason = "stop"
	}

	// Emit done event
	if chunkIndex == 0 {
		stream.Push(LLMErrorEvent{Error: fmt.Errorf("codex stream ended without any data chunks")})
		return
	}

	finalMsg := partial.ToLLMMessage()
	stream.Push(LLMDoneEvent{
		Message:    &finalMsg,
		Usage:      lastUsage,
		StopReason: stopReason,
	})
}

// processCodexOutputItems processes output items from a completed response.
// This handles non-streamed or final output items.
func processCodexOutputItems(items []any, partial *PartialMessage, stream *EventStream[LLMEvent, LLMMessage]) {
	for _, item := range items {
		itemMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		itemType, _ := itemMap["type"].(string)

		switch itemType {
		case "message":
			// Extract text content
			if content, ok := itemMap["content"].([]any); ok {
				for _, c := range content {
					if cMap, ok := c.(map[string]any); ok {
						if text, ok := cMap["text"].(string); ok {
							partial.AppendText(text)
						}
					}
				}
			}
		case "function_call":
			callID, _ := itemMap["call_id"].(string)
			name, _ := itemMap["name"].(string)
			args, _ := itemMap["arguments"].(string)

			tcIndex := len(partial.ToolCalls)
			toolCall := &ToolCall{
				ID:   callID,
				Type: "function",
				Function: FunctionCall{
					Name:      name,
					Arguments: args,
				},
			}
			partial.AppendToolCall(tcIndex, toolCall)
			stream.Push(LLMToolCallDeltaEvent{Index: tcIndex, ToolCall: toolCall})
		}
	}
}

// extractCodexUsage extracts token usage from the Responses API format.
func extractCodexUsage(usage map[string]any) Usage {
	u := Usage{}

	if v, ok := usage["input_tokens"]; ok {
		switch n := v.(type) {
		case float64:
			u.InputTokens = int(n)
		case json.Number:
			if i, err := n.Int64(); err == nil {
				u.InputTokens = int(i)
			}
		}
	}
	if v, ok := usage["output_tokens"]; ok {
		switch n := v.(type) {
		case float64:
			u.OutputTokens = int(n)
		case json.Number:
			if i, err := n.Int64(); err == nil {
				u.OutputTokens = int(i)
			}
		}
	}
	if v, ok := usage["total_tokens"]; ok {
		switch n := v.(type) {
		case float64:
			u.TotalTokens = int(n)
		case json.Number:
			if i, err := n.Int64(); err == nil {
				u.TotalTokens = int(i)
			}
		}
	}

	// Calculate total if not provided
	if u.TotalTokens == 0 {
		u.TotalTokens = u.InputTokens + u.OutputTokens
	}

	return u
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

