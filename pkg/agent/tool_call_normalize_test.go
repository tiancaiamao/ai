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
