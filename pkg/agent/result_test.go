package agent

import (
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"testing"
)

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