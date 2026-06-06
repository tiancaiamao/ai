package llm

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestBuildAnthropicRequestUsesConfiguredMaxTokens(t *testing.T) {
	req := buildAnthropicRequest(Model{
		ID:        "test-model",
		MaxTokens: 123456,
	}, LLMContext{
		Messages: []LLMMessage{
			{Role: "user", Content: "hello"},
		},
	})

	got, ok := req["max_tokens"].(int)
	if !ok {
		t.Fatalf("expected int max_tokens, got %T", req["max_tokens"])
	}
	if got != 123456 {
		t.Fatalf("expected max_tokens=123456, got %d", got)
	}
}

func TestBuildAnthropicRequestUsesLargeDefaultMaxTokens(t *testing.T) {
	req := buildAnthropicRequest(Model{
		ID: "test-model",
	}, LLMContext{
		Messages: []LLMMessage{
			{Role: "user", Content: "hello"},
		},
	})

	got, ok := req["max_tokens"].(int)
	if !ok {
		t.Fatalf("expected int max_tokens, got %T", req["max_tokens"])
	}
	if got != defaultAnthropicMaxTokens {
		t.Fatalf("expected max_tokens=%d, got %d", defaultAnthropicMaxTokens, got)
	}
}

func TestBuildAnthropicRequestKeepsSchemaWithoutDoubleWrapping(t *testing.T) {
	req := buildAnthropicRequest(Model{ID: "test-model"}, LLMContext{
		Messages: []LLMMessage{{Role: "user", Content: "hello"}},
		Tools: []LLMTool{
			{
				Type: "function",
				Function: ToolFunction{
					Name:        "write",
					Description: "write file",
					Parameters: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"path": map[string]any{"type": "string"},
						},
						"required": []string{"path"},
					},
				},
			},
		},
	})

	tools, ok := req["tools"].([]map[string]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("expected one tool schema, got %#v", req["tools"])
	}
	inputSchema, ok := tools[0]["input_schema"].(map[string]any)
	if !ok {
		t.Fatalf("expected input_schema map, got %#v", tools[0]["input_schema"])
	}

	props, ok := inputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected input_schema.properties map, got %#v", inputSchema["properties"])
	}
	if _, hasNestedProperties := props["properties"]; hasNestedProperties {
		t.Fatalf("unexpected nested properties in input_schema: %#v", inputSchema)
	}
	if _, ok := props["path"]; !ok {
		t.Fatalf("expected path in input_schema.properties, got %#v", props)
	}
}

// Cover resolveAnthropicMaxTokens: configured > 0 takes precedence, otherwise default.
func TestResolveAnthropicMaxTokens(t *testing.T) {
	if got := resolveAnthropicMaxTokens(Model{MaxTokens: 0}); got != defaultAnthropicMaxTokens {
		t.Fatalf("expected default %d, got %d", defaultAnthropicMaxTokens, got)
	}
	if got := resolveAnthropicMaxTokens(Model{MaxTokens: 7}); got != 7 {
		t.Fatalf("expected 7, got %d", got)
	}
}

// Cover normalizeAnthropicInputSchema: nil, already-typed, and bare properties.
func TestNormalizeAnthropicInputSchema(t *testing.T) {
	t.Run("nil becomes empty object", func(t *testing.T) {
		got := normalizeAnthropicInputSchema(nil)
		if got["type"] != "object" {
			t.Fatalf("expected type=object, got %#v", got)
		}
		props, ok := got["properties"].(map[string]any)
		if !ok || len(props) != 0 {
			t.Fatalf("expected empty properties map, got %#v", got["properties"])
		}
	})

	t.Run("has type already", func(t *testing.T) {
		in := map[string]any{"type": "string"}
		got := normalizeAnthropicInputSchema(in)
		if got["type"] != "string" {
			t.Fatalf("expected passthrough, got %#v", got)
		}
	})

	t.Run("has properties already", func(t *testing.T) {
		in := map[string]any{"properties": map[string]any{"x": 1}}
		got := normalizeAnthropicInputSchema(in)
		if _, ok := got["properties"]; !ok {
			t.Fatalf("expected passthrough, got %#v", got)
		}
	})

	t.Run("has required already", func(t *testing.T) {
		in := map[string]any{"required": []string{"x"}}
		got := normalizeAnthropicInputSchema(in)
		if _, ok := got["required"]; !ok {
			t.Fatalf("expected passthrough, got %#v", got)
		}
	})

	t.Run("has additionalProperties", func(t *testing.T) {
		in := map[string]any{"additionalProperties": false}
		got := normalizeAnthropicInputSchema(in)
		if _, ok := got["additionalProperties"]; !ok {
			t.Fatalf("expected passthrough, got %#v", got)
		}
	})

	t.Run("bare params wrapped as properties", func(t *testing.T) {
		in := map[string]any{"path": map[string]any{"type": "string"}}
		got := normalizeAnthropicInputSchema(in)
		if got["type"] != "object" {
			t.Fatalf("expected wrapped type=object, got %#v", got)
		}
		props, ok := got["properties"].(map[string]any)
		if !ok {
			t.Fatalf("expected properties map, got %#v", got["properties"])
		}
		if _, ok := props["path"]; !ok {
			t.Fatalf("expected path inside properties, got %#v", props)
		}
	})
}

// Cover convertToolResultContent across simple text, multi-part, and single-part cases.
func TestConvertToolResultContent(t *testing.T) {
	t.Run("plain string content", func(t *testing.T) {
		got := convertToolResultContent(LLMMessage{Content: "hello"})
		s, ok := got.(string)
		if !ok || s != "hello" {
			t.Fatalf("expected string 'hello', got %#v", got)
		}
	})

	t.Run("single text part collapses to string", func(t *testing.T) {
		got := convertToolResultContent(LLMMessage{
			ContentParts: []ContentPart{{Type: "text", Text: "single"}},
		})
		s, ok := got.(string)
		if !ok || s != "single" {
			t.Fatalf("expected collapsed string 'single', got %#v", got)
		}
	})

	t.Run("multiple text parts returns array", func(t *testing.T) {
		got := convertToolResultContent(LLMMessage{
			ContentParts: []ContentPart{
				{Type: "text", Text: "a"},
				{Type: "text", Text: "b"},
			},
		})
		blocks, ok := got.([]map[string]any)
		if !ok {
			t.Fatalf("expected []map[string]any, got %T", got)
		}
		if len(blocks) != 2 {
			t.Fatalf("expected 2 blocks, got %d", len(blocks))
		}
		if blocks[0]["text"] != "a" || blocks[1]["text"] != "b" {
			t.Fatalf("unexpected blocks: %#v", blocks)
		}
	})

	t.Run("non-text part yields empty array", func(t *testing.T) {
		got := convertToolResultContent(LLMMessage{
			ContentParts: []ContentPart{{Type: "image_url", ImageURL: &struct {
				URL string `json:"url"`
			}{URL: "u"}}},
		})
		blocks, ok := got.([]map[string]any)
		if !ok {
			t.Fatalf("expected []map[string]any, got %T", got)
		}
		if len(blocks) != 0 {
			t.Fatalf("expected 0 blocks for image-only, got %d", len(blocks))
		}
	})
}

// Cover mapAnthropicStopReason: known mappings + default passthrough.
func TestMapAnthropicStopReason(t *testing.T) {
	tests := []struct{ in, want string }{
		{"end_turn", "stop"},
		{"max_tokens", "length"},
		{"tool_use", "toolUse"},
		{"stop_sequence", "stop"},
		{"unknown_thing", "unknown_thing"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := mapAnthropicStopReason(tt.in); got != tt.want {
			t.Errorf("mapAnthropicStopReason(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// Cover parseXMLTagStyleArguments for both recognized and unrecognized shapes.
// Note: the implementation keeps a leading quote on keys and a leading ">" on
// values — we assert against actual behavior, not a hypothetical fix.
func TestParseXMLTagStyleArguments(t *testing.T) {
	t.Run("not the expected wrapper", func(t *testing.T) {
		got := parseXMLTagStyleArguments(`{"foo":"bar"}`)
		if len(got) != 0 {
			t.Fatalf("expected empty map, got %#v", got)
		}
	})

	t.Run("empty wrapper", func(t *testing.T) {
		got := parseXMLTagStyleArguments(`{"properties": ""}`)
		if len(got) != 0 {
			t.Fatalf("expected empty map, got %#v", got)
		}
	})

	t.Run("single key-value", func(t *testing.T) {
		input := `{"properties": "{\"path\">value"}`
		got := parseXMLTagStyleArguments(input)
		if len(got) != 1 {
			t.Fatalf("expected 1 entry, got %#v", got)
		}
		// The parser's actual behavior: key retains leading quote, value retains leading ">".
		if v, ok := got[`"path`]; !ok || v != ">value" {
			t.Fatalf("expected '\"path'='>value', got %#v", got)
		}
	})

	t.Run("malformed inner no arrow", func(t *testing.T) {
		// Inner has no `">` so nothing is added.
		got := parseXMLTagStyleArguments(`{"properties": "{\"foo\"}"}`)
		if len(got) != 0 {
			t.Fatalf("expected empty map (no arrow), got %#v", got)
		}
	})
}

// Cover parseRetryAfterHeaderAnthropic — seconds, http-date, invalid, empty.
func TestParseRetryAfterHeaderAnthropic(t *testing.T) {
	if got := parseRetryAfterHeaderAnthropic(""); got != 0 {
		t.Fatalf("expected 0 for empty, got %v", got)
	}
	if got := parseRetryAfterHeaderAnthropic("   "); got != 0 {
		t.Fatalf("expected 0 for whitespace, got %v", got)
	}
	if got := parseRetryAfterHeaderAnthropic("10"); got != 10*time.Second {
		t.Fatalf("expected 10s, got %v", got)
	}
	if got := parseRetryAfterHeaderAnthropic("0"); got != 0 {
		t.Fatalf("expected 0 for non-positive seconds, got %v", got)
	}
	if got := parseRetryAfterHeaderAnthropic("-5"); got != 0 {
		t.Fatalf("expected 0 for negative seconds, got %v", got)
	}
	future := time.Now().Add(2 * time.Second).UTC().Format(http.TimeFormat)
	if got := parseRetryAfterHeaderAnthropic(future); got <= 0 {
		t.Fatalf("expected positive duration for http-date, got %v", got)
	}
	past := time.Now().Add(-2 * time.Second).UTC().Format(http.TimeFormat)
	if got := parseRetryAfterHeaderAnthropic(past); got != 0 {
		t.Fatalf("expected 0 for past http-date, got %v", got)
	}
	if got := parseRetryAfterHeaderAnthropic("not-a-date"); got != 0 {
		t.Fatalf("expected 0 for invalid, got %v", got)
	}
}

// Cover more buildAnthropicRequest branches: system prompt + system message,
// assistant message with tool calls, empty user content placeholder,
// consecutive tool result merging, and tools serialization.
func TestBuildAnthropicRequestComprehensive(t *testing.T) {
	t.Run("system prompt and system message", func(t *testing.T) {
		req := buildAnthropicRequest(Model{ID: "m"}, LLMContext{
			SystemPrompt: "system-prompt",
			Messages: []LLMMessage{
				{Role: "system", Content: "system-msg"},
				{Role: "user", Content: "hi"},
			},
		})
		system, ok := req["system"].([]map[string]any)
		if !ok {
			t.Fatalf("expected system []map, got %T", req["system"])
		}
		if len(system) != 2 {
			t.Fatalf("expected 2 system blocks, got %d", len(system))
		}
		// First block comes from SystemPrompt, second from the "system" message.
		if system[0]["text"] != "system-prompt" {
			t.Fatalf("expected first system block to be SystemPrompt, got %v", system[0]["text"])
		}
		if system[1]["text"] != "system-msg" {
			t.Fatalf("expected second system block to be system message, got %v", system[1]["text"])
		}
	})

	t.Run("assistant with tool calls", func(t *testing.T) {
		req := buildAnthropicRequest(Model{ID: "m"}, LLMContext{
			Messages: []LLMMessage{
				{Role: "user", Content: "q"},
				{
					Role:    "assistant",
					Content: "thinking...",
					ToolCalls: []ToolCall{
						{
							ID:   "tc1",
							Type: "function",
							Function: FunctionCall{
								Name:      "read",
								Arguments: `{"path":"a.go"}`,
							},
						},
					},
				},
			},
		})
		messages, ok := req["messages"].([]map[string]any)
		if !ok || len(messages) != 2 {
			t.Fatalf("expected 2 messages, got %#v", req["messages"])
		}
		asst, ok := messages[1]["content"].([]map[string]any)
		if !ok {
			t.Fatalf("expected assistant content array, got %T", messages[1]["content"])
		}
		// Expect 1 text + 1 tool_use.
		var sawText, sawToolUse bool
		for _, b := range asst {
			if b["type"] == "text" && b["text"] == "thinking..." {
				sawText = true
			}
			if b["type"] == "tool_use" {
				sawToolUse = true
				if b["name"] != "read" || b["id"] != "tc1" {
					t.Fatalf("unexpected tool_use block: %#v", b)
				}
				input, _ := b["input"].(map[string]any)
				if input["path"] != "a.go" {
					t.Fatalf("unexpected input: %#v", input)
				}
			}
		}
		if !sawText || !sawToolUse {
			t.Fatalf("expected both text and tool_use blocks: %#v", asst)
		}
	})

	t.Run("assistant with nested properties arguments", func(t *testing.T) {
		// MiniMax-style nested {"properties":"{\"x\":1}"}
		req := buildAnthropicRequest(Model{ID: "m"}, LLMContext{
			Messages: []LLMMessage{
				{Role: "user", Content: "q"},
				{
					Role: "assistant",
					ToolCalls: []ToolCall{
						{
							ID:   "tc1",
							Type: "function",
							Function: FunctionCall{
								Name:      "search",
								Arguments: `{"properties":"{\"q\":\"foo\"}"}`,
							},
						},
					},
				},
			},
		})
		messages, _ := req["messages"].([]map[string]any)
		asst, _ := messages[1]["content"].([]map[string]any)
		for _, b := range asst {
			if b["type"] == "tool_use" {
				input, _ := b["input"].(map[string]any)
				if input["q"] != "foo" {
					t.Fatalf("expected nested-prop unpacked to q=foo, got %#v", input)
				}
			}
		}
	})

	t.Run("assistant with invalid JSON arguments", func(t *testing.T) {
		// When the outer JSON parse fails, argsObj becomes an empty map.
		req := buildAnthropicRequest(Model{ID: "m"}, LLMContext{
			Messages: []LLMMessage{
				{Role: "user", Content: "q"},
				{
					Role: "assistant",
					ToolCalls: []ToolCall{
						{
							ID:   "tc1",
							Type: "function",
							Function: FunctionCall{
								Name:      "broken",
								Arguments: "not-json",
							},
						},
					},
				},
			},
		})
		messages, _ := req["messages"].([]map[string]any)
		asst, _ := messages[1]["content"].([]map[string]any)
		for _, b := range asst {
			if b["type"] == "tool_use" {
				input, _ := b["input"].(map[string]any)
				if len(input) != 0 {
					t.Fatalf("expected empty input map for bad JSON, got %#v", input)
				}
			}
		}
	})

	t.Run("empty user content gets placeholder", func(t *testing.T) {
		req := buildAnthropicRequest(Model{ID: "m"}, LLMContext{
			Messages: []LLMMessage{{Role: "user", Content: ""}},
		})
		messages, _ := req["messages"].([]map[string]any)
		if messages[0]["content"] != "..." {
			t.Fatalf("expected placeholder, got %v", messages[0]["content"])
		}
	})

	t.Run("consecutive tool results merged", func(t *testing.T) {
		req := buildAnthropicRequest(Model{ID: "m"}, LLMContext{
			Messages: []LLMMessage{
				{Role: "user", Content: "q"},
				{Role: "toolResult", ToolCallID: "tc1", Content: "r1"},
				{Role: "toolResult", ToolCallID: "tc2", Content: "r2"},
				{Role: "user", Content: "more"},
			},
		})
		messages, _ := req["messages"].([]map[string]any)
		// Expected: user(q), user(2 tool_results), user(more) = 3 messages.
		if len(messages) != 3 {
			t.Fatalf("expected 3 messages after merge, got %d (%#v)", len(messages), messages)
		}
		results, ok := messages[1]["content"].([]map[string]any)
		if !ok || len(results) != 2 {
			t.Fatalf("expected 2 tool results merged, got %#v", messages[1]["content"])
		}
		if results[0]["tool_use_id"] != "tc1" || results[1]["tool_use_id"] != "tc2" {
			t.Fatalf("unexpected tool_use_ids: %#v", results)
		}
	})

	t.Run("tools serialized with auto tool_choice", func(t *testing.T) {
		req := buildAnthropicRequest(Model{ID: "m"}, LLMContext{
			Messages: []LLMMessage{{Role: "user", Content: "hi"}},
			Tools: []LLMTool{
				{
					Type: "function",
					Function: ToolFunction{
						Name:        "do_thing",
						Description: "does it",
						Parameters:  map[string]any{"path": map[string]any{"type": "string"}},
					},
				},
			},
		})
		tools, ok := req["tools"].([]map[string]any)
		if !ok || len(tools) != 1 {
			t.Fatalf("expected 1 tool, got %#v", req["tools"])
		}
		if tools[0]["name"] != "do_thing" {
			t.Fatalf("unexpected tool name: %v", tools[0]["name"])
		}
		tc, ok := req["tool_choice"].(map[string]any)
		if !ok || tc["type"] != "auto" {
			t.Fatalf("expected tool_choice auto, got %#v", req["tool_choice"])
		}
	})
}

// StreamAnthropic must reject immediately when no API key is available.
// This covers the early-return branch at very low cost — no HTTP needed.
func TestStreamAnthropicMissingAPIKey(t *testing.T) {
	t.Setenv("ZAI_API_KEY", "")
	stream := StreamAnthropic(
		context.Background(),
		Model{ID: "m", BaseURL: "http://localhost:1", API: "anthropic-messages"},
		LLMContext{Messages: []LLMMessage{{Role: "user", Content: "hi"}}},
		"",
		0,
	)

	var sawErr bool
	for item := range stream.Iterator(context.Background()) {
		if e, ok := item.Value.(LLMErrorEvent); ok {
			sawErr = true
			if !strings.Contains(e.Error.Error(), "ZAI_API_KEY") {
				t.Fatalf("unexpected error: %v", e.Error)
			}
		}
	}
	if !sawErr {
		t.Fatal("expected error event for missing API key")
	}
}
