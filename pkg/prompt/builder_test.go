package prompt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tiancaiamao/ai/pkg/skill"
	"github.com/tiancaiamao/ai/pkg/tools"
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

	// Skills are now in BuildSkillsMessage(), not Build()
	result := b.Build()
	if contains(result, "## Skills") {
		t.Error("Skills section should not appear in Build() output (moved to BuildSkillsMessage)")
	}

	skillsMsg := b.BuildSkillsMessage()
	if !contains(skillsMsg, "agent:skills") {
		t.Error("agent:skills wrapper missing from BuildSkillsMessage()")
	}
	if !contains(skillsMsg, "A test skill") {
		t.Error("Skill description missing from BuildSkillsMessage()")
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

	// In minimal mode, skills should be excluded from BuildSkillsMessage too
	skillsMsg := b.BuildSkillsMessage()
	if skillsMsg != "" {
		t.Error("BuildSkillsMessage should return empty in minimal mode")
	}

	// But tools and workspace should still be there
	if !contains(result, "## Tools") {
		t.Error("Tools section missing in minimal mode")
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
	skillsMsg := b.BuildSkillsMessage()

	if !contains(skillsMsg, "agent:skills") {
		t.Error("agent:skills wrapper missing")
	}
	if !contains(skillsMsg, "- **wf-issue**: issue workflow") {
		t.Error("full skill entry missing")
	}
	if !contains(skillsMsg, "- **subagent**: subagent workflow") {
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

func TestBuildInstructionsMessage(t *testing.T) {
	t.Run("loads AGENTS.md from workspace root", func(t *testing.T) {
		cwd := t.TempDir()
		if err := os.WriteFile(filepath.Join(cwd, "AGENTS.md"), []byte("# My Project\n\nSome rules."), 0644); err != nil {
			t.Fatalf("write AGENTS.md: %v", err)
		}

		b := NewBuilder("", cwd)
		got := b.BuildInstructionsMessage()

		if !strings.HasPrefix(got, "<agent:instructions>\n") {
			t.Fatalf("expected <agent:instructions> open tag, got: %q", got)
		}
		if !strings.HasSuffix(got, "\n</agent:instructions>") {
			t.Fatalf("expected </agent:instructions> close tag, got: %q", got)
		}
		if !contains(got, "# My Project") || !contains(got, "Some rules.") {
			t.Fatalf("AGENTS.md content missing from instructions message: %q", got)
		}
		// Sanity: the wrapped content must NOT leak into Build().
		if contains(b.Build(), "<agent:instructions>") {
			t.Fatalf("Build() should not contain <agent:instructions> tag")
		}
	})

	t.Run("prefers .ai/AGENTS.md over root", func(t *testing.T) {
		cwd := t.TempDir()
		if err := os.Mkdir(filepath.Join(cwd, ".ai"), 0755); err != nil {
			t.Fatalf("mkdir .ai: %v", err)
		}
		if err := os.WriteFile(filepath.Join(cwd, ".ai", "AGENTS.md"), []byte("local override"), 0644); err != nil {
			t.Fatalf("write .ai/AGENTS.md: %v", err)
		}
		if err := os.WriteFile(filepath.Join(cwd, "AGENTS.md"), []byte("root content"), 0644); err != nil {
			t.Fatalf("write AGENTS.md: %v", err)
		}

		b := NewBuilder("", cwd)
		got := b.BuildInstructionsMessage()

		if !contains(got, "local override") {
			t.Fatalf("expected .ai/AGENTS.md content; got: %q", got)
		}
		if contains(got, "root content") {
			t.Fatalf("root AGENTS.md should be shadowed by .ai/AGENTS.md; got: %q", got)
		}
	})

	t.Run("returns empty when no AGENTS.md exists", func(t *testing.T) {
		cwd := t.TempDir()
		b := NewBuilder("", cwd)
		got := b.BuildInstructionsMessage()
		if got != "" {
			t.Fatalf("expected empty string, got: %q", got)
		}
	})
}

func TestWorkspaceSectionPresent(t *testing.T) {
	cwd := t.TempDir()

	// Add minimal tools
	tools := []ToolInfo{mockTool{name: "read", description: "Read files"}}

	// The Workspace section is hardcoded in the prompt template.
	builder := NewBuilder("", cwd)
	builder.SetTools(tools)
	result := builder.Build()
	if !contains(result, "## Workspace") {
		t.Error("expected Workspace section in default build")
	}
	if !contains(result, "current_workdir") {
		t.Error("expected current_workdir guidance in Workspace section")
	}
	if result == "" {
		t.Error("expected non-empty prompt")
	}
}

func TestBuilderWithSkillStats(t *testing.T) {
	cwd := "/workspace"

	skills := []skill.Skill{
		{Name: "popular", Description: "A popular skill", FilePath: "/tmp/popular/SKILL.md"},
		{Name: "unpopular", Description: "An unpopular skill", FilePath: "/tmp/unpopular/SKILL.md"},
		{Name: "medium", Description: "A medium skill", FilePath: "/tmp/medium/SKILL.md"},
	}

	stats := &skill.SkillStatsFile{
		Version: 1,
		TopN:    2,
		Entries: map[string]*skill.SkillUsageEntry{
			"popular":   {Name: "popular", Count: 100, Score: 100.0},
			"unpopular": {Name: "unpopular", Count: 1, Score: 1.0},
		},
	}

	b := NewBuilder("", cwd)
	b.SetSkills(skills)
	b.SetSkillStats(stats)
	skillsMsg := b.BuildSkillsMessage()

	if !contains(skillsMsg, "agent:skills") {
		t.Error("agent:skills wrapper missing")
	}

	// With TopN=2 and "popular" ranked highest, "medium" should not appear
	// because it has no stats entry and popular + unpopular fill the top 2.
	// Actually, let's check: popular (ranked), unpopular (ranked) → top 2 from stats.
	// "medium" has no stats → it gets added as unranked supplement only if room.
	// TopN=2, ranked=2 → selected has 2 → medium is excluded.
	if !contains(skillsMsg, "**popular**") {
		t.Error("popular skill should appear")
	}
	if contains(skillsMsg, "**medium**") {
		t.Error("medium skill should be filtered out (TopN=2, not in top entries)")
	}

	// Stats present → should include find_skill hint
	if !contains(skillsMsg, "find_skill") {
		t.Error("find_skill hint should appear when stats are set")
	}
}

func TestBuilderWithSkillStatsNil(t *testing.T) {
	cwd := "/workspace"

	skills := []skill.Skill{
		{Name: "test", Description: "A test skill", FilePath: "/tmp/test/SKILL.md"},
	}

	b := NewBuilder("", cwd)
	b.SetSkills(skills)
	// SetSkillStats not called → skillStats is nil → backward compat
	skillsMsg := b.BuildSkillsMessage()

	if !contains(skillsMsg, "agent:skills") {
		t.Error("agent:skills wrapper missing")
	}
	if !contains(skillsMsg, "A test skill") {
		t.Error("Skill description missing")
	}
	// No stats → should NOT show find_skill hint
	if contains(skillsMsg, "find_skill") {
		t.Error("find_skill hint should not appear when stats are nil")
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

func TestCompactAndTemplateAccessors(t *testing.T) {
	// Simple accessor coverage for the compact/role template helpers.
	if got := CompactSystemPrompt(); got == "" {
		t.Error("CompactSystemPrompt returned empty string")
	}
	if got := CompactSummarizePrompt(); got == "" {
		t.Error("CompactSummarizePrompt returned empty string")
	}
	if got := CompactUpdatePrompt(); got == "" {
		t.Error("CompactUpdatePrompt returned empty string")
	}
	if got := OrchestratorTemplate(); got == "" {
		t.Error("OrchestratorTemplate returned empty string")
	}
	if got := ValidatorTemplate(); got == "" {
		t.Error("ValidatorTemplate returned empty string")
	}
}

func TestTemplateForRole(t *testing.T) {
	cases := []struct {
		role    string
		wantErr bool
	}{
		{"", false},
		{"coder", false},
		{"orchestrator", false},
		{"validator", false},
		{"unknown", true},
	}
	for _, c := range cases {
		got, err := TemplateForRole(c.role)
		if c.wantErr {
			if err == nil {
				t.Errorf("role %q: expected error, got nil", c.role)
			}
			continue
		}
		if err != nil {
			t.Errorf("role %q: unexpected error: %v", c.role, err)
		}
		if got == "" {
			t.Errorf("role %q: got empty template", c.role)
		}
	}
}

func TestBuilderSetters(t *testing.T) {
	// Coverage for the simple setter chain + minimal-build path.
	b := NewBuilder("", "")

	// All Set* methods should return the builder for chaining.
	if got := b.SetMinimal(true); got != b {
		t.Error("SetMinimal did not return builder")
	}
	if got := b.SetTemplate("custom template {{workspace_section}} {{tool_descriptions}}"); got != b {
		t.Error("SetTemplate did not return builder")
	}
	if got := b.SetContextMeta("meta"); got != b {
		t.Error("SetContextMeta did not return builder")
	}
	if got := b.SetTokensPercent(42.5); got != b {
		t.Error("SetTokensPercent did not return builder")
	}

	// Build with custom template should still produce a non-empty string.
	if out := b.Build(); out == "" {
		t.Error("Build returned empty string with custom template")
	}
}

func TestNewBuilderWithWorkspaceAndGetCWD(t *testing.T) {
	// With workspace: GetCWD should delegate to workspace.GetCWD()
	ws := tools.MustNewWorkspace("/custom/cwd")
	b := NewBuilderWithWorkspace("ignored", ws)
	if got := b.GetCWD(); got != "/custom/cwd" {
		t.Errorf("GetCWD with workspace = %q, want %q", got, "/custom/cwd")
	}

	// Without workspace: GetCWD should fall back to b.cwd
	b2 := NewBuilder("", "/fallback/cwd")
	if got := b2.GetCWD(); got != "/fallback/cwd" {
		t.Errorf("GetCWD without workspace = %q, want %q", got, "/fallback/cwd")
	}
}
