package llm

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseProxyURL(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		want    string // expected host
	}{
		{"full url", "http://proxy:8080", false, "proxy:8080"},
		{"https", "https://proxy:8080", false, "proxy:8080"},
		{"no scheme", "proxy:8080", false, "proxy:8080"},
		{"invalid", "://bad", true, ""},
		{"empty", "", false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseProxyURL(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseProxyURL(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if err == nil && got.Host != tt.want {
				t.Errorf("parseProxyURL(%q).Host = %q, want %q", tt.input, got.Host, tt.want)
			}
		})
	}
}

func TestBuildOpenAIResponsesRequest(t *testing.T) {
	t.Run("with system prompt", func(t *testing.T) {
		model := Model{
			ID:       "test-model",
			Provider: "openai",
		}
		ctx := LLMContext{
			SystemPrompt: "You are a test assistant.",
		}
		req := buildOpenAIResponsesRequest(model, ctx)
		input, ok := req["input"].([]map[string]any)
		if !ok {
			t.Fatal("input should be []map[string]any")
		}
		if len(input) != 1 {
			t.Fatalf("expected 1 input entry, got %d", len(input))
		}
		if input[0]["role"] != "system" {
			t.Errorf("role = %q, want 'system'", input[0]["role"])
		}
	})

	t.Run("reasoning model uses developer role", func(t *testing.T) {
		model := Model{Reasoning: true}
		ctx := LLMContext{SystemPrompt: "test"}
		req := buildOpenAIResponsesRequest(model, ctx)
		input := req["input"].([]map[string]any)
		if input[0]["role"] != "developer" {
			t.Errorf("role = %q, want 'developer' for reasoning model", input[0]["role"])
		}
	})

	t.Run("empty prompt no system entry", func(t *testing.T) {
		model := Model{}
		ctx := LLMContext{}
		req := buildOpenAIResponsesRequest(model, ctx)
		input := req["input"].([]map[string]any)
		if len(input) != 0 {
			t.Errorf("expected 0 input entries for empty prompt, got %d", len(input))
		}
	})

	t.Run("model is set in request", func(t *testing.T) {
		model := Model{ID: "my-model"}
		req := buildOpenAIResponsesRequest(model, LLMContext{})
		if req["model"] != "my-model" {
			t.Errorf("model = %q, want 'my-model'", req["model"])
		}
	})

	t.Run("user image content uses input_image", func(t *testing.T) {
		model := Model{ID: "vision-model"}
		ctx := LLMContext{
			Messages: []LLMMessage{{
				Role:    "user",
				Content: "Describe this image",
				ContentParts: []ContentPart{{
					Type: "image_url",
					ImageURL: &struct {
						URL string `json:"url"`
					}{URL: "data:image/jpeg;base64,abc"},
				}},
			}},
		}

		req := buildOpenAIResponsesRequest(model, ctx)
		input := req["input"].([]map[string]any)
		content := input[0]["content"].([]map[string]any)
		if len(content) != 2 {
			t.Fatalf("expected text and image content, got %d parts", len(content))
		}
		if content[0]["type"] != "input_text" || content[0]["text"] != "Describe this image" {
			t.Errorf("text content = %#v, want input_text with prompt", content[0])
		}
		if content[1]["type"] != "input_image" || content[1]["image_url"] != "data:image/jpeg;base64,abc" {
			t.Errorf("image content = %#v, want input_image with data URL", content[1])
		}
	})
}

// testResponsesItem mirrors the responsesEventChunk.Item inline struct for test
// construction, avoiding repeated anonymous struct literals.
type testResponsesItem struct {
	Type      string
	ID        string
	Name      string
	CallID    string
	Arguments string
	Content   []testContentPart
	Summary   []testSummaryPart
}

type testContentPart struct {
	Type    string
	Text    string
	Refusal string
}

type testSummaryPart struct {
	Type string
	Text string
}

func (i testResponsesItem) chunk(outputIndex int) responsesEventChunk {
	item := &struct {
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
	}{
		Type:      i.Type,
		ID:        i.ID,
		Name:      i.Name,
		CallID:    i.CallID,
		Arguments: i.Arguments,
	}
	if item.CallID == "" {
		item.CallID = i.ID // tool call id commonly flows through ID in tests
	}
	for _, c := range i.Content {
		item.Content = append(item.Content, struct {
			Type    string `json:"type"`
			Text    string `json:"text"`
			Refusal string `json:"refusal"`
		}{Type: c.Type, Text: c.Text, Refusal: c.Refusal})
	}
	for _, s := range i.Summary {
		item.Summary = append(item.Summary, struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{Type: s.Type, Text: s.Text})
	}
	return responsesEventChunk{Type: "response.output_item.added", OutputIndex: outputIndex, Item: item}
}

// testResponse mirrors the responsesEventChunk.Response inline struct.
type testResponse struct {
	Status string
	Usage  *testUsage
	Error  *testRespError
	Reason string
}

type testUsage struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
	CachedTokens int
}

type testRespError struct {
	Code    string
	Message string
}

func (r testResponse) chunk() responsesEventChunk {
	c := responsesEventChunk{}
	resp := &struct {
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
	}{
		Status: r.Status,
	}
	if r.Usage != nil {
		d := &struct {
			CachedTokens int `json:"cached_tokens"`
		}{CachedTokens: r.Usage.CachedTokens}
		resp.Usage = &struct {
			InputTokens        int `json:"input_tokens"`
			OutputTokens       int `json:"output_tokens"`
			TotalTokens        int `json:"total_tokens"`
			InputTokensDetails *struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"input_tokens_details"`
		}{
			InputTokens:        r.Usage.InputTokens,
			OutputTokens:       r.Usage.OutputTokens,
			TotalTokens:        r.Usage.TotalTokens,
			InputTokensDetails: d,
		}
	}
	if r.Error != nil {
		resp.Error = &struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}{Code: r.Error.Code, Message: r.Error.Message}
	}
	if r.Reason != "" {
		resp.IncompleteDetails = &struct {
			Reason string `json:"reason"`
		}{Reason: r.Reason}
	}
	c.Response = resp
	return c
}

// feed sends a stream event into the parser, ignoring the terminal tuple.
func feed(p *responsesParser, chunk responsesEventChunk) {
	p.handle(chunk)
}

func TestOpenAIResponsesParser(t *testing.T) {
	t.Run("thinking and text deltas accumulate", func(t *testing.T) {
		p := newResponsesParser()
		feed(p, testResponsesItem{Type: "reasoning"}.chunk(0))
		feed(p, testResponsesItem{Type: "message"}.chunk(1))
		feed(p, responsesEventChunk{Type: "response.reasoning_summary_text.delta", OutputIndex: 0, Delta: "let me "})
		feed(p, responsesEventChunk{Type: "response.reasoning_text.delta", OutputIndex: 0, Delta: "think"})
		feed(p, responsesEventChunk{Type: "response.output_text.delta", OutputIndex: 1, Delta: "Hello"})
		feed(p, responsesEventChunk{Type: "response.output_text.delta", OutputIndex: 1, Delta: " world"})

		msg := p.buildMessage()
		if msg.Thinking != "let me think" {
			t.Errorf("Thinking = %q, want %q", msg.Thinking, "let me think")
		}
		if msg.Content != "Hello world" {
			t.Errorf("Content = %q, want %q", msg.Content, "Hello world")
		}
	})

	t.Run("reasoning_summary_part.done appends separator", func(t *testing.T) {
		p := newResponsesParser()
		feed(p, responsesEventChunk{Type: "response.reasoning_summary_text.delta", OutputIndex: 0, Delta: "part1"})
		feed(p, responsesEventChunk{Type: "response.reasoning_summary_part.done", OutputIndex: 0})
		feed(p, responsesEventChunk{Type: "response.reasoning_summary_text.delta", OutputIndex: 0, Delta: "part2"})
		msg := p.buildMessage()
		if msg.Thinking != "part1\n\npart2" {
			t.Errorf("Thinking = %q, want %q", msg.Thinking, "part1\n\npart2")
		}
	})

	t.Run("tool call with output_item.added and arguments deltas", func(t *testing.T) {
		p := newResponsesParser()
		feed(p, testResponsesItem{Type: "function_call", ID: "call_123", Name: "get_weather"}.chunk(0))
		feed(p, responsesEventChunk{Type: "response.function_call_arguments.delta", OutputIndex: 0, Delta: `{"city":`})
		feed(p, responsesEventChunk{Type: "response.function_call_arguments.delta", OutputIndex: 0, Delta: `"Beijing"}`})

		msg := p.buildMessage()
		if len(msg.ToolCalls) != 1 {
			t.Fatalf("expected 1 tool call, got %d", len(msg.ToolCalls))
		}
		tc := msg.ToolCalls[0]
		if tc.ID != "call_123" {
			t.Errorf("ToolCall.ID = %q, want %q", tc.ID, "call_123")
		}
		if tc.Function.Name != "get_weather" {
			t.Errorf("ToolCall.Function.Name = %q, want %q", tc.Function.Name, "get_weather")
		}
		if tc.Function.Arguments != `{"city":"Beijing"}` {
			t.Errorf("ToolCall.Function.Arguments = %q, want %q", tc.Function.Arguments, `{"city":"Beijing"}`)
		}
	})

	t.Run("function_call_arguments.done fills missing tail", func(t *testing.T) {
		p := newResponsesParser()
		feed(p, testResponsesItem{Type: "function_call", ID: "call_9", Name: "search"}.chunk(0))
		// Proxy may deliver the whole arguments in the done event without deltas.
		feed(p, responsesEventChunk{Type: "response.function_call_arguments.done", OutputIndex: 0, Arguments: `{"q":"ai"}`})
		msg := p.buildMessage()
		if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Function.Arguments != `{"q":"ai"}` {
			t.Errorf("tool call arguments not finalized: %+v", msg.ToolCalls)
		}
	})

	t.Run("output_item.done finalizes reasoning summary", func(t *testing.T) {
		p := newResponsesParser()
		feed(p, testResponsesItem{Type: "reasoning"}.chunk(0))
		feed(p, responsesEventChunk{Type: "response.reasoning_summary_text.delta", OutputIndex: 0, Delta: "draft"})
		feed(p, testResponsesItem{Type: "reasoning", Summary: []testSummaryPart{{Type: "summary_text", Text: "final summary"}}}.chunk(0).outputItemDone())

		msg := p.buildMessage()
		if msg.Thinking != "final summary" {
			t.Errorf("Thinking = %q, want %q (reasoning finalized by output_item.done)", msg.Thinking, "final summary")
		}
	})

	t.Run("output_item.done finalizes message text", func(t *testing.T) {
		p := newResponsesParser()
		feed(p, responsesEventChunk{Type: "response.output_text.delta", OutputIndex: 0, Delta: "partial"})
		feed(p, testResponsesItem{Type: "message", Content: []testContentPart{{Type: "output_text", Text: "complete text"}}}.chunk(0).outputItemDone())
		msg := p.buildMessage()
		if msg.Content != "complete text" {
			t.Errorf("Content = %q, want %q (finalized by output_item.done)", msg.Content, "complete text")
		}
	})

	t.Run("terminal events map to stop reasons", func(t *testing.T) {
		if got := mapResponsesStopReason("completed", ""); got != "stop" {
			t.Errorf("completed -> %q, want stop", got)
		}
		if got := mapResponsesStopReason("incomplete", "max_output_tokens"); got != "length" {
			t.Errorf("incomplete/max_output_tokens -> %q, want length", got)
		}
		if got := mapResponsesStopReason("incomplete", "other"); got != "error" {
			t.Errorf("incomplete/other -> %q, want error", got)
		}
		if got := mapResponsesStopReason("failed", ""); got != "error" {
			t.Errorf("failed -> %q, want error", got)
		}
	})

	t.Run("error and failed events return error", func(t *testing.T) {
		p := newResponsesParser()
		_, err := p.handle(responsesEventChunk{Type: "error", Code: "E1", Message: "boom"})
		if err == nil || !strings.Contains(err.Error(), "E1") {
			t.Errorf("error event -> %v, want code included", err)
		}
		_, err = p.handle(responsesEventChunk{
			Type:     "response.failed",
			Response: testResponse{Status: "failed", Error: &testRespError{Code: "rate_limit", Message: "slow down"}}.chunk().Response,
		})
		if err == nil || !strings.Contains(err.Error(), "rate_limit") {
			t.Errorf("failed event -> %v, want error code included", err)
		}
	})

	t.Run("usage extracts with cached subtraction", func(t *testing.T) {
		chunk := testResponse{Status: "completed", Usage: &testUsage{InputTokens: 120, OutputTokens: 30, TotalTokens: 150, CachedTokens: 20}}.chunk()
		u := extractResponsesUsage(chunk)
		if u.InputTokens != 100 || u.OutputTokens != 30 || u.TotalTokens != 150 {
			t.Errorf("usage = %+v, want input=100 output=30 total=150", u)
		}
		if u.PromptTokensDetails == nil || u.PromptTokensDetails.CachedTokens != 20 {
			t.Errorf("cached tokens mismatch: %+v", u.PromptTokensDetails)
		}
	})
}

// outputItemDone converts an added-item chunk into an output_item.done chunk.
func (c responsesEventChunk) outputItemDone() responsesEventChunk {
	c.Type = "response.output_item.done"
	return c
}

// Regression test: assistant tool calls must be emitted as top-level
// function_call items, not chat-completions style nested "tool_calls".
// The nested form makes the upstream provider reject the request with 400.
func TestBuildOpenAIResponsesRequest_ToolCallReplay(t *testing.T) {
	model := Model{ID: "gpt-test"}
	ctx := LLMContext{
		Messages: []LLMMessage{
			{Role: "user", Content: "run ls"},
			{
				Role: "assistant",
				ToolCalls: []ToolCall{
					{ID: "call_1", Function: FunctionCall{Name: "bash", Arguments: `{"command":"ls"}`}},
				},
			},
			{Role: "tool", ToolCallID: "call_1", Content: "file1.txt"},
		},
	}
	req := buildOpenAIResponsesRequest(model, ctx)
	input := req["input"].([]map[string]any)

	if len(input) != 3 {
		t.Fatalf("expected 3 input items, got %d: %v", len(input), input)
	}

	// Item 1: user message
	if input[0]["role"] != "user" {
		t.Errorf("input[0] role = %v, want user", input[0]["role"])
	}

	// Item 2: top-level function_call (no nested tool_calls anywhere)
	if input[1]["type"] != "function_call" {
		t.Errorf("input[1] type = %v, want function_call", input[1]["type"])
	}
	if input[1]["call_id"] != "call_1" {
		t.Errorf("input[1] call_id = %v, want call_1", input[1]["call_id"])
	}
	if input[1]["name"] != "bash" {
		t.Errorf("input[1] name = %v, want bash", input[1]["name"])
	}
	for _, item := range input {
		if _, ok := item["tool_calls"]; ok {
			t.Errorf("input item must not contain nested tool_calls: %v", item)
		}
	}

	// Item 3: function_call_output
	if input[2]["type"] != "function_call_output" {
		t.Errorf("input[2] type = %v, want function_call_output", input[2]["type"])
	}
	if input[2]["call_id"] != "call_1" {
		t.Errorf("input[2] call_id = %v, want call_1", input[2]["call_id"])
	}
}

// Regression test for double accumulation: the streaming loop used to write
// deltas into parser slots directly AND feed them to parser.handle, doubling
// text/thinking/args when output_item.done carried no authoritative content.
// Streams deltas without output_item.done content; the final message must
// contain each delta exactly once.
func TestStreamOpenAIResponses_NoDoubleAccumulation(t *testing.T) {
	var body strings.Builder
	w := func(s string) {
		body.WriteString("data: " + s + "\n\n")
	}
	w(`{"type":"response.output_item.added","output_index":0,"item":{"type":"message","id":"m1"}}`)
	w(`{"type":"response.output_text.delta","output_index":0,"delta":"Hello"}`)
	w(`{"type":"response.output_text.delta","output_index":0,"delta":" world"}`)
	w(`{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}}`)

	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		rw.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(rw, body.String())
	}))
	defer srv.Close()

	model := Model{ID: "gpt-test", BaseURL: srv.URL, API: "openai-responses"}
	stream := StreamOpenAIResponses(context.Background(), model, LLMContext{}, "k", 5*time.Second)
	var msg LLMMessage
	var streamErr error
	for it := stream.Iterator(context.Background()); ; {
		r, ok := <-it
		if !ok {
			break
		}
		if r.Done {
			break
		}
		switch e := r.Value.(type) {
		case LLMDoneEvent:
			if e.Message != nil {
				msg = *e.Message
			}
		case LLMErrorEvent:
			streamErr = e.Error
		}
	}
	if streamErr != nil {
		t.Fatalf("stream error: %v", streamErr)
	}
	if msg.Content != "Hello world" {
		t.Errorf("Content = %q, want %q (deltas must accumulate exactly once)", msg.Content, "Hello world")
	}
}

func TestBuildOpenAIResponsesRequest_ReasoningContext(t *testing.T) {
	// Configured reasoningContext must appear on the Responses body even when
	// the model does not declare reasoning effort controls.
	req := buildOpenAIResponsesRequest(Model{ReasoningContext: "all_turns"}, LLMContext{})
	reasoning, ok := req["reasoning"].(map[string]any)
	if !ok {
		t.Fatalf("missing reasoning param, got %#v", req["reasoning"])
	}
	if reasoning["context"] != "all_turns" {
		t.Errorf("reasoning.context = %v, want all_turns", reasoning["context"])
	}

	// Without the capability, no reasoning param is emitted.
	req = buildOpenAIResponsesRequest(Model{}, LLMContext{})
	if _, ok := req["reasoning"]; ok {
		t.Errorf("unexpected reasoning param: %v", req["reasoning"])
	}
}

func TestBuildOpenAIResponsesRequest_ReasoningEffort(t *testing.T) {
	cases := []struct {
		level string
		want  any // nil means no reasoning key
	}{
		{"", "medium"},
		{"low", "low"},
		{"medium", "medium"},
		{"high", "high"},
		{"xhigh", "high"},
		{"off", nil},
	}
	for _, tc := range cases {
		model := Model{Reasoning: true}
		ctx := LLMContext{ThinkingLevel: tc.level}
		req := buildOpenAIResponsesRequest(model, ctx)
		reasoning, ok := req["reasoning"].(map[string]any)
		if tc.want == nil {
			if ok {
				t.Errorf("level %q: expected no reasoning param, got %v", tc.level, req["reasoning"])
			}
			continue
		}
		if !ok {
			t.Errorf("level %q: missing reasoning param", tc.level)
			continue
		}
		if reasoning["effort"] != tc.want {
			t.Errorf("level %q: effort = %v, want %v", tc.level, reasoning["effort"], tc.want)
		}
	}
}
