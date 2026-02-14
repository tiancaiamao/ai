package prompt

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tiancaiamao/ai/pkg/skill"
)

// mockTool implements ToolInfo for testing
type mockTool struct {
	name        string
	description string
}

func (m mockTool) Name() string        { return m.name }
func (m mockTool) Description() string { return m.description }

func TestNewBuilder(t *testing.T) {
	base := "You are a helpful assistant."
	cwd := "/test/workspace"

	b := NewBuilder(base, cwd)

	if b == nil {
		t.Fatal("NewBuilder returned nil")
	}

	if b.base != base {
		t.Errorf("expected base %q, got %q", base, b.base)
	}

	if b.cwd != cwd {
		t.Errorf("expected cwd %q, got %q", cwd, b.cwd)
	}
}

func TestBuilderBuild(t *testing.T) {
	tests := []struct {
		name string
		base string
		cwd  string
	}{
		{
			name: "basic prompt",
			base: "You are an AI assistant.",
			cwd:  "/workspace",
		},
		{
			name: "empty base",
			base: "",
			cwd:  "/workspace",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := NewBuilder(tt.base, tt.cwd)
			result := b.Build()

			if result == "" {
				t.Error("Build() returned empty string")
			}

			// Check that workspace section is included
			if !contains(result, "## Workspace") {
				t.Error("Workspace section missing from result")
			}

			if !contains(result, tt.cwd) {
				t.Errorf("CWD %q not found in result", tt.cwd)
			}
		})
	}
}

func TestBuilderWithTools(t *testing.T) {
	base := "You are an AI assistant."
	cwd := "/workspace"

	tools := []ToolInfo{
		mockTool{name: "read", description: "Read files"},
		mockTool{name: "write", description: "Write files"},
	}

	b := NewBuilder(base, cwd)
	b.SetTools(tools)
	result := b.Build()

	if !contains(result, "## Tooling") {
		t.Error("Tooling section missing")
	}

	if !contains(result, "read: Read files") {
		t.Error("Tool 'read' missing from result")
	}

	if !contains(result, "write: Write files") {
		t.Error("Tool 'write' missing from result")
	}

	if !contains(result, "Only use the tools listed above") {
		t.Error("Tool limitation warning missing")
	}
}

func TestBuilderWithSkills(t *testing.T) {
	base := "You are an AI assistant."
	cwd := "/workspace"

	skills := []skill.Skill{
		{Name: "test", Description: "A test skill"},
	}

	b := NewBuilder(base, cwd)
	b.SetSkills(skills)
	result := b.Build()

	if !contains(result, "## Skills") {
		t.Error("Skills section missing")
	}

	if !contains(result, "A test skill") {
		t.Error("Skill description missing")
	}
}

func TestBuilderMinimalMode(t *testing.T) {
	base := "You are an AI assistant."
	cwd := "/workspace"

	tools := []ToolInfo{
		mockTool{name: "read", description: "Read files"},
	}
	skills := []skill.Skill{
		{Name: "test", Description: "A test skill"},
	}

	b := NewBuilder(base, cwd)
	b.SetTools(tools).SetSkills(skills).SetMinimal(true)
	result := b.Build()

	// In minimal mode, skills should be excluded
	if contains(result, "## Skills") {
		t.Error("Skills section should not appear in minimal mode")
	}

	// But tools and workspace should still be there
	if !contains(result, "## Tooling") {
		t.Error("Tooling section missing in minimal mode")
	}

	if !contains(result, "## Workspace") {
		t.Error("Workspace section missing in minimal mode")
	}
}

func TestThinkingInstruction(t *testing.T) {
	tests := []struct {
		level    string
		contains string
	}{
		{"off", "Thinking level is off"},
		{"minimal", "Thinking level is minimal"},
		{"low", "Thinking level is low"},
		{"medium", "Thinking level is medium"},
		{"high", "Thinking level is high"},
		{"xhigh", "Thinking level is xhigh"},
		{"", "Thinking level is high"},        // default
		{"invalid", "Thinking level is high"}, // default for invalid
	}

	for _, tt := range tests {
		t.Run(tt.level, func(t *testing.T) {
			result := ThinkingInstruction(tt.level)

			if !contains(result, tt.contains) {
				t.Errorf("ThinkingInstruction(%q) = %q, want to contain %q", tt.level, result, tt.contains)
			}
		})
	}
}

func TestNormalizeThinkingLevel(t *testing.T) {
	tests := []struct {
		input  string
		output string
	}{
		{"off", "off"},
		{"OFF", "off"},
		{"minimal", "minimal"},
		{"low", "low"},
		{"medium", "medium"},
		{"high", "high"},
		{"xhigh", "xhigh"},
		{"", "high"},        // default
		{"invalid", "high"}, // default for invalid
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := NormalizeThinkingLevel(tt.input)

			if result != tt.output {
				t.Errorf("NormalizeThinkingLevel(%q) = %q, want %q", tt.input, result, tt.output)
			}
		})
	}
}

func TestConvertTools(t *testing.T) {
	tools := []ToolInfo{
		mockTool{name: "tool1", description: "Desc 1"},
		mockTool{name: "tool2", description: "Desc 2"},
	}

	result := convertTools(tools)

	if len(result) != 2 {
		t.Fatalf("convertTools() returned %d items, want 2", len(result))
	}

	if result[0].Name() != "tool1" {
		t.Errorf("result[0].Name() = %q, want %q", result[0].Name(), "tool1")
	}

	if result[1].Description() != "Desc 2" {
		t.Errorf("result[1].Description() = %q, want %q", result[1].Description(), "Desc 2")
	}
}

func TestConvertToolsNil(t *testing.T) {
	result := convertTools(nil)

	if result != nil {
		t.Errorf("convertTools(nil) = %v, want nil", result)
	}
}

func TestConvertToolsNonSlice(t *testing.T) {
	result := convertTools("not a slice")

	if result != nil {
		t.Errorf("convertTools(\"not a slice\") = %v, want nil", result)
	}
}

func TestProjectContextPrefersAgentsOverClaude(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "AGENTS.md"), []byte("agents instructions"), 0644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "CLAUDE.md"), []byte("claude instructions"), 0644); err != nil {
		t.Fatalf("write CLAUDE.md: %v", err)
	}

	b := NewBuilder("base", cwd)
	result := b.Build()

	if !contains(result, "### AGENTS.md") {
		t.Fatalf("expected AGENTS.md in project context")
	}
	if contains(result, "### CLAUDE.md") {
		t.Fatalf("expected CLAUDE.md to be skipped when AGENTS.md exists")
	}
}

func TestProjectContextUsesClaudeWhenAgentsMissing(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "CLAUDE.md"), []byte("claude instructions"), 0644); err != nil {
		t.Fatalf("write CLAUDE.md: %v", err)
	}

	b := NewBuilder("base", cwd)
	result := b.Build()

	if !contains(result, "### CLAUDE.md") {
		t.Fatalf("expected CLAUDE.md in project context when AGENTS.md is missing")
	}
}

// Helper function
func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
