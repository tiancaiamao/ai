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
	"sort"
	"strings"
	"time"

	"github.com/tiancaiamao/ai/pkg/traceevent"
	"golang.org/x/net/proxy"
)

// responsesEventChunk is the parsed JSON body of a single SSE data line from
// the OpenAI Responses API. Only the fields the streaming parser needs are
// declared; unknown fields are ignored by encoding/json.
type responsesEventChunk struct {
	Type        string `json:"type"`
	Delta       string `json:"delta"`
	Arguments   string `json:"arguments"`
	OutputIndex int    `json:"output_index"`
	Item        *struct {
		Type      string `json:"type"`
		ID        string `json:"id"`
		Name      string `json:"name"`
		CallID    string `json:"call_id"`
		Arguments string `json:"arguments"`
		Content   []struct {
			Type    string `json:"type"`
			Text    string `json:"text"`
			Refusal string `json:"refusal"`
		} `json:"content"`
		Summary []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"summary"`
	} `json:"item"`
	Response *struct {
		Status string `json:"status"`
		Usage  *struct {
			InputTokens        int `json:"input_tokens"`
			OutputTokens       int `json:"output_tokens"`
			TotalTokens        int `json:"total_tokens"`
			InputTokensDetails *struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"input_tokens_details"`
		} `json:"usage"`
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		IncompleteDetails *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
	} `json:"response"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// responsesSlotKind is the type of an output slot.
type responsesSlotKind int

const (
	slotText responsesSlotKind = iota
	slotThinking
	slotToolCall
)

// responsesOutputSlot mirrors pi's ResponsesOutputSlot: one per output_index,
// accumulating streaming deltas until response.output_item.done finalizes it.
type responsesOutputSlot struct {
	kind     responsesSlotKind
	text     strings.Builder
	args     strings.Builder
	toolID   string
	toolName string
}

// openaiResponsesParser accumulates Responses API stream events into slots.
// It is a pure accumulator (no stream pushing) so it can be unit-tested.
type openaiResponsesParser struct {
	slots map[int]*responsesOutputSlot
}

func newOpenAIResponsesParser() *openaiResponsesParser {
	return &openaiResponsesParser{slots: make(map[int]*responsesOutputSlot)}
}

// getOrCreateSlot returns the slot at outputIndex, creating one of the given
// kind if it does not exist yet (pi's getOrCreateSlot semantics). The delta
// events imply their slot kind, so a missing slot can be created lazily.
func (p *openaiResponsesParser) getOrCreateSlot(outputIndex int, kind responsesSlotKind) *responsesOutputSlot {
	if s, ok := p.slots[outputIndex]; ok {
		return s
	}
	s := &responsesOutputSlot{kind: kind}
	p.slots[outputIndex] = s
	return s
}

// handle processes a single stream event and returns the terminal stop reason
// ("stop"/"length"/"error") once the response is complete, or "" to continue.
func (p *openaiResponsesParser) handle(chunk responsesEventChunk) (string, error) {
	switch chunk.Type {
	case "response.output_item.added":
		if chunk.Item == nil {
			return "", nil
		}
		switch chunk.Item.Type {
		case "message":
			p.getOrCreateSlot(chunk.OutputIndex, slotText)
		case "reasoning":
			p.getOrCreateSlot(chunk.OutputIndex, slotThinking)
		case "function_call":
			slot := p.getOrCreateSlot(chunk.OutputIndex, slotToolCall)
			slot.toolID = chunk.Item.CallID
			slot.toolName = chunk.Item.Name
		}

	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		if chunk.Delta != "" {
			slot := p.getOrCreateSlot(chunk.OutputIndex, slotThinking)
			slot.text.WriteString(chunk.Delta)
		}

	case "response.reasoning_summary_part.done":
		slot := p.getOrCreateSlot(chunk.OutputIndex, slotThinking)
		slot.text.WriteString("\n\n")

	case "response.output_text.delta", "response.refusal.delta":
		if chunk.Delta != "" {
			slot := p.getOrCreateSlot(chunk.OutputIndex, slotText)
			slot.text.WriteString(chunk.Delta)
		}

	case "response.function_call_arguments.delta":
		if chunk.Delta != "" {
			slot := p.getOrCreateSlot(chunk.OutputIndex, slotToolCall)
			slot.args.WriteString(chunk.Delta)
		}

	case "response.function_call_arguments.done":
		// The done event carries the complete arguments JSON. If the deltas
		// were truncated (e.g. proxy aggregation), append the missing tail.
		slot := p.getOrCreateSlot(chunk.OutputIndex, slotToolCall)
		buffered := slot.args.String()
		if chunk.Arguments != "" && chunk.Arguments != buffered {
			if strings.HasPrefix(chunk.Arguments, buffered) {
				slot.args.WriteString(strings.TrimPrefix(chunk.Arguments, buffered))
			} else {
				// Mismatched partial buffer — trust the authoritative value.
				slot.args.Reset()
				slot.args.WriteString(chunk.Arguments)
			}
		}

	case "response.output_item.done":
		if chunk.Item == nil {
			return "", nil
		}
		switch chunk.Item.Type {
		case "message":
			slot := p.getOrCreateSlot(chunk.OutputIndex, slotText)
			text := ""
			for _, c := range chunk.Item.Content {
				switch c.Type {
				case "output_text":
					text += c.Text
				case "refusal":
					text += c.Refusal
				}
			}
			if text != "" {
				slot.text.Reset()
				slot.text.WriteString(text)
			}
		case "reasoning":
			slot := p.getOrCreateSlot(chunk.OutputIndex, slotThinking)
			if len(chunk.Item.Summary) > 0 {
				parts := make([]string, 0, len(chunk.Item.Summary))
				for _, s := range chunk.Item.Summary {
					parts = append(parts, s.Text)
				}
				slot.text.Reset()
				slot.text.WriteString(strings.Join(parts, "\n\n"))
			} else if len(chunk.Item.Content) > 0 {
				parts := make([]string, 0, len(chunk.Item.Content))
				for _, c := range chunk.Item.Content {
					parts = append(parts, c.Text)
				}
				slot.text.Reset()
				slot.text.WriteString(strings.Join(parts, "\n\n"))
			}
		case "function_call":
			slot := p.getOrCreateSlot(chunk.OutputIndex, slotToolCall)
			if chunk.Item.Arguments != "" {
				slot.args.Reset()
				slot.args.WriteString(chunk.Item.Arguments)
			}
		}

	case "response.completed", "response.incomplete":
		status := chunk.Type[len("response."):]
		var incompleteReason string
		if chunk.Response != nil {
			if chunk.Response.Status != "" {
				status = chunk.Response.Status
			}
			if chunk.Response.IncompleteDetails != nil {
				incompleteReason = chunk.Response.IncompleteDetails.Reason
			}
		}
		return mapResponsesStopReason(status, incompleteReason), nil

	case "response.failed":
		msg := "response failed"
		if chunk.Response != nil && chunk.Response.Error != nil {
			code, errMsg := chunk.Response.Error.Code, chunk.Response.Error.Message
			if code == "" {
				code = "unknown"
			}
			if errMsg == "" {
				errMsg = "no message"
			}
			msg = code + ": " + errMsg
		}
		return "error", fmt.Errorf("%s", msg)

	case "error":
		errMsg := chunk.Message
		if chunk.Code != "" {
			errMsg = fmt.Sprintf("Error Code %s: %s", chunk.Code, chunk.Message)
		}
		if errMsg == "" {
			errMsg = "unknown error"
		}
		return "error", fmt.Errorf("%s", errMsg)
	}
	return "", nil
}

// mapResponsesStopReason mirrors pi's mapStopReason.
func mapResponsesStopReason(status, incompleteReason string) string {
	switch status {
	case "completed":
		return "stop"
	case "incomplete":
		if incompleteReason == "max_output_tokens" {
			return "length"
		}
		return "error"
	case "failed", "cancelled":
		return "error"
	default: // in_progress, queued, or unknown
		return "stop"
	}
}

// buildMessage assembles the final LLMMessage from accumulated slots, ordered
// by output_index. Slots without content (empty text/args) are skipped.
func (p *openaiResponsesParser) buildMessage() LLMMessage {
	indexes := make([]int, 0, len(p.slots))
	for idx := range p.slots {
		indexes = append(indexes, idx)
	}
	sort.Ints(indexes)

	var content, thinking strings.Builder
	var toolCalls []ToolCall
	for _, idx := range indexes {
		slot := p.slots[idx]
		switch slot.kind {
		case slotText:
			if slot.text.Len() > 0 {
				content.WriteString(slot.text.String())
			}
		case slotThinking:
			if slot.text.Len() > 0 {
				thinking.WriteString(slot.text.String())
			}
		case slotToolCall:
			if slot.args.Len() == 0 {
				continue
			}
			toolCalls = append(toolCalls, ToolCall{
				ID:   slot.toolID,
				Type: "function",
				Function: FunctionCall{
					Name:      slot.toolName,
					Arguments: slot.args.String(),
				},
			})
		}
	}

	msg := LLMMessage{
		Role:    "assistant",
		Content: content.String(),
	}
	if thinking.Len() > 0 {
		msg.Thinking = thinking.String()
	}
	if len(toolCalls) > 0 {
		msg.ToolCalls = toolCalls
	}
	return msg
}

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
			applyProxy(client, proxyURL)
		} else if proxyURL := os.Getenv("HTTPS_PROXY"); proxyURL != "" {
			applyProxy(client, proxyURL)
		}

		resp, err := client.Do(req)
		if err != nil {
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
			retryAfter := parseRetryAfterHeader(resp.Header.Get("Retry-After"))
			stream.Push(LLMErrorEvent{Error: ClassifyAPIErrorWithRetryAfter(resp.StatusCode, string(body), retryAfter)})
			return
		}

		// Parse SSE stream
		scanner := bufio.NewScanner(resp.Body)
		// Increase buffer size for large responses (default 64KB, max 1MB)
		const maxTokenSize = 1024 * 1024
		buf := make([]byte, 64*1024)
		scanner.Buffer(buf, maxTokenSize)

		// Set read deadline so a stalled upstream (connected but silent)
		// triggers the chunk interval timeout instead of hanging until the
		// total request deadline.
		type deadliner interface {
			SetReadDeadline(time.Time) error
		}
		setReadDeadline := func() {
			if dl, ok := resp.Body.(deadliner); ok && chunkIntervalTimeout > 0 {
				nextDeadline := time.Now().Add(chunkIntervalTimeout)
				if ctxDeadline, ok := ctx.Deadline(); ok && nextDeadline.After(ctxDeadline) {
					nextDeadline = ctxDeadline
				}
				dl.SetReadDeadline(nextDeadline)
			}
		}
		setReadDeadline()

		parser := newOpenAIResponsesParser()

		// Signal stream start so the agent layer accepts subsequent deltas.
		stream.Push(LLMStartEvent{Partial: NewPartialMessage()})

		for scanner.Scan() {
			setReadDeadline()

			// Check parent context cancellation
			select {
			case <-ctx.Done():
				stream.Push(LLMErrorEvent{Error: ctx.Err()})
				return
			default:
			}

			line := scanner.Text()

			// Skip empty lines and SSE comments
			if line == "" || strings.HasPrefix(line, ":") {
				continue
			}

			// Parse SSE data line
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")

			// Some proxies terminate with a bare [DONE] sentinel, which is
			// not valid JSON — check before unmarshal or the break below is
			// unreachable.
			if data == "[DONE]" {
				break
			}

			var chunk responsesEventChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				continue // Skip malformed chunks
			}

			if chunk.Type == "" {
				continue
			}

			// Stream deltas to the consumer as they arrive. Slot accumulation
			// happens exclusively in parser.handle below — do not write to
			// slots here or deltas will be double-accumulated.
			switch chunk.Type {
			case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
				stream.Push(LLMThinkingDeltaEvent{Delta: chunk.Delta, Index: chunk.OutputIndex})
			case "response.reasoning_summary_part.done":
				stream.Push(LLMThinkingDeltaEvent{Delta: "\n\n", Index: chunk.OutputIndex})
			case "response.output_text.delta", "response.refusal.delta":
				stream.Push(LLMTextDeltaEvent{Delta: chunk.Delta, Index: chunk.OutputIndex})
			case "response.function_call_arguments.delta":
				stream.Push(LLMToolCallDeltaEvent{
					Index: chunk.OutputIndex,
					ToolCall: &ToolCall{
						Type:     "function",
						Function: FunctionCall{Arguments: chunk.Delta},
					},
				})
			case "response.output_item.added":
				if chunk.Item != nil && chunk.Item.Type == "function_call" {
					stream.Push(LLMToolCallDeltaEvent{
						Index: chunk.OutputIndex,
						ToolCall: &ToolCall{
							ID:       chunk.Item.CallID,
							Type:     "function",
							Function: FunctionCall{Name: chunk.Item.Name},
						},
					})
				}
			}

			// Feed the accumulator; terminal events end the loop.
			stopReason, err := parser.handle(chunk)
			if err != nil {
				stream.Push(LLMErrorEvent{Error: err})
				return
			}
			if stopReason != "" {
				msg := parser.buildMessage()
				usage := extractResponsesUsage(chunk)
				// A completed response containing tool calls is a tool-use turn
				// regardless of the provider status (mirrors pi's mapping).
				// Note: agent's isSuccessfulStopReason whitelist expects
				// "tool_calls" (OpenAI style), not Anthropic's "tool_use".
				if len(msg.ToolCalls) > 0 && stopReason == "stop" {
					stopReason = "tool_calls"
				}
				stream.Push(LLMDoneEvent{Message: &msg, Usage: usage, StopReason: stopReason})
				return
			}
		}

		if err := scanner.Err(); err != nil {
			stream.Push(LLMErrorEvent{Error: fmt.Errorf("error reading stream: %w", err)})
			return
		}

		// Stream ended without a terminal event (e.g. [DONE] or clean EOF).
		msg := parser.buildMessage()
		stream.Push(LLMDoneEvent{Message: &msg, StopReason: "stop"})
	}()

	return stream
}

// extractResponsesUsage maps Responses API usage to the local Usage struct,
// subtracting cached tokens from input, mirroring pi's accounting.
func extractResponsesUsage(chunk responsesEventChunk) Usage {
	u := Usage{}
	if chunk.Response == nil || chunk.Response.Usage == nil {
		return u
	}
	ru := chunk.Response.Usage
	cached := 0
	if ru.InputTokensDetails != nil {
		cached = ru.InputTokensDetails.CachedTokens
	}
	u.InputTokens = max(0, ru.InputTokens-cached)
	u.OutputTokens = ru.OutputTokens
	u.TotalTokens = ru.TotalTokens
	if cached > 0 {
		u.PromptTokensDetails = &PromptTokensDetails{CachedTokens: cached}
	}
	return u
}

// applyProxy configures the HTTP client to route through the given proxy URL.
// Supports socks5:// (via golang.org/x/net/proxy) and http(s):// schemes.
func applyProxy(client *http.Client, proxyURL string) {
	parsed, err := parseProxyURL(proxyURL)
	if err != nil {
		return
	}
	if parsed.Scheme == "socks5" || parsed.Scheme == "socks5h" {
		dialer, err := proxy.SOCKS5("tcp", parsed.Host, nil, nil)
		if err != nil {
			return
		}
		// Keep the standard transport defaults (connection pooling, HTTP/2, etc.)
		// while replacing only the dialer used for SOCKS5 connections.
		baseTransport, ok := http.DefaultTransport.(*http.Transport)
		if !ok {
			return
		}
		transport := baseTransport.Clone()
		if cd, ok := dialer.(proxy.ContextDialer); ok {
			transport.DialContext = cd.DialContext
		} else {
			transport.Dial = dialer.Dial
		}
		client.Transport = transport
		return
	}
	// HTTP/HTTPS proxy
	baseTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return
	}
	transport := baseTransport.Clone()
	transport.Proxy = http.ProxyURL(parsed)
	client.Transport = transport
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
		switch msg.Role {
		case "user":
			input = append(input, map[string]any{
				"role": "user",
				"content": []map[string]any{
					{
						"type": "input_text",
						"text": msg.Content,
					},
				},
			})
		case "assistant":
			// Emit plain text first (if any). Reasoning items are not
			// replayable across requests without store:true.
			if msg.Content != "" {
				input = append(input, map[string]any{
					"role":    "assistant",
					"content": msg.Content,
				})
			}
			// Responses API requires tool calls as top-level function_call
			// items — NOT chat-completions style nested "tool_calls".
			for _, tc := range msg.ToolCalls {
				input = append(input, map[string]any{
					"type":      "function_call",
					"call_id":   tc.ID,
					"name":      tc.Function.Name,
					"arguments": tc.Function.Arguments,
				})
			}
		case "tool":
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

	// Add reasoning parameters if the model supports it. Honor the
	// requested thinking level; default to medium when unspecified.
	if model.Reasoning {
		effort := "medium"
		switch llmCtx.ThinkingLevel {
		case "minimal", "low", "medium", "high":
			effort = llmCtx.ThinkingLevel
		case "xhigh":
			effort = "high" // Responses API has no xhigh
		case "off":
			// No API-level disable for Responses; omit the parameter so the
			// provider default applies.
			effort = ""
		}
		if effort != "" {
			reqBody["reasoning"] = map[string]any{
				"effort": effort,
			}
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
