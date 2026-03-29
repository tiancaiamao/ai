package llm

import "testing"

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
