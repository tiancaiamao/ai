package tools

import (
	"context"
	"strings"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

func TestDetectSleepCommand(t *testing.T) {
	tests := []struct {
		name        string
		command     string
		expectedSec int
		expectedHas bool
	}{
		{
			name:        "simple sleep",
			command:     "sleep 90",
			expectedSec: 90,
			expectedHas: true,
		},
		{
			name:        "sleep with seconds unit",
			command:     "sleep 30s",
			expectedSec: 30,
			expectedHas: true,
		},
		{
			name:        "sleep with minutes unit",
			command:     "sleep 2m",
			expectedSec: 120,
			expectedHas: true,
		},
		{
			name:        "sleep with hours unit",
			command:     "sleep 1h",
			expectedSec: 3600,
			expectedHas: true,
		},
		{
			name:        "sleep after command",
			command:     "echo done && sleep 60",
			expectedSec: 60,
			expectedHas: true,
		},
		{
			name:        "full path sleep",
			command:     "/bin/sleep 120",
			expectedSec: 120,
			expectedHas: true,
		},
		{
			name:        "sleep 29 seconds - below threshold",
			command:     "sleep 29",
			expectedSec: 29,
			expectedHas: true,
		},
		{
			name:        "sleep 30 seconds - at threshold",
			command:     "sleep 30",
			expectedSec: 30,
			expectedHas: true,
		},
		{
			name:        "echo command - no sleep",
			command:     "echo hello",
			expectedSec: 0,
			expectedHas: false,
		},
		{
			name:        "grep command - no sleep",
			command:     "grep -r pattern .",
			expectedSec: 0,
			expectedHas: false,
		},
		{
			name:        "word containing sleep - no match",
			command:     "echo sleeping is good",
			expectedSec: 0,
			expectedHas: false,
		},
		{
			name:        "comment about sleep - no match",
			command:     "# sleep for a bit",
			expectedSec: 0,
			expectedHas: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			duration, hasSleep := detectSleepCommand(tt.command)
			if hasSleep != tt.expectedHas {
				t.Errorf("detectSleepCommand() hasSleep = %v, want %v", hasSleep, tt.expectedHas)
			}
			if duration != tt.expectedSec {
				t.Errorf("detectSleepCommand() duration = %v, want %v", duration, tt.expectedSec)
			}
		})
	}
}

func TestBashTool_SleepDetection(t *testing.T) {
	tool := NewBashTool(&Workspace{})

	tests := []struct {
		name          string
		command       string
		expectBlock   bool
		errorContains string
	}{
		{
			name:          "sleep 90 should be blocked",
			command:       "sleep 90",
			expectBlock:   true,
			errorContains: "sleep with duration >= 30 seconds is not allowed",
		},
		{
			name:          "sleep 60s should be blocked",
			command:       "sleep 60s",
			expectBlock:   true,
			errorContains: "sleep with duration >= 30 seconds is not allowed",
		},
		{
			name:          "sleep 1m should be blocked",
			command:       "sleep 1m",
			expectBlock:   true,
			errorContains: "sleep with duration >= 30 seconds is not allowed",
		},
		{
			name:          "command with sleep 45 should be blocked",
			command:       "echo start && sleep 45 && echo done",
			expectBlock:   true,
			errorContains: "sleep with duration >= 30 seconds is not allowed",
		},
		{
			name:          "sleep 30 at threshold should be blocked",
			command:       "sleep 30",
			expectBlock:   true,
			errorContains: "sleep with duration >= 30 seconds is not allowed",
		},
		{
			name:        "echo command should work",
			command:     "echo hello",
			expectBlock: false,
		},
		{
			name:        "grep command should work",
			command:     "grep -r pattern .",
			expectBlock: false,
		},
		{
			name:        "word containing sleep should work",
			command:     "echo sleeping is good",
			expectBlock: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			args := map[string]any{"command": tt.command}

			result, err := tool.Execute(ctx, args)

			// The tool doesn't return an error, but returns error in result
			if err != nil {
				t.Errorf("Execute() returned unexpected error: %v", err)
			}

			if tt.expectBlock {
				if len(result) == 0 {
					t.Errorf("Execute() should return result for blocked sleep")
					return
				}
				text, ok := result[0].(agentctx.TextContent)
				if !ok {
					t.Errorf("Execute() result[0] should be TextContent")
					return
				}
				if text.Type != "text" {
					t.Errorf("Execute() result[0].Type should be 'text'")
				}
				if !strings.Contains(text.Text, tt.errorContains) {
					t.Errorf("Execute() result should contain error message:\ngot: %s\nwant to contain: %s", text.Text, tt.errorContains)
				}
			} else {
				// For allowed commands, result should not contain error about long sleep
				if len(result) > 0 {
					text, ok := result[0].(agentctx.TextContent)
					if ok && strings.Contains(text.Text, "sleep with duration >= 30 seconds") {
						t.Errorf("Execute() should not block this command: %s\nresult: %s", tt.command, text.Text)
					}
				}
			}
		})
	}
}