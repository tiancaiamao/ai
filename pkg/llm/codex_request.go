package llm

import (
	"net/http"
	"strings"

	"github.com/tiancaiamao/ai/pkg/auth"
)

const defaultCodexBaseURL = "https://chatgpt.com/backend-api"

func responsesRequestBody(model Model, ctx LLMContext) map[string]any {
	if model.API == "openai-codex-responses" {
		return codexRequestBody(model, ctx)
	}
	return buildOpenAIResponsesRequest(model, ctx)
}

func responsesEndpoint(model Model) string {
	if model.API != "openai-codex-responses" {
		return modelURL(model)
	}
	raw := strings.TrimRight(strings.TrimSpace(model.BaseURL), "/")
	if raw == "" {
		raw = defaultCodexBaseURL
	}
	if strings.HasSuffix(raw, "/codex/responses") {
		return raw
	}
	if strings.HasSuffix(raw, "/codex") {
		return raw + "/responses"
	}
	return raw + "/codex/responses"
}

func responsesHeaders(model Model, apiKey string) http.Header {
	headers := make(http.Header)
	headers.Set("Content-Type", "application/json")
	headers.Set("Authorization", "Bearer "+apiKey)
	if model.API != "openai-codex-responses" {
		return headers
	}
	headers.Set("OpenAI-Beta", "responses=experimental")
	headers.Set("Accept", "text/event-stream")
	if accountID, err := auth.ExtractAccountID(apiKey); err == nil && accountID != "" {
		headers.Set("openai-account-id", accountID)
		headers.Set("chatgpt-account-id", accountID)
	}
	return headers
}

func codexRequestBody(model Model, ctx LLMContext) map[string]any {
	body := map[string]any{
		"model":               model.ID,
		"stream":              true,
		"store":               false,
		"instructions":        ctx.SystemPrompt,
		"input":               codexInput(ctx),
		"tool_choice":         "auto",
		"parallel_tool_calls": true,
		"text":                map[string]any{"verbosity": "low"},
		"reasoning":           map[string]any{"effort": "high", "summary": "auto"},
		"include":             []string{"reasoning.encrypted_content"},
	}
	if tools := codexTools(ctx.Tools); len(tools) > 0 {
		body["tools"] = tools
	}
	return body
}

func codexInput(ctx LLMContext) []any {
	var input []any
	for _, msg := range ctx.Messages {
		switch msg.Role {
		case "user":
			input = append(input, map[string]any{"role": "user", "content": []map[string]any{{"type": "input_text", "text": msg.Content}}})
		case "assistant":
			if msg.Content != "" {
				input = append(input, map[string]any{"type": "message", "role": "assistant", "status": "completed", "content": []map[string]any{{"type": "output_text", "text": msg.Content}}})
			}
			for _, call := range msg.ToolCalls {
				input = append(input, map[string]any{"type": "function_call", "call_id": call.ID, "name": call.Function.Name, "arguments": call.Function.Arguments})
			}
		case "tool":
			input = append(input, map[string]any{"type": "function_call_output", "call_id": msg.ToolCallID, "output": msg.Content})
		}
	}
	return input
}

func codexTools(tools []LLMTool) []map[string]any {
	result := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		result = append(result, map[string]any{
			"type": "function", "name": tool.Function.Name,
			"description": tool.Function.Description, "parameters": tool.Function.Parameters,
		})
	}
	return result
}
