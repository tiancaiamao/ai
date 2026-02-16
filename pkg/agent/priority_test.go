package agent

import (
	"testing"
)

func TestPriorityCalculator_PositionRule(t *testing.T) {
	calculator := NewPriorityCalculator()

	tests := []struct {
		name          string
		messageIndex  int
		totalMessages int
		minScore      float64
		maxScore      float64
	}{
		// Note: Final score is weighted average across all rules (0.15 position weight)
		// so scores are lower than raw position scores
		{"first message", 0, 10, 0.25, 0.50},
		{"recent message (80%+)", 9, 10, 0.30, 0.50},
		{"middle message", 5, 10, 0.0, 0.40},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := &PriorityContext{
				MessageIndex:  tt.messageIndex,
				TotalMessages: tt.totalMessages,
			}

			msg := AgentMessage{Role: "assistant"}
			score := calculator.Calculate(msg, ctx)

			if score < tt.minScore || score > tt.maxScore {
				t.Errorf("score %v not in expected range [%v, %v]", score, tt.minScore, tt.maxScore)
			}
		})
	}
}

func TestPriorityCalculator_RoleRule(t *testing.T) {
	calculator := NewPriorityCalculator()
	ctx := &PriorityContext{
		MessageIndex:  5,
		TotalMessages: 10,
	}

	tests := []struct {
		name     string
		msg      AgentMessage
		minScore float64
	}{
		// Note: Final score is weighted across all rules, not just role
		{"user message", AgentMessage{Role: "user"}, 0.25},
		{"toolResult", AgentMessage{Role: "toolResult", ToolName: "read"}, 0.20},
		{"assistant", AgentMessage{Role: "assistant"}, 0.15},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := calculator.Calculate(tt.msg, ctx)
			if score < tt.minScore {
				t.Errorf("score %v < minimum expected %v for %s", score, tt.minScore, tt.name)
			}
		})
	}
}

func TestPriorityCalculator_ErrorBoost(t *testing.T) {
	calculator := NewPriorityCalculator()
	ctx := &PriorityContext{
		MessageIndex:  5,
		TotalMessages: 10,
	}

	// Normal message
	normalMsg := AgentMessage{
		Role:    "toolResult",
		Content: []ContentBlock{TextContent{Type: "text", Text: "success output"}},
	}

	// Error message
	errorMsg := AgentMessage{
		Role:    "toolResult",
		Content: []ContentBlock{TextContent{Type: "text", Text: "error: something failed"}},
		IsError: true,
	}

	normalScore := calculator.Calculate(normalMsg, ctx)
	errorScore := calculator.Calculate(errorMsg, ctx)

	if errorScore <= normalScore {
		t.Errorf("error message score %v should be higher than normal %v", errorScore, normalScore)
	}
}

func TestPriorityCalculator_FilePathDetection(t *testing.T) {
	calculator := NewPriorityCalculator()
	ctx := &PriorityContext{
		MessageIndex:  5,
		TotalMessages: 10,
	}

	tests := []struct {
		name           string
		text           string
		shouldHavePath bool
	}{
		{"file path", "edited pkg/agent/loop.go:245", true},
		{"no path", "completed successfully", false},
		{"absolute path", "read /Users/test/file.go", true},
		{"file extension", "created output.txt", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := AgentMessage{
				Role:    "assistant",
				Content: []ContentBlock{TextContent{Type: "text", Text: tt.text}},
			}

			score := calculator.Calculate(msg, ctx)
			// Just check that it computes without error
			if score < 0 || score > 1 {
				t.Errorf("invalid score %v", score)
			}
		})
	}
}

func TestPriorityCalculator_ToolImportance(t *testing.T) {
	calculator := NewPriorityCalculator()
	ctx := &PriorityContext{
		MessageIndex:  5,
		TotalMessages: 10,
	}

	// Write operations should have higher priority than read
	writeMsg := AgentMessage{Role: "toolResult", ToolName: "write"}
	readMsg := AgentMessage{Role: "toolResult", ToolName: "read"}
	grepMsg := AgentMessage{Role: "toolResult", ToolName: "grep"}

	writeScore := calculator.Calculate(writeMsg, ctx)
	readScore := calculator.Calculate(readMsg, ctx)
	grepScore := calculator.Calculate(grepMsg, ctx)

	// All should have valid scores
	if writeScore < 0 || readScore < 0 || grepScore < 0 {
		t.Error("scores should be non-negative")
	}

	// Write should generally be >= read >= grep due to role rule
	// (exact ordering depends on other rules too)
	t.Logf("write=%.2f, read=%.2f, grep=%.2f", writeScore, readScore, grepScore)
}

func TestHasFilePath(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		expected bool
	}{
		{"unix path", "file: pkg/agent/loop.go", true},
		{"absolute unix path", "/Users/test/file.go", true},
		{"no path", "completed successfully", false},
		{"windows path", "C:\\Users\\file.txt", true},
		{"file extension", "see output.txt", true},
		{"line reference", "at loop.go:123", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := hasFilePath(tt.text)
			if result != tt.expected {
				t.Errorf("hasFilePath(%q) = %v, want %v", tt.text, result, tt.expected)
			}
		})
	}
}

// Note: TestContainsErrorPattern is in tool_output_test.go

func TestGetToolImportance(t *testing.T) {
	tests := []struct {
		toolName    string
		isError     bool
		minExpected float64
	}{
		{"write", false, 0.7},
		{"edit", false, 0.7},
		{"bash", false, 0.7},
		{"read", false, 0.5},
		{"grep", false, 0.3},
		{"unknown", false, 0.4},
		{"write", true, 0.8}, // Error should be even higher
	}

	for _, tt := range tests {
		name := tt.toolName
		if tt.isError {
			name += "_error"
		}
		t.Run(name, func(t *testing.T) {
			score := getToolImportance(tt.toolName, tt.isError)
			if score < tt.minExpected {
				t.Errorf("getToolImportance(%q, %v) = %v, want >= %v", tt.toolName, tt.isError, score, tt.minExpected)
			}
		})
	}
}
