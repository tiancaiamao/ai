package agent

import (
	"strings"
	"testing"
)

func TestProcessOutput_Full(t *testing.T) {
	processor := NewToolOutputProcessor(nil, 50000)

	// Test read tool - should use full strategy
	longOutput := strings.Repeat("line\n", 1000)
	result := processor.ProcessOutput("read", longOutput, false)

	// Should be truncated due to max chars limit (8000 tokens * 4 = 32000 chars)
	if len(result) > 33000 {
		t.Errorf("Full strategy should still cap output, got %d chars", len(result))
	}
}

func TestProcessOutput_Digest(t *testing.T) {
	// Create processor with small limit to trigger compression
	policies := map[string]OutputPolicy{
		"bash": {
			ToolName:  "bash",
			MaxTokens: 100, // 400 chars limit
			Strategy:  StrategyDigest,
			KeepTail:  5,
		},
	}
	processor := NewToolOutputProcessor(policies, 1000)

	// Create output that exceeds the limit
	var lines []string
	for i := 0; i < 50; i++ {
		lines = append(lines, "Building module "+string(rune('A'+i%26)))
	}
	lines = append(lines, "error: undefined variable in main.go:42")
	for i := 0; i < 50; i++ {
		lines = append(lines, "more output line "+string(rune('0'+i%10)))
	}

	output := strings.Join(lines, "\n")

	result := processor.ProcessOutput("bash", output, true)

	// Should extract error line
	if !strings.Contains(result, "error:") {
		t.Error("Digest should extract error lines")
	}

	// Should be much shorter (digest extracts key info)
	if len(result) >= len(output) {
		t.Errorf("Digest should compress output, got %d vs %d", len(result), len(output))
	}
}

func TestProcessOutput_Extract(t *testing.T) {
	// Create processor with small limit to trigger compression
	policies := map[string]OutputPolicy{
		"grep": {
			ToolName:  "grep",
			MaxTokens: 100, // 400 chars limit
			Strategy:  StrategyExtract,
		},
	}
	processor := NewToolOutputProcessor(policies, 1000)

	// Create large output to trigger extraction
	var lines []string
	for i := 0; i < 20; i++ {
		lines = append(lines, "main.go:"+string(rune('0'+i%10))+": func main"+string(rune('A'+i))+"() {}")
	}
	for i := 0; i < 20; i++ {
		lines = append(lines, "utils.go:"+string(rune('0'+i%10))+": func helper"+string(rune('A'+i))+"() {}")
	}

	output := strings.Join(lines, "\n")

	result := processor.ProcessOutput("grep", output, false)

	// Should preserve file information
	if !strings.Contains(result, "main.go") {
		t.Error("Extract should preserve file information")
	}

	// Should be shorter due to extraction
	if len(result) >= len(output) {
		t.Errorf("Extract should reduce output size, got %d vs %d", len(result), len(output))
	}
}

func TestProcessOutput_Truncate(t *testing.T) {
	// Create processor with small limit to trigger truncation
	policies := map[string]OutputPolicy{
		"test": {
			ToolName:  "test",
			MaxTokens: 100, // 400 chars limit
			Strategy:  StrategyTruncate,
			KeepHead:  5,
			KeepTail:  5,
		},
	}
	processor := NewToolOutputProcessor(policies, 1000)

	// Create large output to trigger truncation
	var lines []string
	for i := 0; i < 50; i++ {
		lines = append(lines, "line "+string(rune('0'+i%10))+" with some additional content")
	}
	output := strings.Join(lines, "\n")

	result := processor.ProcessOutput("test", output, false)

	// Should contain truncation marker
	if !strings.Contains(result, "...") && !strings.Contains(result, "truncated") {
		t.Error("Truncate should add truncation marker")
	}

	// Should be shorter
	if len(result) >= len(output) {
		t.Errorf("Truncate should reduce output size, got %d vs %d", len(result), len(output))
	}
}

func TestProcessOutput_SmallOutput(t *testing.T) {
	processor := NewToolOutputProcessor(nil, 50000)

	smallOutput := "just a small output"
	result := processor.ProcessOutput("bash", smallOutput, false)

	// Should return unchanged
	if result != smallOutput {
		t.Errorf("Small output should be unchanged, got: %s", result)
	}
}

func TestProcessOutput_Empty(t *testing.T) {
	processor := NewToolOutputProcessor(nil, 50000)

	result := processor.ProcessOutput("bash", "", false)
	if result != "" {
		t.Errorf("Empty output should remain empty, got: %s", result)
	}
}

func TestDefaultToolPolicies(t *testing.T) {
	policies := DefaultToolPolicies()

	// Verify all expected tools have policies
	expectedTools := []string{"read", "write", "edit", "bash", "grep"}
	for _, tool := range expectedTools {
		if _, ok := policies[tool]; !ok {
			t.Errorf("Missing policy for tool: %s", tool)
		}
	}

	// Verify read uses full strategy
	if policies["read"].Strategy != StrategyFull {
		t.Error("read tool should use StrategyFull")
	}

	// Verify bash uses digest strategy
	if policies["bash"].Strategy != StrategyDigest {
		t.Error("bash tool should use StrategyDigest")
	}

	// Verify grep uses extract strategy
	if policies["grep"].Strategy != StrategyExtract {
		t.Error("grep tool should use StrategyExtract")
	}
}

func TestContainsErrorPattern(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"error: something went wrong", true},
		{"ERROR: critical failure", true},
		{"Failed to connect", true},
		{"Exception in thread main", true},
		{"exit code 1", true},
		{"This is fine", false},
		{"Normal output", false},
	}

	for _, tt := range tests {
		result := containsErrorPattern(strings.ToLower(tt.input))
		if result != tt.expected {
			t.Errorf("containsErrorPattern(%q) = %v, want %v", tt.input, result, tt.expected)
		}
	}
}

func TestExtractFilePath(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"main.go:10: code here", "main.go"},
		{"pkg/agent/loop.go:245: func run()", "pkg/agent/loop.go"},
		{"src/utils.py:5: def help()", "src/utils.py"},
		{"no file here", ""},
		{"just text: no path", ""},
	}

	for _, tt := range tests {
		result := extractFilePath(tt.input)
		if result != tt.expected {
			t.Errorf("extractFilePath(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}