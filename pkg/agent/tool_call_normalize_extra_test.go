package agent

import (
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

func TestInferToolFromArgs_Branches(t *testing.T) {
	tests := []struct {
		name      string
		args      map[string]any
		wantTool  string
		wantOK    bool
		checkArgs func(t *testing.T, args map[string]any)
	}{
		{
			name:     "nil args no match",
			args:     nil,
			wantTool: "",
			wantOK:   false,
		},
		{
			name:     "empty args no match",
			args:     map[string]any{},
			wantTool: "",
			wantOK:   false,
		},
		{
			name:     "bash from command",
			args:     map[string]any{"command": "ls -la"},
			wantTool: "bash",
			wantOK:   true,
			checkArgs: func(t *testing.T, args map[string]any) {
				if args["command"] != "ls -la" {
					t.Errorf("command = %v, want 'ls -la'", args["command"])
				}
			},
		},
		{
			name:     "bash from cmd alias",
			args:     map[string]any{"cmd": "pwd"},
			wantTool: "bash",
			wantOK:   true,
		},
		{
			name:     "grep from pattern",
			args:     map[string]any{"pattern": "foo"},
			wantTool: "grep",
			wantOK:   true,
			checkArgs: func(t *testing.T, args map[string]any) {
				if args["pattern"] != "foo" {
					t.Errorf("pattern = %v, want 'foo'", args["pattern"])
				}
			},
		},
		{
			name:     "grep from query alias with path and filePattern",
			args:     map[string]any{"query": "foo", "path": "/src", "filePattern": "*.go"},
			wantTool: "grep",
			wantOK:   true,
			checkArgs: func(t *testing.T, args map[string]any) {
				if args["path"] != "/src" || args["filePattern"] != "*.go" {
					t.Errorf("grep args = %v, want path=/src filePattern=*.go", args)
				}
			},
		},
		{
			name:     "write from path+content",
			args:     map[string]any{"path": "/tmp/f", "content": "data"},
			wantTool: "write",
			wantOK:   true,
		},
		{
			name:     "write from file+text aliases",
			args:     map[string]any{"file": "/tmp/f", "text": "data"},
			wantTool: "write",
			wantOK:   true,
		},
		{
			name:     "edit from path+oldText+newText",
			args:     map[string]any{"path": "/tmp/f", "oldText": "a", "newText": "b"},
			wantTool: "edit",
			wantOK:   true,
		},
		{
			name:     "edit from old alias",
			args:     map[string]any{"path": "/tmp/f", "old": "a", "new": "b"},
			wantTool: "edit",
			wantOK:   true,
		},
		{
			name:     "read from path only",
			args:     map[string]any{"path": "/tmp/f"},
			wantTool: "read",
			wantOK:   true,
		},
		{
			name:     "read from file alias",
			args:     map[string]any{"file": "/tmp/f"},
			wantTool: "read",
			wantOK:   true,
		},
		{
			name:     "name hint wins over shape",
			args:     map[string]any{"name": "read", "path": "/tmp/f", "command": "ls"},
			wantTool: "read",
			wantOK:   true,
		},
		{
			name:     "generic name hint falls back to shape",
			args:     map[string]any{"tool": "tool", "command": "ls"},
			wantTool: "bash",
			wantOK:   true,
		},
		{
			name:     "nested arguments wrapper",
			args:     map[string]any{"arguments": map[string]any{"command": "ls"}},
			wantTool: "bash",
			wantOK:   true,
		},
		{
			name:     "nested input wrapper",
			args:     map[string]any{"input": map[string]any{"path": "/f"}},
			wantTool: "read",
			wantOK:   true,
		},
		{
			name:     "name hint via function_name alias",
			args:     map[string]any{"function_name": "grep", "pattern": "x"},
			wantTool: "grep",
			wantOK:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool, args, ok := inferToolFromArgs(tt.args)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if tool != tt.wantTool {
				t.Fatalf("tool = %q, want %q", tool, tt.wantTool)
			}
			if tt.checkArgs != nil && args != nil {
				tt.checkArgs(t, args)
			}
		})
	}
}

func TestGetStringArg_Coercion(t *testing.T) {
	// Non-string values are coerced via fmt.Sprint.
	args := map[string]any{"count": 42, "nil_val": nil, "empty": "", "blank": "   "}
	if got := getStringArg(args, "count"); got != "42" {
		t.Errorf("count = %q, want '42'", got)
	}
	// nil coerces to "<nil>" which is filtered out.
	if got := getStringArg(args, "nil_val"); got != "" {
		t.Errorf("nil_val = %q, want empty", got)
	}
	// Empty string treated as missing.
	if got := getStringArg(args, "empty"); got != "" {
		t.Errorf("empty = %q, want empty", got)
	}
	// Whitespace-only trimmed to empty.
	if got := getStringArg(args, "blank"); got != "" {
		t.Errorf("blank = %q, want empty", got)
	}
	// Key absent falls through to next key.
	if got := getStringArg(args, "missing", "count"); got != "42" {
		t.Errorf("fallback = %q, want '42'", got)
	}
	// Value is trimmed.
	if got := getStringArg(map[string]any{"p": "  /tmp/x  "}, "p"); got != "/tmp/x" {
		t.Errorf("trimmed = %q, want '/tmp/x'", got)
	}
}

func TestUnwrapPropertiesArguments_EdgeCases(t *testing.T) {
	// nil stays nil.
	if got := unwrapPropertiesArguments(nil); got != nil {
		t.Errorf("nil case = %v, want nil", got)
	}
	// No properties key returns original map.
	orig := map[string]any{"a": 1}
	if got := unwrapPropertiesArguments(orig); len(got) != 1 || got["a"] != 1 {
		t.Errorf("no-properties case = %v", got)
	}
	// Empty properties map returns original.
	if got := unwrapPropertiesArguments(map[string]any{"properties": map[string]any{}}); len(got) != 1 {
		t.Errorf("empty-properties case = %v, got %d keys", got, len(got))
	}
	// Properties map unwrapped.
	got := unwrapPropertiesArguments(map[string]any{
		"properties": map[string]any{"path": "/f"},
	})
	if got["path"] != "/f" {
		t.Errorf("map unwrap = %v", got)
	}
	// Properties as JSON string unwrapped.
	got = unwrapPropertiesArguments(map[string]any{
		"properties": `{"path":"/g"}`,
	})
	if got["path"] != "/g" {
		t.Errorf("string unwrap = %v", got)
	}
	// Empty properties string returns original.
	emptyStr := unwrapPropertiesArguments(map[string]any{"properties": "  "})
	if _, ok := emptyStr["properties"]; !ok {
		t.Errorf("empty string case should return original map: %v", emptyStr)
	}
	// Invalid JSON string returns original.
	invalid := unwrapPropertiesArguments(map[string]any{"properties": "not-json"})
	if _, ok := invalid["properties"]; !ok {
		t.Errorf("invalid JSON case should return original map: %v", invalid)
	}
	// Non-map non-string type returns original.
	weird := unwrapPropertiesArguments(map[string]any{"properties": 42})
	if _, ok := weird["properties"]; !ok {
		t.Errorf("non-map case should return original map: %v", weird)
	}
}

func TestNormalizeToolCallName_MoreAliases(t *testing.T) {
	cases := map[string]string{
		"Read_File": "read",
		"READFILE":  "read",
		"WriteFile": "write",
		"EditFile":  "edit",
		"shell":     "bash",
		"sh":        "bash",
		"command":   "bash",
		"ripgrep":   "grep",
		"search":    "grep",
		"custom":    "custom",
	}
	for in, want := range cases {
		if got := normalizeToolCallName(in); got != want {
			t.Errorf("normalizeToolCallName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeToolCall_NilArgumentsGetEmptyMap(t *testing.T) {
	normalized := normalizeToolCall(agentctx.ToolCallContent{
		ID:   "t1",
		Type: "toolCall",
		Name: "read",
	})
	if normalized.Arguments == nil {
		t.Error("normalized.Arguments should be non-nil empty map")
	}
	if normalized.ID == "" {
		t.Error("ID should be filled in")
	}
}

func TestSanitizeToolCallID_EdgeCases(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"   ", ""},
		{"___abc___", "abc"},
		{"call-123", "call-123"},
		{"a b c", "abc"},
		{"中文id", "id"},
	}
	for _, tt := range cases {
		if got := sanitizeToolCallID(tt.in); got != tt.want {
			t.Errorf("sanitizeToolCallID(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	// >64 chars truncated.
	long := make([]byte, 100)
	for i := range long {
		long[i] = 'a'
	}
	if got := sanitizeToolCallID(string(long)); len(got) != 64 {
		t.Errorf("long id len = %d, want 64", len(got))
	}
	// ensureToolCallID generates an id for empty input.
	gen := ensureToolCallID("")
	if gen == "" {
		t.Error("ensureToolCallID(\"\") should generate a non-empty id")
	}
	if len(gen) > 64 {
		t.Errorf("generated id exceeds 64 chars: %q", gen)
	}
}
