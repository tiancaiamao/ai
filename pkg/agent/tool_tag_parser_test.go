package agent

import (
	"testing"
)

func TestInjectToolCallsFromTaggedText_Basic(t *testing.T) {
	tests := []struct {
		name     string
		input    AgentMessage
		wantCall bool
		callName string
	}{
		{
			name: "simple bash command",
			input: AgentMessage{
				Role:    "assistant",
				Content: []ContentBlock{TextContent{Type: "text", Text: "<bash>git diff HEAD</bash>"}},
			},
			wantCall: true,
			callName: "bash",
		},
		{
			name: "nested bash command",
			input: AgentMessage{
				Role:    "assistant",
				Content: []ContentBlock{TextContent{Type: "text", Text: "<bash>\n<command>git diff HEAD</command>\n</bash>"}},
			},
			wantCall: true,
			callName: "bash",
		},
		{
			name: "read with path",
			input: AgentMessage{
				Role:    "assistant",
				Content: []ContentBlock{TextContent{Type: "text", Text: "<read>\n<path>file.txt</path>\n</read>"}},
			},
			wantCall: true,
			callName: "read",
		},
		{
			name: "write with content",
			input: AgentMessage{
				Role:    "assistant",
				Content: []ContentBlock{TextContent{Type: "text", Text: "<write>\n<path>file.txt</path>\n<content>hello</content>\n</write>"}},
			},
			wantCall: true,
			callName: "write",
		},
		{
			name: "text without tags",
			input: AgentMessage{
				Role:    "assistant",
				Content: []ContentBlock{TextContent{Type: "text", Text: "Hello, world!"}},
			},
			wantCall: false,
		},
		{
			name: "incomplete tag - should not parse",
			input: AgentMessage{
				Role:    "assistant",
				Content: []ContentBlock{TextContent{Type: "text", Text: "<bash>git diff HEAD"}},
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
	// Test that we don't skip tag parsing when existing tool calls are empty/invalid
	msg := AgentMessage{
		Role: "assistant",
		Content: []ContentBlock{
			TextContent{Type: "text", Text: "Let me run: <bash>ls -la</bash>"},
			ToolCallContent{ID: "empty", Name: "", Arguments: map[string]any{}}, // Empty tool call
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

func TestDetectIncompleteToolCalls(t *testing.T) {
	tests := []struct {
		name        string
		text        string
		wantIssues  int
		shouldHave  string
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
			name:       "orphaned closing tag",
			text:       "ls -la</bash>",
			wantIssues: 1,
			shouldHave: "closing </bash>",
		},
		{
			name:       "uppercase tag",
			text:       "<Bash>ls -la</Bash>",
			wantIssues: 1,
			shouldHave: "uppercase",
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

func TestValidateToolCallArgs(t *testing.T) {
	tests := []struct {
		name      string
		toolName  string
		args      map[string]any
		wantError bool
	}{
		{
			name:     "valid read",
			toolName: "read",
			args:     map[string]any{"path": "file.txt"},
			wantError: false,
		},
		{
			name:     "read missing path",
			toolName: "read",
			args:     map[string]any{},
			wantError: true,
		},
		{
			name:     "valid bash",
			toolName: "bash",
			args:     map[string]any{"command": "ls -la"},
			wantError: false,
		},
		{
			name:     "bash missing command",
			toolName: "bash",
			args:     map[string]any{},
			wantError: true,
		},
		{
			name:     "valid write",
			toolName: "write",
			args:     map[string]any{"path": "file.txt", "content": "hello"},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateToolCallArgs(tt.toolName, tt.args)
			if (err != nil) != tt.wantError {
				t.Errorf("ValidateToolCallArgs() error = %v, wantError %v", err, tt.wantError)
			}
		})
	}
}

