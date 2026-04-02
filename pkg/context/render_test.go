package context

import (
	"strings"
	"testing"
)

// TestRender_NormalMode_ToolCallIDHidden tests that in Normal mode,
// the tool_call_id is not visible to the LLM (Category 3.1).
func TestRender_NormalMode_ToolCallIDHidden(t *testing.T) {
	// Given: A tool result message
	msg := NewToolResultMessage("call_abc", "bash", []ContentBlock{
		TextContent{Type: "text", Text: "output content"},
	}, false)

	// When: Rendering in Normal mode
	rendered := RenderToolResult(&msg, ModeNormal, 5)

	// Then: No tool_call_id visible
	if strings.Contains(rendered, "call_abc") {
		t.Errorf("Normal mode should hide tool_call_id, but got: %s", rendered)
	}

	if strings.Contains(rendered, "<agent:tool") {
		t.Errorf("Normal mode should not use <agent:tool> tag, but got: %s", rendered)
	}

	if !strings.Contains(rendered, "output content") {
		t.Errorf("Normal mode should show content, but got: %s", rendered)
	}

	if rendered != "output content" {
		t.Errorf("Normal mode should return raw content, got: %s", rendered)
	}
}

// TestRender_ContextMgmtMode_ToolCallIDVisible tests that in Context Management mode,
// the tool_call_id and metadata are visible to the LLM (Category 3.2).
func TestRender_ContextMgmtMode_ToolCallIDVisible(t *testing.T) {
	// Given: A tool result message
	msg := NewToolResultMessage("call_abc", "bash", []ContentBlock{
		TextContent{Type: "text", Text: "output content"},
	}, false)

	// When: Rendering in Context Management mode
	rendered := RenderToolResult(&msg, ModeContextMgmt, 5)

	// Then: Metadata present in XML format
	if !strings.Contains(rendered, `id="call_abc"`) {
		t.Errorf("ContextMgmt mode should expose tool_call_id, got: %s", rendered)
	}

	if !strings.Contains(rendered, `stale="5"`) {
		t.Errorf("ContextMgmt mode should show stale value, got: %s", rendered)
	}

	if !strings.Contains(rendered, `chars="14"`) {
		t.Errorf("ContextMgmt mode should show char count, got: %s", rendered)
	}

	if !strings.Contains(rendered, "output content") {
		t.Errorf("ContextMgmt mode should show content, got: %s", rendered)
	}

	if !strings.Contains(rendered, "<agent:tool") {
		t.Errorf("ContextMgmt mode should use <agent:tool> tag, got: %s", rendered)
	}
}

// TestRender_ContextMgmtMode_LargeOutputPreview tests that large outputs
// are truncated to a preview in Context Management mode.
func TestRender_ContextMgmtMode_LargeOutputPreview(t *testing.T) {
	// Given: A large tool result (> 2000 chars)
	largeContent := strings.Repeat("x", 5000)
	msg := NewToolResultMessage("call_large", "grep", []ContentBlock{
		TextContent{Type: "text", Text: largeContent},
	}, false)

	// When: Rendering in Context Management mode
	rendered := RenderToolResult(&msg, ModeContextMgmt, 3)

	// Then: Should show preview, not full content
	if strings.Contains(rendered, largeContent) {
		t.Error("Large output should be truncated to preview")
	}

	if !strings.Contains(rendered, `chars="5000"`) {
		t.Error("Large output should preserve original char count in metadata")
	}

	if !strings.Contains(rendered, "truncated") {
		t.Error("Large output should indicate truncation")
	}

	// Verify preview format (head + ... + tail)
	if !strings.Contains(rendered, "xxx") {
		t.Error("Preview should contain head content")
	}

	// Verify that full content is NOT present (it should be truncated)
	if strings.Contains(rendered, largeContent) {
		t.Error("Large output should be truncated, not show full content")
	}

	// Verify truncation message is present
	if !strings.Contains(rendered, "truncated") {
		t.Error("Preview should indicate truncation")
	}
}

// TestRender_ContextMgmtMode_SmallOutputNoPreview tests that small outputs
// are not truncated.
func TestRender_ContextMgmtMode_SmallOutputNoPreview(t *testing.T) {
	// Given: A small tool result (< 2000 chars)
	smallContent := "small output"
	msg := NewToolResultMessage("call_small", "bash", []ContentBlock{
		TextContent{Type: "text", Text: smallContent},
	}, false)

	// When: Rendering in Context Management mode
	rendered := RenderToolResult(&msg, ModeContextMgmt, 2)

	// Then: Should show full content, no preview truncation
	if !strings.Contains(rendered, smallContent) {
		t.Errorf("Small output should show full content, got: %s", rendered)
	}

	if strings.Contains(rendered, "truncated") {
		t.Error("Small output should not be truncated")
	}

	if !strings.Contains(rendered, `chars="12"`) {
		t.Errorf("Small output should show correct char count, got: %s", rendered)
	}
}
