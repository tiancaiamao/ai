package tools

import (
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadTool_BasicFile(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	content := "line1\nline2\nline3\n"
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	ws, _ := NewWorkspace(dir)
	tool := NewReadTool(ws)

	result, err := tool.Execute(context.Background(), map[string]any{
		"path": filePath,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(result))
	}

	text := result[0].(agentctx.TextContent).Text
	// 3 lines joined with \n (no trailing newline in output)
	expected := "line1\nline2\nline3"
	if text != expected {
		t.Errorf("expected %q, got %q", expected, text)
	}
}

func TestReadTool_OffsetAndLimit(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	// Create a 10-line file
	var content string
	for i := 1; i <= 10; i++ {
		content += fmt.Sprintf("line%d\n", i)
	}
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	ws, _ := NewWorkspace(dir)
	tool := NewReadTool(ws)

	// Read lines 3-5 (offset=3, limit=3)
	result, err := tool.Execute(context.Background(), map[string]any{
		"path":   filePath,
		"offset": float64(3),
		"limit":  float64(3),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := result[0].(agentctx.TextContent).Text

	// Should contain lines 3-5
	if !strings.Contains(text, "line3") || !strings.Contains(text, "line4") || !strings.Contains(text, "line5") {
		t.Errorf("expected lines 3-5 in output, got: %s", text)
	}
	// Should NOT contain lines 1-2 or 6-10
	if strings.Contains(text, "line1") || strings.Contains(text, "line2") {
		t.Errorf("should not contain lines 1-2, got: %s", text)
	}
	if strings.Contains(text, "line6") {
		t.Errorf("should not contain lines 6+, got: %s", text)
	}
	// 10 lines total, we read lines 3-5 (3 lines), remaining: 5 lines (6,7,8,9,10)
	if !strings.Contains(text, "5 more lines below") {
		t.Errorf("expected footer hint '5 more lines below', got: %s", text)
	}
	// Should have header hint
	if !strings.Contains(text, "2 lines above omitted") {
		t.Errorf("expected header hint '2 lines above omitted', got: %s", text)
	}
}

func TestReadTool_OffsetOnly(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	var content string
	for i := 1; i <= 100; i++ {
		content += fmt.Sprintf("line%d\n", i)
	}
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	ws, _ := NewWorkspace(dir)
	tool := NewReadTool(ws)

	// Read from line 90 (offset=90, default limit=2000)
	result, err := tool.Execute(context.Background(), map[string]any{
		"path":   filePath,
		"offset": float64(90),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := result[0].(agentctx.TextContent).Text

	// Should contain lines 90-100
	if !strings.Contains(text, "line90") || !strings.Contains(text, "line100") {
		t.Errorf("expected lines 90-100, got: %s", text)
	}
	// Should have header hint
	if !strings.Contains(text, "89 lines above omitted") {
		t.Errorf("expected header hint about 89 lines omitted, got: %s", text)
	}
	// Should NOT have footer hint (we read to end)
	if strings.Contains(text, "more lines below") {
		t.Errorf("should not have footer hint, got: %s", text)
	}
}

func TestReadTool_OffsetExceedsFileLength(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	content := "line1\nline2\nline3\n"
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	ws, _ := NewWorkspace(dir)
	tool := NewReadTool(ws)

	_, err := tool.Execute(context.Background(), map[string]any{
		"path":   filePath,
		"offset": float64(100),
	})
	if err == nil {
		t.Fatal("expected error for offset exceeding file length")
	}
	if !strings.Contains(err.Error(), "offset 100 exceeds file length") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestReadTool_FileTooLarge(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "large.txt")

	// Create a file larger than 50KB
	largeContent := make([]byte, 60*1024)
	for i := range largeContent {
		largeContent[i] = 'a'
	}
	// Add some newlines so it's valid text
	largeContent[100] = '\n'
	largeContent[200] = '\n'

	if err := os.WriteFile(filePath, largeContent, 0644); err != nil {
		t.Fatal(err)
	}

	ws, _ := NewWorkspace(dir)
	tool := NewReadTool(ws)

	_, err := tool.Execute(context.Background(), map[string]any{
		"path": filePath,
	})
	if err == nil {
		t.Fatal("expected error for large file")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Errorf("unexpected error message: %v", err)
	}
	if !strings.Contains(err.Error(), "offset") || !strings.Contains(err.Error(), "limit") {
		t.Errorf("error should suggest using offset/limit, got: %v", err)
	}
}

func TestReadTool_LimitTruncation(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	var content string
	for i := 1; i <= 100; i++ {
		content += fmt.Sprintf("line%d\n", i)
	}
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	ws, _ := NewWorkspace(dir)
	tool := NewReadTool(ws)

	// Read first 5 lines with limit=5
	result, err := tool.Execute(context.Background(), map[string]any{
		"path":  filePath,
		"limit": float64(5),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := result[0].(agentctx.TextContent).Text

	// Should contain lines 1-5
	if !strings.Contains(text, "line1") || !strings.Contains(text, "line5") {
		t.Errorf("expected lines 1-5, got: %s", text)
	}
	// 100 lines total, read 5, remaining: 95 lines
	if !strings.Contains(text, "95 more lines below") {
		t.Errorf("expected '95 more lines below', got: %s", text)
	}
	if !strings.Contains(text, "offset=6") {
		t.Errorf("expected hint 'offset=6', got: %s", text)
	}
	// Should NOT have header hint (started from line 1)
	if strings.Contains(text, "lines above omitted") {
		t.Errorf("should not have header hint when starting from line 1, got: %s", text)
	}
}

func TestReadTool_BinaryFile(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.bin")
	binaryContent := []byte{0x00, 0x01, 0x02, 0x03, 0xFF, 0xFE}
	if err := os.WriteFile(filePath, binaryContent, 0644); err != nil {
		t.Fatal(err)
	}

	ws, _ := NewWorkspace(dir)
	tool := NewReadTool(ws)

	_, err := tool.Execute(context.Background(), map[string]any{
		"path": filePath,
	})
	if err == nil {
		t.Fatal("expected error for binary file")
	}
	if !strings.Contains(err.Error(), "binary") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestReadTool_NoOffsetLimit(t *testing.T) {
	// Default behavior: read entire file (no offset, no limit)
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	content := "hello\nworld\n"
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	ws, _ := NewWorkspace(dir)
	tool := NewReadTool(ws)

	result, err := tool.Execute(context.Background(), map[string]any{
		"path": filePath,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := result[0].(agentctx.TextContent).Text
	// No header or footer hints for small files
	if strings.Contains(text, "omitted") || strings.Contains(text, "more lines") {
		t.Errorf("small file should not have truncation hints, got: %s", text)
	}
}

func TestReadTool_HashlineWithOffset(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	content := "line1\nline2\nline3\nline4\nline5\n"
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	ws, _ := NewWorkspace(dir)
	tool := NewReadTool(ws)
	tool.SetHashLines(true)

	result, err := tool.Execute(context.Background(), map[string]any{
		"path":   filePath,
		"offset": float64(3),
		"limit":  float64(2),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := result[0].(agentctx.TextContent).Text

	// Hashline mode should use FormatHashLines with correct startLine
	// Lines should be numbered starting from 3
	if !strings.Contains(text, "3:") || !strings.Contains(text, "4:") {
		t.Errorf("hashline output should contain line numbers 3 and 4, got: %s", text)
	}
	// Should NOT contain lines 1-2 or 5 in hashline format
	if strings.Contains(text, "1:") || strings.Contains(text, "2:") || strings.Contains(text, "5:") {
		t.Errorf("hashline output should not contain lines 1,2,5, got: %s", text)
	}
}

func TestReadTool_TildeExpansion(t *testing.T) {
	// Test that ~/path is expanded to home directory
	dir := t.TempDir()
	filePath := filepath.Join(dir, "test.txt")
	content := "hello\n"
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	ws, _ := NewWorkspace(dir)
	tool := NewReadTool(ws)

	// Test with non-tilde path (just verify it works with absolute paths)
	result, err := tool.Execute(context.Background(), map[string]any{
		"path": filePath,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	text := result[0].(agentctx.TextContent).Text
	if text != "hello" {
		t.Errorf("expected 'hello', got %q", text)
	}
}

func TestReadTool_FileNotFound(t *testing.T) {
	ws, _ := NewWorkspace(t.TempDir())
	tool := NewReadTool(ws)

	_, err := tool.Execute(context.Background(), map[string]any{
		"path": "/nonexistent/file.txt",
	})
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}