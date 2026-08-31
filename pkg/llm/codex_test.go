package llm

import (
	"encoding/json"
	"testing"
)

func TestBuildCodexToolsUsesResponsesToolShape(t *testing.T) {
	tools := buildCodexTools([]LLMTool{{
		Type: "function",
		Function: ToolFunction{
			Name:        "read",
			Description: "Read a file",
			Parameters: map[string]any{
				"type": "object",
			},
		},
	}})

	if len(tools) != 1 {
		t.Fatalf("got %d tools, want 1", len(tools))
	}
	encoded, err := json.Marshal(tools[0])
	if err != nil {
		t.Fatalf("marshal tool: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatalf("decode tool: %v", err)
	}
	if got["name"] != "read" {
		t.Fatalf("name = %v, want read", got["name"])
	}
	if _, nested := got["function"]; nested {
		t.Fatal("Responses API tool must not contain nested function field")
	}
}
