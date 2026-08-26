package agent

import (
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"testing"
)

func TestInjectToolCallsFromTaggedText_Basic(t *testing.T) {
	tests := []struct {
		name     string
		input    agentctx.AgentMessage
		wantCall bool
		callName string
	}{
		{
			name: "simple bash command",
			input: agentctx.AgentMessage{
				Role:    "assistant",
				Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "<bash>git diff HEAD</bash>"}},
			},
			wantCall: true,
			callName: "bash",
		},
		{
			name: "nested bash command",
			input: agentctx.AgentMessage{
				Role:    "assistant",
				Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "<bash>\n<command>git diff HEAD</command>\n</bash>"}},
			},
			wantCall: true,
			callName: "bash",
		},
		{
			name: "read with path",
			input: agentctx.AgentMessage{
				Role:    "assistant",
				Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "<read>\n<path>file.txt</path>\n</read>"}},
			},
			wantCall: true,
			callName: "read",
		},
		{
			name: "write with content",
			input: agentctx.AgentMessage{
				Role:    "assistant",
				Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "<write>\n<path>file.txt</path>\n<content>hello</content>\n</write>"}},
			},
			wantCall: true,
			callName: "write",
		},
		{
			name: "tool_call wrapper with inline name",
			input: agentctx.AgentMessage{
				Role:    "assistant",
				Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "<tool_call>read<arg_key>path</arg_key><arg_value>file.txt</arg_value></tool_call>"}},
			},
			wantCall: true,
			callName: "read",
		},
		{
			name: "tool wrapper with name tag",
			input: agentctx.AgentMessage{
				Role:    "assistant",
				Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "<tool><name>bash</name><arg_key>command</arg_key><arg_value>ls -la</arg_value></tool>"}},
			},
			wantCall: true,
			callName: "bash",
		},
		{
			name: "text without tags",
			input: agentctx.AgentMessage{
				Role:    "assistant",
				Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "Hello, world!"}},
			},
			wantCall: false,
		},
		{
			name: "incomplete tag - should not parse",
			input: agentctx.AgentMessage{
				Role:    "assistant",
				Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "<bash>git diff HEAD"}},
			},
			wantCall: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, injected := injectToolCallsFromTaggedText(tt.input)
			if tt.wantCall && !injected {
				t.Errorf("injectToolCallsFromTaggedText() should have injected tool call")
			}
			if tt.wantCall {
				calls := result.ExtractToolCalls()
				if len(calls) == 0 {
					t.Errorf("injectToolCallsFromTaggedText() injected=true but no calls found")
					return
				}
				if calls[0].Name != tt.callName {
					t.Errorf("injectToolCallsFromTaggedText() name = %v, want %v", calls[0].Name, tt.callName)
				}
			}
		})
	}
}

func TestInjectToolCallsFromTaggedText_WithExistingToolCalls(t *testing.T) {
	msg := agentctx.AgentMessage{
		Role: "assistant",
		Content: []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "Let me run: <bash>ls -la</bash>"},
			agentctx.ToolCallContent{ID: "empty", Name: "", Arguments: map[string]any{}},
		},
	}

	result, injected := injectToolCallsFromTaggedText(msg)
	if !injected {
		t.Errorf("injectToolCallsFromTaggedText() should inject when existing tool calls are empty")
	}

	calls := result.ExtractToolCalls()
	if len(calls) == 0 {
		t.Errorf("injectToolCallsFromTaggedText() should have injected bash call")
	}
	if len(calls) > 0 && calls[0].Name != "bash" {
		t.Errorf("injectToolCallsFromTaggedText() name = %v, want bash", calls[0].Name)
	}
}

func TestInjectToolCallsFromTaggedText_GenericExistingToolCallDoesNotBlockParsing(t *testing.T) {
	msg := agentctx.AgentMessage{
		Role: "assistant",
		Content: []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "Use tool: <tool_call>read<arg_key>path</arg_key><arg_value>file.txt</arg_value></tool_call>"},
			agentctx.ToolCallContent{ID: "generic", Name: "tool_call", Arguments: map[string]any{"path": "wrong.txt"}},
		},
	}

	result, injected := injectToolCallsFromTaggedText(msg)
	if !injected {
		t.Fatalf("expected tagged tool call to be injected")
	}
	calls := result.ExtractToolCalls()
	if len(calls) == 0 {
		t.Fatalf("expected injected call")
	}
	if calls[0].Name != "read" {
		t.Fatalf("expected injected call name read, got %s", calls[0].Name)
	}
}

func TestDetectIncompleteToolCalls(t *testing.T) {
	tests := []struct {
		name       string
		text       string
		wantIssues int
		shouldHave string
	}{
		{
			name:       "complete tool call",
			text:       "<bash>ls -la</bash>",
			wantIssues: 0,
		},
		{
			name:       "unclosed tag",
			text:       "<bash>ls -la",
			wantIssues: 1,
			shouldHave: "unclosed",
		},
		{
			name:       "extra closing tag",
			text:       "</bash>ls -la</bash>",
			wantIssues: 1,
			shouldHave: "closing </bash>",
		},
		{
			name:       "plain text with angle brackets (no false positive)",
			text:       "fn foo<T>() -> Result<T, E> { ... }",
			wantIssues: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issues := DetectIncompleteToolCalls(tt.text)
			if len(issues) != tt.wantIssues {
				t.Errorf("DetectIncompleteToolCalls() issues = %d, want %d", len(issues), tt.wantIssues)
			}
			if tt.shouldHave != "" {
				found := false
				for _, issue := range issues {
					if contains(issue, tt.shouldHave) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("DetectIncompleteToolCalls() should contain '%s', got %v", tt.shouldHave, issues)
				}
			}
		})
	}
}

func TestInjectToolCallsFromTaggedText_LooseArgPairsWithToolHint(t *testing.T) {
	msg := agentctx.AgentMessage{
		Role: "assistant",
		Content: []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "权限错误，tool: bash\n<arg_key>command</arg_key><arg_value>make debug-asan</arg_value>"},
		},
	}

	result, injected := injectToolCallsFromTaggedText(msg)
	if !injected {
		t.Fatal("expected loose arg-key/value call to be injected")
	}
	calls := result.ExtractToolCalls()
	if len(calls) != 1 {
		t.Fatalf("expected exactly one call, got %d", len(calls))
	}
	if calls[0].Name != "bash" {
		t.Fatalf("expected bash call, got %q", calls[0].Name)
	}
	if got := calls[0].Arguments["command"]; got != "make debug-asan" {
		t.Fatalf("expected command arg, got %v", got)
	}
}

func TestInjectToolCallsFromTaggedText_LooseArgPairsInferByArgs(t *testing.T) {
	msg := agentctx.AgentMessage{
		Role: "assistant",
		Content: []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "<arg_key>path</arg_key><arg_value>README.md</arg_value>"},
		},
	}

	result, injected := injectToolCallsFromTaggedText(msg)
	if !injected {
		t.Fatal("expected arg-shape inference to inject call")
	}
	calls := result.ExtractToolCalls()
	if len(calls) != 1 {
		t.Fatalf("expected exactly one call, got %d", len(calls))
	}
	if calls[0].Name != "read" {
		t.Fatalf("expected read call, got %q", calls[0].Name)
	}
	if got := calls[0].Arguments["path"]; got != "README.md" {
		t.Fatalf("expected path arg, got %v", got)
	}
}

func TestInjectToolCallsFromTaggedText_ToolCallTagWithInlineName(t *testing.T) {
	msg := agentctx.AgentMessage{
		Role: "assistant",
		Content: []agentctx.ContentBlock{
			agentctx.TextContent{Type: "text", Text: "我需要查看正确的行。让我使用 sed 命令来查看第1370-1385行：\n<tool_call>bash\n<arg_key>command</arg_key>\n<arg_value>sed -n '1370,1385p' Client/GameInit.cpp</arg_value>\n</tool_call>"},
		},
	}

	result, injected := injectToolCallsFromTaggedText(msg)
	if !injected {
		t.Fatal("expected tool_call tag with inline name to be injected")
	}
	calls := result.ExtractToolCalls()
	if len(calls) != 1 {
		t.Fatalf("expected exactly one call, got %d", len(calls))
	}
	if calls[0].Name != "bash" {
		t.Fatalf("expected bash call, got %q", calls[0].Name)
	}
	if got := calls[0].Arguments["command"]; got != "sed -n '1370,1385p' Client/GameInit.cpp" {
		t.Fatalf("expected command arg, got %v", got)
	}
}

func TestInjectToolCallsFromTaggedText_ReadTagWithOffsetLimit(t *testing.T) {
	msg := agentctx.AgentMessage{
		Role: "assistant",
		Content: []agentctx.ContentBlock{
			agentctx.TextContent{
				Type: "text",
				Text: "<read><path>README.md</path><offset>3</offset><limit>5</limit></read>",
			},
		},
	}

	result, injected := injectToolCallsFromTaggedText(msg)
	if !injected {
		t.Fatal("expected read tag to be injected")
	}
	calls := result.ExtractToolCalls()
	if len(calls) != 1 {
		t.Fatalf("expected exactly one call, got %d", len(calls))
	}
	if calls[0].Name != "read" {
		t.Fatalf("expected read call, got %q", calls[0].Name)
	}
	if got := calls[0].Arguments["path"]; got != "README.md" {
		t.Fatalf("expected path arg, got %v", got)
	}
	if got := calls[0].Arguments["offset"]; got != "3" {
		t.Fatalf("expected offset arg, got %v", got)
	}
	if got := calls[0].Arguments["limit"]; got != "5" {
		t.Fatalf("expected limit arg, got %v", got)
	}
}

// contains is a helper function for string searching in tests.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr ||
		(len(s) >= len(substr) && indexOf(s, substr) >= 0))
}

// indexOf returns the index of substr in s, or -1 if not found.
func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// --- Additional parseToolTag tests ---

func TestParseToolTag_Extended(t *testing.T) {
	tests := []struct {
		name     string
		tagName  string
		body     string
		wantTool string
		wantOK   bool
	}{
		// read_file alias
		{"read_file alias", "read_file", "<path>/tmp/f.txt</path>", "read", true},
		// read with offset+limit
		{"read with offset+limit", "read", "<path>/tmp/f.txt</path><offset>10</offset><limit>5</limit>", "read", true},
		// read missing path
		{"read no path", "read", "no tags here", "", false},
		// write: missing content
		{"write no content", "write", "<path>/tmp/f.txt</path>", "", false},
		// write: missing path
		{"write no path", "write", "<content>hello</content>", "", false},
		// write: using file alias
		{"write file alias", "write", "<file>/tmp/f.txt</file><text>hello</text>", "write", true},
		// edit: missing old
		{"edit no old", "edit", "<path>a.txt</path><newText>b</newText>", "", false},
		// edit: missing new
		{"edit no new", "edit", "<path>a.txt</path><oldText>a</oldText>", "", false},
		// edit: using old/new aliases
		{"edit aliases", "edit", "<file>a.txt</file><old>a</old><new>b</new>", "edit", true},
		// bash: body-only
		{"bash body only", "bash", "make test", "bash", true},
		// bash: empty
		{"bash empty", "bash", "", "", false},
		// grep: using query alias
		{"grep query alias", "grep", "<query>func.*Test</query>", "grep", true},
		// grep: with path
		{"grep with path", "grep", "<pattern>TODO</pattern><path>/src</path>", "grep", true},
		// grep: missing pattern
		{"grep no pattern", "grep", "<path>/src</path>", "", false},
		// unknown tag
		{"unknown tag", "unknown", "<arg>val</arg>", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool, _, ok := parseToolTag(tt.tagName, tt.body)
			if ok != tt.wantOK {
				t.Errorf("parseToolTag(%q) ok = %v, want %v", tt.tagName, ok, tt.wantOK)
			}
			if tool != tt.wantTool {
				t.Errorf("parseToolTag(%q) tool = %q, want %q", tt.tagName, tool, tt.wantTool)
			}
		})
	}
}

// --- truncateLine tests ---

func TestTruncateLine(t *testing.T) {
	tests := []struct {
		name  string
		value string
		limit int
		want  string
	}{
		{"no truncation", "hello", 10, "hello"},
		{"exact", "hello", 5, "hello"},
		{"limit 0", "hello", 0, "hello"},
		{"limit negative", "hello", -1, "hello"},
		{"limit 3", "hello", 3, "hel"},
		{"limit 2", "hello", 2, "he"},
		{"limit 4 with ellipsis", "hello", 4, "h..."},
		{"limit 6 with ellipsis", "hello world", 6, "hel..."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := truncateLine(tt.value, tt.limit); got != tt.want {
				t.Errorf("truncateLine(%q, %d) = %q, want %q", tt.value, tt.limit, got, tt.want)
			}
		})
	}
}
