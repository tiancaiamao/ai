package rpc

import (
	"strings"
	"testing"
)

func TestAdvanceSubagentDepth(t *testing.T) {
	tests := []struct {
		name      string
		value     string
		set       bool
		want      string
		wantError string
	}{
		{name: "top-level", want: "0"},
		{name: "first child", value: "0", set: true, want: "1"},
		{name: "second child is rejected", value: "1", set: true, wantError: "nested agent limit"},
		{name: "already over limit", value: "2", set: true, wantError: "nested agent limit"},
		{name: "invalid value", value: "bad", set: true, wantError: "invalid AI_SUBAGENT_DEPTH"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got string
			err := advanceSubagentDepth(
				func(string) (string, bool) { return tt.value, tt.set },
				func(_, value string) error { got = value; return nil },
			)
			if tt.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantError) {
					t.Fatalf("error = %v, want substring %q", err, tt.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("advanceSubagentDepth: %v", err)
			}
			if got != tt.want {
				t.Fatalf("set value = %q, want %q", got, tt.want)
			}
		})
	}
}
