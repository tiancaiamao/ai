package agent

import (
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"testing"
)

func TestNormalizeToolCallInfersGenericWrapperName(t *testing.T) {
	tests := []struct {
		name     string
		input    agentctx.ToolCallContent
		wantName string
	}{
		{
			name: "infer read from path",
			input: agentctx.ToolCallContent{
				Name:      "tool_call",
				Arguments: map[string]any{"path": "/tmp/a.txt"},
			},
			wantName: "read",
		},
		{
			name: "infer bash from command",
			input: agentctx.ToolCallContent{
				Name:      "tool",
				Arguments: map[string]any{"command": "ls -la"},
			},
			wantName: "bash",
		},
		{
			name: "unwrap nested arguments",
			input: agentctx.ToolCallContent{
				Name: "tool_call",
				Arguments: map[string]any{
					"name": "write",
					"arguments": map[string]any{
						"path":    "/tmp/a.txt",
						"content": "hello",
					},
				},
			},
			wantName: "write",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeToolCall(tt.input)
			if got.Name != tt.wantName {
				t.Fatalf("normalizeToolCall() name=%q want=%q", got.Name, tt.wantName)
			}
			if got.ID == "" {
				t.Fatalf("normalizeToolCall() should always assign ID")
			}
		})
	}
}

func TestNormalizeToolCallUnwrapsPropertiesStringForWrite(t *testing.T) {
	got := normalizeToolCall(agentctx.ToolCallContent{
		Name: "write",
		Arguments: map[string]any{
			"properties": `{"path":"/tmp/a.txt","content":"hello world"}`,
		},
	})

	args, err := coerceToolArguments(got.Name, got.Arguments)
	if err != nil {
		t.Fatalf("coerceToolArguments returned error: %v", err)
	}

	if args["path"] != "/tmp/a.txt" {
		t.Fatalf("expected path=/tmp/a.txt, got %v", args["path"])
	}
	if args["content"] != "hello world" {
		t.Fatalf("expected content=hello world, got %v", args["content"])
	}
}

func TestNormalizeToolCallUnwrapsPropertiesMapForWrite(t *testing.T) {
	got := normalizeToolCall(agentctx.ToolCallContent{
		Name: "write",
		Arguments: map[string]any{
			"properties": map[string]any{
				"path":    "/tmp/b.txt",
				"content": "abc",
			},
		},
	})

	args, err := coerceToolArguments(got.Name, got.Arguments)
	if err != nil {
		t.Fatalf("coerceToolArguments returned error: %v", err)
	}

	if args["path"] != "/tmp/b.txt" {
		t.Fatalf("expected path=/tmp/b.txt, got %v", args["path"])
	}
	if args["content"] != "abc" {
		t.Fatalf("expected content=abc, got %v", args["content"])
	}
}
