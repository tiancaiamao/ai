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
	cwd := "/test/workspace"

	b := NewBuilder("", cwd)

	if b == nil {
		t.Fatal("NewBuilder returned nil")
	}

	if b.cwd != cwd {
		t.Errorf("expected cwd %q, got %q", cwd, b.cwd)
	}
}

func TestBuilderBuild(t *testing.T) {
	tests := []struct {
		name string
		cwd  string
	}{
		{
			name: "basic prompt",
			cwd:  "/workspace",
		},
		{
			name: "empty base",
			cwd:  "/workspace",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := NewBuilder("", tt.cwd)
			result := b.Build()

			if result == "" {
				t.Error("Build() returned empty string")
			}

			// Check that workspace section is included
			if !contains(result, "## Workspace") {
				t.Error("Workspace section missing from result")
			}

			if contains(result, "Your working directory is:") {
				t.Error("workspace section should not embed a dynamic working directory")
			}
		})
	}
}

func TestBuilderWithSkills(t *testing.T) {
	cwd := "/workspace"

	skills := []skill.Skill{
		{Name: "test", Description: "A test skill"},
	}

	b := NewBuilder("", cwd)
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
	cwd := "/workspace"

	tools := []ToolInfo{
		mockTool{name: "read", description: "Read files"},
	}
	skills := []skill.Skill{
		{Name: "test", Description: "A test skill"},
	}

	b := NewBuilder("", cwd)
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

func TestBuilderSkillsRendering(t *testing.T) {
	cwd := "/workspace"
	skills := []skill.Skill{
		{Name: "wf-issue", Description: "issue workflow", FilePath: "/tmp/wf-issue/SKILL.md"},
		{Name: "subagent", Description: "subagent workflow", FilePath: "/tmp/subagent/SKILL.md"},
	}

	b := NewBuilder("", cwd)
	b.SetSkills(skills)
	result := b.Build()

	if !contains(result, "## Skills") {
		t.Error("skills header missing")
	}
	if !contains(result, "- **wf-issue**: issue workflow (/tmp/wf-issue/SKILL.md)") {
		t.Error("full skill entry missing")
	}
	if !contains(result, "- **subagent**: subagent workflow (/tmp/subagent/SKILL.md)") {
		t.Error("full skill entry missing")
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

	b := NewBuilder("", cwd)
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

	b := NewBuilder("", cwd)
	result := b.Build()

	if !contains(result, "### CLAUDE.md") {
		t.Fatalf("expected CLAUDE.md in project context when AGENTS.md is missing")
	}
}

func TestNoWorkspaceMode(t *testing.T) {
	cwd := t.TempDir()

	// Add minimal tools for both builders
	tools := []ToolInfo{mockTool{name: "read", description: "Read files"}}

	// Test with workspace (default)
	builderWithWorkspace := NewBuilder("", cwd)
	builderWithWorkspace.SetTools(tools)
	resultWith := builderWithWorkspace.Build()
	if !contains(resultWith, "## Workspace") {
		t.Error("expected Workspace section when noWorkspace is false")
	}

	// Test without workspace (noWorkspace mode)
	builderNoWorkspace := NewBuilder("", cwd).SetNoWorkspace(true)
	builderNoWorkspace.SetTools(tools)
	resultWithout := builderNoWorkspace.Build()
	if contains(resultWithout, "## Workspace") {
		t.Error("expected no Workspace section when noWorkspace is true")
	}
	if contains(resultWithout, "Your working directory is:") {
		t.Error("expected no working directory mention when noWorkspace is true")
	}

	// Ensure the prompt still has content (it will have Tooling section)
	if resultWithout == "" {
		t.Error("expected non-empty prompt even in noWorkspace mode")
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
