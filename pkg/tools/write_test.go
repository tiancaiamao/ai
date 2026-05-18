package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

func newWriteToolInTempDir(t *testing.T) (*WriteTool, string) {
	t.Helper()
	dir := t.TempDir()
	ws, err := NewWorkspace(dir)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	return NewWriteTool(ws), dir
}

// ---------------------------------------------------------------------------
// Interface
// ---------------------------------------------------------------------------

func TestWriteTool_Name(t *testing.T) {
	tool, _ := newWriteToolInTempDir(t)
	if tool.Name() != "write" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "write")
	}
}

func TestWriteTool_Description(t *testing.T) {
	tool, _ := newWriteToolInTempDir(t)
	if tool.Description() == "" {
		t.Error("Description() should not be empty")
	}
}

func TestWriteTool_Parameters(t *testing.T) {
	tool, _ := newWriteToolInTempDir(t)
	params := tool.Parameters()
	if params["type"] != "object" {
		t.Errorf("Parameters type = %v, want object", params["type"])
	}
}

// ---------------------------------------------------------------------------
// Core functionality
// ---------------------------------------------------------------------------

func TestWriteTool_NewFile(t *testing.T) {
	tool, dir := newWriteToolInTempDir(t)

	result, err := tool.Execute(context.Background(), map[string]any{
		"path":    "hello.txt",
		"content": "hello world",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Verify file was written
	got, err := os.ReadFile(filepath.Join(dir, "hello.txt"))
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(got) != "hello world" {
		t.Errorf("file content = %q, want %q", string(got), "hello world")
	}

	// Verify result message
	text := result[0].(agentctx.TextContent).Text
	if !strings.Contains(text, "Successfully wrote") {
		t.Errorf("result = %q, should contain 'Successfully wrote'", text)
	}
	if !strings.Contains(text, "11 bytes") {
		t.Errorf("result = %q, should contain byte count", text)
	}
}

func TestWriteTool_OverwriteExisting(t *testing.T) {
	tool, dir := newWriteToolInTempDir(t)
	path := filepath.Join(dir, "file.txt")
	os.WriteFile(path, []byte("old content"), 0644)

	_, err := tool.Execute(context.Background(), map[string]any{
		"path":    "file.txt",
		"content": "new content",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	got, _ := os.ReadFile(path)
	if string(got) != "new content" {
		t.Errorf("content = %q, want %q", string(got), "new content")
	}
}

func TestWriteTool_CreatesParentDirectory(t *testing.T) {
	tool, dir := newWriteToolInTempDir(t)

	_, err := tool.Execute(context.Background(), map[string]any{
		"path":    "sub/dir/deep/file.txt",
		"content": "nested",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dir, "sub", "dir", "deep", "file.txt"))
	if err != nil {
		t.Fatalf("read nested file: %v", err)
	}
	if string(got) != "nested" {
		t.Errorf("content = %q, want %q", string(got), "nested")
	}
}

func TestWriteTool_AbsolutePath(t *testing.T) {
	tool, dir := newWriteToolInTempDir(t)
	absPath := filepath.Join(dir, "abs.txt")

	_, err := tool.Execute(context.Background(), map[string]any{
		"path":    absPath,
		"content": "absolute",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	got, _ := os.ReadFile(absPath)
	if string(got) != "absolute" {
		t.Errorf("content = %q, want %q", string(got), "absolute")
	}
}

func TestWriteTool_EmptyContent(t *testing.T) {
	tool, dir := newWriteToolInTempDir(t)

	result, err := tool.Execute(context.Background(), map[string]any{
		"path":    "empty.txt",
		"content": "",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	got, _ := os.ReadFile(filepath.Join(dir, "empty.txt"))
	if string(got) != "" {
		t.Errorf("content = %q, want empty", string(got))
	}
	text := result[0].(agentctx.TextContent).Text
	if !strings.Contains(text, "0 bytes") {
		t.Errorf("result = %q, should contain '0 bytes'", text)
	}
}

func TestWriteTool_LargeContent(t *testing.T) {
	tool, dir := newWriteToolInTempDir(t)
	content := strings.Repeat("x", 100000)

	_, err := tool.Execute(context.Background(), map[string]any{
		"path":    "large.txt",
		"content": content,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	got, _ := os.ReadFile(filepath.Join(dir, "large.txt"))
	if len(got) != 100000 {
		t.Errorf("file size = %d, want 100000", len(got))
	}
}

// ---------------------------------------------------------------------------
// Error cases
// ---------------------------------------------------------------------------

func TestWriteTool_MissingPath(t *testing.T) {
	tool, _ := newWriteToolInTempDir(t)

	_, err := tool.Execute(context.Background(), map[string]any{
		"content": "hello",
	})
	if err == nil {
		t.Fatal("expected error for missing path")
	}
}

func TestWriteTool_MissingContent(t *testing.T) {
	tool, _ := newWriteToolInTempDir(t)

	_, err := tool.Execute(context.Background(), map[string]any{
		"path": "test.txt",
	})
	if err == nil {
		t.Fatal("expected error for missing content")
	}
}

func TestWriteTool_InvalidPathType(t *testing.T) {
	tool, _ := newWriteToolInTempDir(t)

	_, err := tool.Execute(context.Background(), map[string]any{
		"path":    123,
		"content": "hello",
	})
	if err == nil {
		t.Fatal("expected error for non-string path")
	}
}