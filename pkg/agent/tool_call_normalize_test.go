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

func TestCoercedToolArgs_EditEmptyNewText(t *testing.T) {
	tests := []struct {
		name    string
		args    map[string]any
		wantErr bool
		wantNew string
	}{
		{
			name:    "empty newText should succeed (deletion)",
			args:    map[string]any{"path": "/tmp/a.txt", "oldText": "remove me", "newText": ""},
			wantErr: false,
			wantNew: "",
		},
		{
			name:    "missing newText key should fail",
			args:    map[string]any{"path": "/tmp/a.txt", "oldText": "remove me"},
			wantErr: true,
		},
		{
			name:    "whitespace-only newText should succeed",
			args:    map[string]any{"path": "/tmp/a.txt", "oldText": "remove me", "newText": "  "},
			wantErr: false,
			wantNew: "  ",
		},
		{
			name:    "non-empty newText should succeed as before",
			args:    map[string]any{"path": "/tmp/a.txt", "oldText": "old", "newText": "new"},
			wantErr: false,
			wantNew: "new",
		},
		{
			name:    "missing path should fail",
			args:    map[string]any{"oldText": "old", "newText": "new"},
			wantErr: true,
		},
		{
			name:    "missing oldText should fail",
			args:    map[string]any{"path": "/tmp/a.txt", "newText": ""},
			wantErr: true,
		},
		{
			name:    "new_text alias with empty string should succeed",
			args:    map[string]any{"path": "/tmp/a.txt", "oldText": "old", "new_text": ""},
			wantErr: false,
			wantNew: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := coerceToolArguments("edit", tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("coerceToolArguments() error=%v wantErr=%v", err, tt.wantErr)
			}
			if !tt.wantErr {
				if got["newText"] != tt.wantNew {
					t.Fatalf("expected newText=%q, got %q", tt.wantNew, got["newText"])
				}
				if got["path"] == "" || got["oldText"] == "" {
					t.Fatalf("path and oldText should be preserved, got=%v", got)
				}
			}
		})
	}
}

func TestInferToolFromArgs_EditWithEmptyNewText(t *testing.T) {
	// Verify that inferToolFromArgs correctly infers "edit" when newText is empty
	name, args, ok := inferToolFromArgs(map[string]any{
		"path": "/tmp/a.txt", "oldText": "old", "newText": "",
	})
	if !ok {
		t.Fatalf("expected inference to succeed")
	}
	if name != "edit" {
		t.Fatalf("expected tool=edit, got %q", name)
	}
	if args["newText"] != "" {
		t.Fatalf("expected newText to be empty, got %q", args["newText"])
	}
}

func TestGetOptionalStringArg(t *testing.T) {
	tests := []struct {
		name      string
		args      map[string]any
		keys      []string
		wantVal   string
		wantFound bool
	}{
		{
			name:      "key present with empty string",
			args:      map[string]any{"newText": ""},
			keys:      []string{"newText", "new_text"},
			wantVal:   "",
			wantFound: true,
		},
		{
			name:      "key absent",
			args:      map[string]any{"path": "x"},
			keys:      []string{"newText", "new_text"},
			wantVal:   "",
			wantFound: false,
		},
		{
			name:      "key present with whitespace",
			args:      map[string]any{"newText": "  "},
			keys:      []string{"newText"},
			wantVal:   "  ",
			wantFound: true,
		},
		{
			name:      "alias key used",
			args:      map[string]any{"new_text": "hello"},
			keys:      []string{"newText", "new_text"},
			wantVal:   "hello",
			wantFound: true,
		},
		{
			name:      "non-string value coerced",
			args:      map[string]any{"newText": 42},
			keys:      []string{"newText"},
			wantVal:   "42",
			wantFound: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			val, found := getOptionalStringArg(tt.args, tt.keys...)
			if val != tt.wantVal || found != tt.wantFound {
				t.Fatalf("getOptionalStringArg() = (%q, %v), want (%q, %v)", val, found, tt.wantVal, tt.wantFound)
			}
		})
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

func TestCoerceToolArgs_ReadPreservesOffsetLimit(t *testing.T) {
	args, err := coerceToolArguments("read", map[string]any{
		"path":   "/tmp/a.txt",
		"offset": float64(10),
		"limit":  float64(20),
	})
	if err != nil {
		t.Fatalf("coerceToolArguments returned error: %v", err)
	}

	if args["path"] != "/tmp/a.txt" {
		t.Fatalf("expected path=/tmp/a.txt, got %v", args["path"])
	}
	if args["offset"] != float64(10) {
		t.Fatalf("expected offset=10, got %v", args["offset"])
	}
	if args["limit"] != float64(20) {
		t.Fatalf("expected limit=20, got %v", args["limit"])
	}
}
