package session

import (
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

func TestNormalizeSessionPath(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantEmpty bool
	}{
		{"empty", "", true},
		{"relative", "foo/bar", false},
		{"absolute", "/tmp/test", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeSessionPath(tt.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantEmpty && got != "" {
				t.Errorf("NormalizeSessionPath(%q) = %q, want empty", tt.input, got)
			}
			if !tt.wantEmpty && got == "" {
				t.Errorf("NormalizeSessionPath(%q) = empty, want non-empty", tt.input)
			}
		})
	}
}

func TestTreeEntryLabel(t *testing.T) {
	tests := []struct {
		name     string
		entry    SessionEntry
		wantRole string
		wantText string
	}{
		{
			name:     "nil message",
			entry:    SessionEntry{Type: EntryTypeMessage},
			wantRole: "message",
			wantText: "",
		},
		{
			name: "user message with text",
			entry: SessionEntry{Type: EntryTypeMessage, Message: &agentctx.AgentMessage{
				Role:    "user",
				Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "hello"}},
			}},
			wantRole: "user",
			wantText: "hello",
		},
		{
			name: "assistant tool call",
			entry: SessionEntry{Type: EntryTypeMessage, Message: &agentctx.AgentMessage{
				Role: "assistant",
				Content: []agentctx.ContentBlock{
					agentctx.ToolCallContent{Type: "tool_call", ID: "tu1", Name: "bash", Arguments: map[string]any{"command": "ls"}},
				},
			}},
			wantRole: "assistant",
			wantText: "tool call",
		},
		{
			name: "toolResult with name",
			entry: SessionEntry{Type: EntryTypeMessage, Message: &agentctx.AgentMessage{
				Role:     "toolResult",
				ToolName: "bash",
			}},
			wantRole: "toolResult",
			wantText: "bash result",
		},
		{
			name: "toolResult without name",
			entry: SessionEntry{Type: EntryTypeMessage, Message: &agentctx.AgentMessage{
				Role: "toolResult",
			}},
			wantRole: "toolResult",
			wantText: "tool result",
		},
		{
			name:     "compaction",
			entry:    SessionEntry{Type: EntryTypeCompaction, Summary: "summary text"},
			wantRole: "compaction",
			wantText: "summary text",
		},
		{
			name:     "branch summary",
			entry:    SessionEntry{Type: EntryTypeBranchSummary, Summary: "branch text"},
			wantRole: "branch summary",
			wantText: "branch text",
		},
		{
			name:     "session info with name",
			entry:    SessionEntry{Type: EntryTypeSessionInfo, Name: "my session"},
			wantRole: "session info",
			wantText: "my session",
		},
		{
			name:     "session info fallback to title",
			entry:    SessionEntry{Type: EntryTypeSessionInfo, Title: "the title"},
			wantRole: "session info",
			wantText: "the title",
		},
		{
			name:     "unknown type",
			entry:    SessionEntry{Type: "custom"},
			wantRole: "custom",
			wantText: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			role, text := TreeEntryLabel(tt.entry)
			if role != tt.wantRole {
				t.Errorf("role = %q, want %q", role, tt.wantRole)
			}
			if text != tt.wantText {
				t.Errorf("text = %q, want %q", text, tt.wantText)
			}
		})
	}
}

func TestBuildTreeEntries(t *testing.T) {
	id1, id2, id3 := "e1", "e2", "e3"

	t.Run("empty", func(t *testing.T) {
		if got := BuildTreeEntries(nil, nil); got != nil {
			t.Errorf("BuildTreeEntries(nil) = %v, want nil", got)
		}
	})

	t.Run("linear chain", func(t *testing.T) {
		entries := []SessionEntry{
			{ID: id1, Type: EntryTypeMessage, ParentID: nil},
			{ID: id2, Type: EntryTypeMessage, ParentID: &id1},
			{ID: id3, Type: EntryTypeMessage, ParentID: &id2},
		}
		leafID := id3
		result := BuildTreeEntries(entries, &leafID)
		if len(result) != 3 {
			t.Fatalf("got %d entries, want 3", len(result))
		}
		if result[0].Depth != 0 || result[1].Depth != 1 || result[2].Depth != 2 {
			t.Errorf("depths wrong: %d %d %d", result[0].Depth, result[1].Depth, result[2].Depth)
		}
		if !result[2].Leaf {
			t.Errorf("last entry should be leaf")
		}
	})

	t.Run("orphan becomes root", func(t *testing.T) {
		missing := "missing-parent"
		entries := []SessionEntry{
			{ID: id1, Type: EntryTypeMessage, ParentID: nil},
			{ID: id2, Type: EntryTypeMessage, ParentID: &missing},
		}
		result := BuildTreeEntries(entries, nil)
		if len(result) != 2 {
			t.Fatalf("got %d entries, want 2", len(result))
		}
		if result[0].Depth != 0 && result[1].Depth != 0 {
			t.Errorf("both should be root depth, got %d and %d", result[0].Depth, result[1].Depth)
		}
	})
}

func TestCollectSessionUsage(t *testing.T) {
	msgs := []agentctx.AgentMessage{
		{Role: "user"},
		{Role: "user"},
		{
			Role: "assistant",
			Usage: &agentctx.Usage{
				InputTokens:  1000,
				OutputTokens: 200,
				Cost:         agentctx.Cost{Total: 0.05},
			},
			Content: []agentctx.ContentBlock{
				agentctx.ToolCallContent{Type: "tool_call", ID: "tu1", Name: "bash"},
			},
		},
		{Role: "toolResult"},
		{
			Role: "assistant",
			Usage: &agentctx.Usage{
				InputTokens:  3000,
				OutputTokens: 500,
				Cost:         agentctx.Cost{Total: 0.10},
			},
		},
	}

	u := CollectSessionUsage(msgs)

	if u.UserCount != 2 {
		t.Errorf("UserCount = %d, want 2", u.UserCount)
	}
	if u.AssistantCount != 2 {
		t.Errorf("AssistantCount = %d, want 2", u.AssistantCount)
	}
	if u.ToolCalls != 1 {
		t.Errorf("ToolCalls = %d, want 1", u.ToolCalls)
	}
	if u.ToolResults != 1 {
		t.Errorf("ToolResults = %d, want 1", u.ToolResults)
	}
	if u.Tokens.Output != 700 {
		t.Errorf("Output = %d, want 700", u.Tokens.Output)
	}
	if u.Tokens.Total != 700+3000 {
		t.Errorf("Total = %d, want %d", u.Tokens.Total, 700+3000)
	}
	if u.Cost < 0.149 || u.Cost > 0.151 {
		t.Errorf("Cost = %f, want ~0.15", u.Cost)
	}
}

func TestCollectSessionUsageEmpty(t *testing.T) {
	u := CollectSessionUsage(nil)
	if u.UserCount != 0 || u.Tokens.Total != 0 {
		t.Errorf("expected zero values, got %+v", u)
	}
}

func TestResolveSessionName(t *testing.T) {
	t.Run("nil manager", func(t *testing.T) {
		if got := ResolveSessionName(nil, "s1"); got != "s1" {
			t.Errorf("ResolveSessionName(nil, s1) = %q, want s1", got)
		}
	})
	t.Run("empty id", func(t *testing.T) {
		if got := ResolveSessionName(nil, ""); got != "" {
			t.Errorf("ResolveSessionName(nil, '') = %q, want ''", got)
		}
	})
}
