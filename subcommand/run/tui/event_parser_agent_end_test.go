package tui

import "testing"

func TestIsAgentEnd(t *testing.T) {
	tests := []struct {
		name string
		line string
		want bool
	}{
		{name: "agent end", line: `{"type":"agent_end"}`, want: true},
		{name: "text containing marker", line: `{"type":"text_delta","delta":"agent_end"}`},
		{name: "invalid json", line: `agent_end`},
		{name: "other event", line: `{"type":"agent_start"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsAgentEnd(tt.line); got != tt.want {
				t.Fatalf("IsAgentEnd(%q) = %t, want %t", tt.line, got, tt.want)
			}
		})
	}
}
