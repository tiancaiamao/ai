package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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

		headers := make(http.Header)
		headers.Set("Content-Type", "application/json")
		headers.Set("Authorization", "Bearer "+accessToken)
		headers.Set("OpenAI-Beta", "responses=experimental")
		headers.Set("Accept", "text/event-stream")
		if accountID != "" {
			headers.Set("openai-account-id", accountID)
			headers.Set("chatgpt-account-id", accountID)
		}
		resp, err := doResponsesRequest(ctx, responsesRequestOptions{
			Endpoint:   resolveCodexURL(model.BaseURL),
			Body:       bodyJson,
			Headers:    headers,
			Proxy:      model.Proxy,
			MaxRetries: 3,
		})
		if err != nil {
			stream.Push(LLMErrorEvent{Error: err})
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
