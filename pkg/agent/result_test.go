package agent

import (
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"testing"
)

func TestGetFinalAssistantText(t *testing.T) {
	tests := []struct {
		name     string
		messages []agentctx.AgentMessage
		want     string
	}{
		{
			name:     "empty messages",
			messages: []agentctx.AgentMessage{},
			want:     "",
		},
		{
			name: "no assistant messages",
			messages: []agentctx.AgentMessage{
				{Role: "user", Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "hello"}}},
			},
			want: "",
		},
		{
			name: "single assistant message",
			messages: []agentctx.AgentMessage{
				{Role: "user", Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "hello"}}},
				{Role: "assistant", Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "hi there"}}},
			},
			want: "hi there",
		},
		{
			name: "multiple assistant messages - returns last",
			messages: []agentctx.AgentMessage{
				{Role: "user", Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "hello"}}},
				{Role: "assistant", Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "first response"}}},
				{Role: "user", Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "how are you?"}}},
				{Role: "assistant", Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "second response"}}},
			},
			want: "second response",
		},
		{
			name: "assistant with multiple content blocks",
			messages: []agentctx.AgentMessage{
				{
					Role: "assistant",
					Content: []agentctx.ContentBlock{
						agentctx.TextContent{Type: "text", Text: "Hello "},
						agentctx.TextContent{Type: "text", Text: "World"},
					},
				},
			},
			want: "Hello World",
		},
		{
			name: "assistant with mixed content blocks",
			messages: []agentctx.AgentMessage{
				{
					Role: "assistant",
					Content: []agentctx.ContentBlock{
						agentctx.TextContent{Type: "text", Text: "Here is the answer: "},
						agentctx.ToolCallContent{ID: "1", Type: "tool_call", Name: "read", Arguments: map[string]any{}},
						agentctx.TextContent{Type: "text", Text: "The result is 42"},
					},
				},
			},
			want: "Here is the answer: The result is 42",
		},
		{
			name: "assistant with empty text",
			messages: []agentctx.AgentMessage{
				{
					Role:    "assistant",
					Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: ""}},
				},
			},
			want: "",
		},
		{
			name: "assistant with no text content",
			messages: []agentctx.AgentMessage{
				{
					Role: "assistant",
					Content: []agentctx.ContentBlock{
						agentctx.ToolCallContent{ID: "1", Type: "tool_call", Name: "read", Arguments: map[string]any{}},
					},
				},
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetFinalAssistantText(tt.messages)
			if got != tt.want {
				t.Errorf("GetFinalAssistantText() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGetAssistantTexts(t *testing.T) {
	tests := []struct {
		name     string
		messages []agentctx.AgentMessage
		want     string
	}{
		{
			name:     "empty messages",
			messages: []agentctx.AgentMessage{},
			want:     "",
		},
		{
			name: "single assistant message",
			messages: []agentctx.AgentMessage{
				{Role: "assistant", Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "response"}}},
			},
			want: "response",
		},
		{
			name: "multiple assistant messages - all returned",
			messages: []agentctx.AgentMessage{
				{Role: "user", Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "q1"}}},
				{Role: "assistant", Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "a1"}}},
				{Role: "user", Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "q2"}}},
				{Role: "assistant", Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "a2"}}},
			},
			want: "a1\na2",
		},
		{
			name: "filters out empty text",
			messages: []agentctx.AgentMessage{
				{Role: "assistant", Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "first"}}},
				{Role: "assistant", Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: ""}}},
				{Role: "assistant", Content: []agentctx.ContentBlock{agentctx.TextContent{Type: "text", Text: "third"}}},
			},
			want: "first\nthird",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetAssistantTexts(tt.messages)
			if got != tt.want {
				t.Errorf("GetAssistantTexts() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestGetTotalUsage(t *testing.T) {
	tests := []struct {
		name     string
		messages []agentctx.AgentMessage
		want     UsageStats
	}{
		{
			name:     "empty messages",
			messages: []agentctx.AgentMessage{},
			want:     UsageStats{},
		},
		{
			name: "no assistant messages with usage",
			messages: []agentctx.AgentMessage{
				{Role: "user", Content: []agentctx.ContentBlock{}},
			},
			want: UsageStats{},
		},
		{
			name: "single assistant with usage",
			messages: []agentctx.AgentMessage{
				{
					Role:    "assistant",
					Content: []agentctx.ContentBlock{},
					Usage: &agentctx.Usage{
						InputTokens:  100,
						OutputTokens: 50,
						TotalTokens:  150,
					},
				},
			},
			want: UsageStats{InputTokens: 100, OutputTokens: 50, TotalTokens: 150},
		},
		{
			name: "multiple assistant messages - aggregates usage",
			messages: []agentctx.AgentMessage{
				{
					Role:    "assistant",
					Content: []agentctx.ContentBlock{},
					Usage: &agentctx.Usage{
						InputTokens:  100,
						OutputTokens: 50,
						TotalTokens:  150,
					},
				},
				{Role: "user", Content: []agentctx.ContentBlock{}},
				{
					Role:    "assistant",
					Content: []agentctx.ContentBlock{},
					Usage: &agentctx.Usage{
						InputTokens:  200,
						OutputTokens: 75,
						TotalTokens:  275,
					},
				},
			},
			want: UsageStats{InputTokens: 300, OutputTokens: 125, TotalTokens: 425},
		},
		{
			name: "assistant with nil usage",
			messages: []agentctx.AgentMessage{
				{Role: "assistant", Content: []agentctx.ContentBlock{}, Usage: nil},
				{
					Role:    "assistant",
					Content: []agentctx.ContentBlock{},
					Usage: &agentctx.Usage{
						InputTokens:  100,
						OutputTokens: 50,
						TotalTokens:  150,
					},
				},
			},
			want: UsageStats{InputTokens: 100, OutputTokens: 50, TotalTokens: 150},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetTotalUsage(tt.messages)
			if got != tt.want {
				t.Errorf("GetTotalUsage() = %+v, want %+v", got, tt.want)
			}
		})
	}
}