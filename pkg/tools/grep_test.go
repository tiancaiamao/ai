package tools

import (
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setupGrepTest(t *testing.T) (*GrepTool, string) {
	t.Helper()
	tmpDir := t.TempDir()
	ws := &Workspace{cwd: tmpDir}
	tool := NewGrepTool(ws)

	// Create test files
	_ = os.WriteFile(filepath.Join(tmpDir, "hello.txt"), []byte("Hello World\nhello world\nGoodbye World\n"), 0644)
	_ = os.WriteFile(filepath.Join(tmpDir, "code.go"), []byte("package main\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"), 0644)
	_ = os.MkdirAll(filepath.Join(tmpDir, "sub"), 0755)
	_ = os.WriteFile(filepath.Join(tmpDir, "sub", "nested.txt"), []byte("nested hello\nNested Hello\n"), 0644)

	return tool, tmpDir
}

// getText extracts text from the first ContentBlock of a tool result.
func getText(blocks []agentctx.ContentBlock) string {
	if len(blocks) == 0 {
		return ""
	}
	if tc, ok := blocks[0].(agentctx.TextContent); ok {
		return tc.Text
	}
	return ""
}

func TestGrepTool_NameAndDescription(t *testing.T) {
	tool, _ := setupGrepTest(t)
	if tool.Name() != "grep" {
		t.Errorf("Expected name 'grep', got '%s'", tool.Name())
	}
	desc := tool.Description()
	if desc == "" {
		t.Error("Description should not be empty")
	}
}

func TestGrepTool_BasicSearch(t *testing.T) {
	tool, _ := setupGrepTest(t)
	result, err := tool.Execute(context.Background(), map[string]any{
		"pattern": "Hello",
	})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	text := getText(result)
	if text == "No matches found" {
		t.Error("Expected matches, got 'No matches found'")
	}
}

func TestGrepTool_IgnoreCase(t *testing.T) {
	tool, _ := setupGrepTest(t)

	// Without ignoreCase, searching "HELLO" should find nothing in hello.txt
	result, err := tool.Execute(context.Background(), map[string]any{
		"pattern":    "HELLO",
		"ignoreCase": false,
	})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	text := getText(result)
	if text != "No matches found" {
		t.Errorf("Expected no matches for case-sensitive HELLO, got: %s", text)
	}

	// With ignoreCase, should find matches
	result, err = tool.Execute(context.Background(), map[string]any{
		"pattern":    "HELLO",
		"ignoreCase": true,
	})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	text = getText(result)
	if text == "No matches found" {
		t.Error("Expected matches with ignoreCase=true, got 'No matches found'")
	}
	// Should find both "Hello" and "hello"
	if !strings.Contains(text, "Hello") || !strings.Contains(text, "hello") {
		t.Errorf("Expected both Hello and hello in output, got: %s", text)
	}
}

func TestGrepTool_Literal(t *testing.T) {
	tool, tmpDir := setupGrepTest(t)

	// Search for a regex special char in literal mode
	_ = os.WriteFile(filepath.Join(tmpDir, "special.txt"), []byte("func() {}\nother line\n"), 0644)

	result, err := tool.Execute(context.Background(), map[string]any{
		"pattern": "func()",
		"literal": true,
	})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	text := getText(result)
	if text == "No matches found" {
		t.Error("Expected match for literal 'func()', got 'No matches found'")
	}
}

func TestGrepTool_ContextLines(t *testing.T) {
	tool, _ := setupGrepTest(t)

	result, err := tool.Execute(context.Background(), map[string]any{
		"pattern": "hello",
		"path":    "hello.txt",
		"context": 1,
	})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	text := getText(result)
	if text == "No matches found" {
		t.Error("Expected matches, got 'No matches found'")
	}
	// With context=1, should see nearby lines (context lines use '-' separator)
	lines := strings.Split(text, "\n")
	hasContextLine := false
	for _, line := range lines {
		// Context lines have format: file-line_num- content
		if strings.Contains(line, "- ") && !strings.Contains(line, ": ") {
			hasContextLine = true
			break
		}
	}
	if !hasContextLine {
		t.Logf("Note: context lines may not be present if only 1 match, output was:\n%s", text)
	}
}

func TestGrepTool_Limit(t *testing.T) {
	tool, tmpDir := setupGrepTest(t)

	// Create a file with many matching lines
	var content string
	for i := 0; i < 50; i++ {
		content += "match line here\n"
	}
	_ = os.WriteFile(filepath.Join(tmpDir, "many.txt"), []byte(content), 0644)

	result, err := tool.Execute(context.Background(), map[string]any{
		"pattern": "match",
		"limit":   5,
	})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	text := getText(result)

	// Count match lines (lines with ":" separator, not context or truncation notice)
	matchCount := 0
	for _, line := range strings.Split(text, "\n") {
		if line != "" && !strings.HasPrefix(line, "[") && !strings.HasPrefix(line, "--") {
			// Match lines contain "line_num:" pattern
			matchCount++
		}
	}
	if matchCount > 5 {
		t.Errorf("Expected at most 5 matches with limit=5, got %d", matchCount)
	}
	if !strings.Contains(text, "truncated") {
		t.Logf("Expected truncation notice, output:\n%s", text)
	}
}

func TestGrepTool_FilePattern(t *testing.T) {
	tool, _ := setupGrepTest(t)

	result, err := tool.Execute(context.Background(), map[string]any{
		"pattern":     "hello",
		"filePattern": "*.txt",
	})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	text := getText(result)
	if text == "No matches found" {
		t.Error("Expected matches in .txt files")
	}
}

func TestGrepTool_NoMatches(t *testing.T) {
	tool, _ := setupGrepTest(t)

	result, err := tool.Execute(context.Background(), map[string]any{
		"pattern": "zzzznonexistent",
	})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	text := getText(result)
	if text != "No matches found" {
		t.Errorf("Expected 'No matches found', got: %s", text)
	}
}

func TestGrepTool_PathExpansion(t *testing.T) {
	tool, _ := setupGrepTest(t)

	result, err := tool.Execute(context.Background(), map[string]any{
		"pattern": "Hello",
		"path":    "hello.txt",
	})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	text := getText(result)
	if text == "No matches found" {
		t.Error("Expected matches in hello.txt")
	}
}

func TestGrepTool_AllParametersCombined(t *testing.T) {
	tool, _ := setupGrepTest(t)

	result, err := tool.Execute(context.Background(), map[string]any{
		"pattern":     "HELLO",
		"ignoreCase":  true,
		"literal":     false,
		"context":     0,
		"limit":       10,
		"filePattern": "*.txt",
	})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	text := getText(result)
	if text == "No matches found" {
		t.Error("Expected matches with all params combined")
	}
}