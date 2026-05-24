package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
)

func newChangeWorkspaceTool(t *testing.T) (*ChangeWorkspaceTool, string) {
	t.Helper()
	dir := t.TempDir()
	ws, err := NewWorkspace(dir)
	if err != nil {
		t.Fatalf("NewWorkspace: %v", err)
	}
	return NewChangeWorkspaceTool(ws), dir
}

// ---------------------------------------------------------------------------
// Interface
// ---------------------------------------------------------------------------

func TestChangeWorkspaceTool_Name(t *testing.T) {
	tool, _ := newChangeWorkspaceTool(t)
	if tool.Name() != "change_workspace" {
		t.Errorf("Name() = %q, want %q", tool.Name(), "change_workspace")
	}
}

func TestChangeWorkspaceTool_Description(t *testing.T) {
	tool, _ := newChangeWorkspaceTool(t)
	if tool.Description() == "" {
		t.Error("Description() should not be empty")
	}
}

func TestChangeWorkspaceTool_Parameters(t *testing.T) {
	tool, _ := newChangeWorkspaceTool(t)
	params := tool.Parameters()
	if params["type"] != "object" {
		t.Errorf("Parameters type = %v, want object", params["type"])
	}
}

// ---------------------------------------------------------------------------
// Execute
// ---------------------------------------------------------------------------

func TestChangeWorkspaceTool_AbsolutePath(t *testing.T) {
	tool, dir := newChangeWorkspaceTool(t)
	target := filepath.Join(dir, "target")
	os.MkdirAll(target, 0755)

	result, err := tool.Execute(context.Background(), map[string]any{
		"path": target,
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	text := result[0].(agentctx.TextContent).Text
	if !strings.Contains(text, "Workspace changed to") {
		t.Errorf("result = %q, should contain 'Workspace changed to'", text)
	}
	if !strings.Contains(text, target) {
		t.Errorf("result = %q, should contain target path", text)
	}
}

func TestChangeWorkspaceTool_RelativePath(t *testing.T) {
	tool, dir := newChangeWorkspaceTool(t)
	sub := filepath.Join(dir, "subdir")
	os.MkdirAll(sub, 0755)

	result, err := tool.Execute(context.Background(), map[string]any{
		"path": "subdir",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	text := result[0].(agentctx.TextContent).Text
	if !strings.Contains(text, "Workspace changed to") {
		t.Errorf("result = %q, should contain 'Workspace changed to'", text)
	}
}

func TestChangeWorkspaceTool_NonExistentPath(t *testing.T) {
	tool, _ := newChangeWorkspaceTool(t)

	result, err := tool.Execute(context.Background(), map[string]any{
		"path": "/nonexistent/directory/xyz",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	text := result[0].(agentctx.TextContent).Text
	if !strings.Contains(text, "Failed to change workspace") {
		t.Errorf("result = %q, should contain failure message", text)
	}
}

func TestChangeWorkspaceTool_InvalidArg(t *testing.T) {
	tool, _ := newChangeWorkspaceTool(t)

	_, err := tool.Execute(context.Background(), map[string]any{
		"path": 123,
	})
	if err == nil {
		t.Fatal("expected error for invalid path type")
	}
}
