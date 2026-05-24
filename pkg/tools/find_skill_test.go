package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/skill"
)

// helper extracts text from the first ContentBlock.
func firstText(blocks []agentctx.ContentBlock) string {
	if len(blocks) == 0 {
		return ""
	}
	if tc, ok := blocks[0].(agentctx.TextContent); ok {
		return tc.Text
	}
	return ""
}

func testSkills() []skill.Skill {
	return []skill.Skill{
		{
			Name:        "review",
			Description: "Code review skill using codex-rs methodology with ag CLI",
			FilePath:    "/home/user/.ai/skills/review/SKILL.md",
			Content:     "# Review Skill\n\nThis is the full review skill content.",
		},
		{
			Name:        "plan",
			Description: "Read design.md, produce tasks.md with plan-lint validation",
			FilePath:    "/home/user/.ai/skills/plan/SKILL.md",
			Content:     "# Plan Skill\n\nThis is the full plan skill content.",
		},
		{
			Name:        "github",
			Description: "Interact with GitHub using the gh CLI",
			FilePath:    "/home/user/.ai/skills/github/SKILL.md",
			Content:     "# GitHub Skill\n\nThis is the full github skill content.",
		},
		{
			Name:        "test-driven-development",
			Description: "Use during IMPLEMENTATION phase. Write test first, watch it fail, then write minimal code to pass.",
			FilePath:    "/home/user/.ai/skills/tdd/SKILL.md",
			Content:     "# TDD Skill\n\nFull TDD content here.",
		},
	}
}

func newTestStats(t *testing.T) *skill.SkillStatsFile {
	t.Helper()
	return skill.LoadStats(filepath.Join(t.TempDir(), "stats.json"))
}

// newTestToolWithIndex creates a FindSkillTool with a custom index path for testing.
func newTestToolWithIndex(skills []skill.Skill, stats *skill.SkillStatsFile, indexDir string) *FindSkillTool {
	indexPath := filepath.Join(indexDir, "skill-index.json")
	return &FindSkillTool{
		skills:    skills,
		stats:     stats,
		indexPath: indexPath,
	}
}

// writeTestIndex writes a skill-index.json file in the given directory.
func writeTestIndex(t *testing.T, dir string, entries []SkillIndexEntry) {
	t.Helper()
	idx := SkillIndex{
		Version:     1,
		GeneratedAt: "2025-01-01T00:00:00Z",
		EntryCount:  len(entries),
		Entries:     entries,
	}
	data, err := json.Marshal(idx)
	if err != nil {
		t.Fatalf("Failed to marshal index: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "skill-index.json"), data, 0644); err != nil {
		t.Fatalf("Failed to write index: %v", err)
	}
}

func TestFindSkill_SearchReturnsMatchingSkills(t *testing.T) {
	tool := NewFindSkillTool(testSkills(), nil)

	result, err := tool.Execute(context.Background(), map[string]any{
		"query": "review",
	})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	text := firstText(result)
	if !strings.Contains(text, "review") {
		t.Errorf("Expected 'review' in result, got: %s", text)
	}
	if !strings.Contains(text, "SKILL.md") {
		t.Errorf("Expected file path in result, got: %s", text)
	}
}

func TestFindSkill_SearchNoMatches(t *testing.T) {
	tool := NewFindSkillTool(testSkills(), nil)

	result, err := tool.Execute(context.Background(), map[string]any{
		"query": "nonexistent-skill-xyz",
	})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	text := firstText(result)
	if !strings.Contains(text, "No skills found matching") {
		t.Errorf("Expected no-matches message, got: %s", text)
	}
	if !strings.Contains(text, "nonexistent-skill-xyz") {
		t.Errorf("Expected query echoed in message, got: %s", text)
	}
}

func TestFindSkill_LoadReturnsFullContent(t *testing.T) {
	tool := NewFindSkillTool(testSkills(), nil)

	result, err := tool.Execute(context.Background(), map[string]any{
		"query": "anything",
		"load":  true,
		"name":  "review",
	})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	text := firstText(result)
	if !strings.Contains(text, "full review skill content") {
		t.Errorf("Expected full skill content, got: %s", text)
	}
}

func TestFindSkill_LoadNonexistentReturnsError(t *testing.T) {
	tool := NewFindSkillTool(testSkills(), nil)

	_, err := tool.Execute(context.Background(), map[string]any{
		"query": "anything",
		"load":  true,
		"name":  "does-not-exist",
	})
	if err == nil {
		t.Fatal("Expected error for nonexistent skill load, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("Expected 'not found' in error, got: %v", err)
	}
}

func TestFindSkill_CaseInsensitiveSearch(t *testing.T) {
	tool := NewFindSkillTool(testSkills(), nil)
	// Avoid loading the real user's skill-index.json which can inject
	// unrelated matches and push expected results out of the top-5 limit.
	tool.SetIndexPath(filepath.Join(t.TempDir(), "nonexistent-skill-index.json"))

	// Search with uppercase should find lowercase skill names
	result, err := tool.Execute(context.Background(), map[string]any{
		"query": "GITHUB",
	})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	text := firstText(result)
	if !strings.Contains(text, "github") {
		t.Errorf("Expected 'github' in result for uppercase query, got: %s", text)
	}

	// Mixed case search on description
	result, err = tool.Execute(context.Background(), map[string]any{
		"query": "implementation",
	})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	text = firstText(result)
	if !strings.Contains(text, "test-driven-development") {
		t.Errorf("Expected 'test-driven-development' in result, got: %s", text)
	}
}

func TestFindSkill_RecordUsageCalledOnSearch(t *testing.T) {
	stats := newTestStats(t)
	tool := NewFindSkillTool(testSkills(), stats)

	_, err := tool.Execute(context.Background(), map[string]any{
		"query": "review",
	})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	entry, ok := stats.Entries["review"]
	if !ok {
		t.Fatal("Expected 'review' usage entry in stats")
	}
	if entry.Count != 1 {
		t.Errorf("Expected count=1, got %d", entry.Count)
	}
}

func TestFindSkill_RecordUsageOnLoad(t *testing.T) {
	stats := newTestStats(t)
	tool := NewFindSkillTool(testSkills(), stats)

	_, err := tool.Execute(context.Background(), map[string]any{
		"query": "x",
		"load":  true,
		"name":  "plan",
	})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	entry, ok := stats.Entries["plan"]
	if !ok {
		t.Fatal("Expected 'plan' usage entry in stats after load")
	}
	if entry.Count != 1 {
		t.Errorf("Expected count=1, got %d", entry.Count)
	}
}

func TestFindSkill_NilStatsDoesNotCrash(t *testing.T) {
	tool := NewFindSkillTool(testSkills(), nil)

	result, err := tool.Execute(context.Background(), map[string]any{
		"query": "review",
	})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if len(result) == 0 {
		t.Error("Expected non-empty result")
	}
}

func TestFindSkill_EmptySkillsList(t *testing.T) {
	tool := NewFindSkillTool([]skill.Skill{}, nil)

	result, err := tool.Execute(context.Background(), map[string]any{
		"query": "anything",
	})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	text := firstText(result)
	if !strings.Contains(text, "No skills found matching") {
		t.Errorf("Expected no-matches message for empty skills, got: %s", text)
	}
}

func TestFindSkill_NoQueryReturnsError(t *testing.T) {
	tool := NewFindSkillTool(testSkills(), nil)

	_, err := tool.Execute(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("Expected error when no query provided, got nil")
	}
	if !strings.Contains(err.Error(), "query") {
		t.Errorf("Expected error about query parameter, got: %v", err)
	}
}

func TestFindSkill_SearchFooterHint(t *testing.T) {
	tool := NewFindSkillTool(testSkills(), nil)

	result, err := tool.Execute(context.Background(), map[string]any{
		"query": "github",
	})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	text := firstText(result)
	if !strings.Contains(text, "load=true") {
		t.Errorf("Expected footer with load=true hint, got: %s", text)
	}
}

func TestFindSkill_SearchLimitsToFive(t *testing.T) {
	// Create 10 skills all matching "skill"
	var skills []skill.Skill
	for i := 0; i < 10; i++ {
		skills = append(skills, skill.Skill{
			Name:        fmt.Sprintf("skill-%d", i),
			Description: "A test skill",
			FilePath:    fmt.Sprintf("/path/skill-%d.md", i),
			Content:     "content",
		})
	}
	tool := NewFindSkillTool(skills, nil)
	tool.SetIndexPath("") // disable index loading to test pure skill search

	result, err := tool.Execute(context.Background(), map[string]any{
		"query": "skill",
	})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	text := firstText(result)

	// Count how many skill names appear (lines starting with "- skill-")
	count := strings.Count(text, "- skill-")
	if count != 5 {
		t.Errorf("Expected exactly 5 results, got %d", count)
	}
}

// --- Index-aware tests ---

func TestFindSkill_IndexAliasMatch(t *testing.T) {
	dir := t.TempDir()
	writeTestIndex(t, dir, []SkillIndexEntry{
		{
			Name:        "systematic-debugging",
			Description: "Use when encountering bugs, test failures, or unexpected behavior",
			Aliases:     []string{"troubleshoot", "debug", "调试"},
			UseWhen:     []string{"encountering bugs", "test failures"},
			Categories:  []string{"debugging"},
		},
	})

	tool := newTestToolWithIndex(testSkills(), nil, dir)

	// Search for alias "debug" — should find systematic-debugging via index
	result, err := tool.Execute(context.Background(), map[string]any{
		"query": "debug",
	})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	text := firstText(result)
	if !strings.Contains(text, "systematic-debugging") {
		t.Errorf("Expected 'systematic-debugging' found via alias 'debug', got: %s", text)
	}
	if !strings.Contains(text, "from index") {
		t.Errorf("Expected 'from index' note for unloaded skill, got: %s", text)
	}
}

func TestFindSkill_IndexUseWhenMatch(t *testing.T) {
	dir := t.TempDir()
	writeTestIndex(t, dir, []SkillIndexEntry{
		{
			Name:        "systematic-debugging",
			Description: "Use when encountering bugs, test failures, or unexpected behavior",
			Aliases:     []string{"troubleshoot"},
			UseWhen:     []string{"encountering bugs", "test failures", "unexpected behavior"},
			Categories:  []string{"debugging"},
		},
	})

	tool := newTestToolWithIndex(testSkills(), nil, dir)

	// Search for "test failures" from use_when
	result, err := tool.Execute(context.Background(), map[string]any{
		"query": "test failures",
	})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	text := firstText(result)
	if !strings.Contains(text, "systematic-debugging") {
		t.Errorf("Expected 'systematic-debugging' found via use_when 'test failures', got: %s", text)
	}
}

func TestFindSkill_IndexCategoryMatch(t *testing.T) {
	dir := t.TempDir()
	writeTestIndex(t, dir, []SkillIndexEntry{
		{
			Name:        "systematic-debugging",
			Description: "Use when encountering bugs, test failures, or unexpected behavior",
			Aliases:     []string{"troubleshoot"},
			UseWhen:     []string{"encountering bugs"},
			Categories:  []string{"debugging", "development"},
		},
	})

	tool := newTestToolWithIndex(testSkills(), nil, dir)

	// Search for "debugging" from category
	result, err := tool.Execute(context.Background(), map[string]any{
		"query": "debugging",
	})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	text := firstText(result)
	if !strings.Contains(text, "systematic-debugging") {
		t.Errorf("Expected 'systematic-debugging' found via category 'debugging', got: %s", text)
	}
}

func TestFindSkill_NoIndexFile_FallsBackToDirectMatch(t *testing.T) {
	dir := t.TempDir()
	// Don't write an index file — should still work with direct matching
	tool := newTestToolWithIndex(testSkills(), nil, dir)

	result, err := tool.Execute(context.Background(), map[string]any{
		"query": "review",
	})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	text := firstText(result)
	if !strings.Contains(text, "review") {
		t.Errorf("Expected 'review' in result without index, got: %s", text)
	}
}

func TestFindSkill_MalformedIndexFile_FallsBack(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "skill-index.json"), []byte("not valid json{"), 0644); err != nil {
		t.Fatalf("Failed to write malformed index: %v", err)
	}

	tool := newTestToolWithIndex(testSkills(), nil, dir)

	result, err := tool.Execute(context.Background(), map[string]any{
		"query": "review",
	})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	text := firstText(result)
	if !strings.Contains(text, "review") {
		t.Errorf("Expected 'review' in result with malformed index, got: %s", text)
	}
}

func TestFindSkill_Deduplication(t *testing.T) {
	dir := t.TempDir()
	// Index entry for "review" which also exists in loaded skills
	writeTestIndex(t, dir, []SkillIndexEntry{
		{
			Name:        "review",
			Description: "Code review skill",
			Aliases:     []string{"code review", "pr review"},
			UseWhen:     []string{"code review needed"},
			Categories:  []string{"review"},
		},
	})

	tool := newTestToolWithIndex(testSkills(), nil, dir)

	// Search for "review" — should find review only once
	result, err := tool.Execute(context.Background(), map[string]any{
		"query": "review",
	})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	text := firstText(result)

	// Count occurrences of "- review:" in result
	count := strings.Count(text, "- review:")
	if count != 1 {
		t.Errorf("Expected exactly 1 occurrence of 'review' (deduped), got %d.\nText: %s", count, text)
	}

	// Should show with file path (loaded skill), not "from index"
	if strings.Contains(text, "from index") {
		t.Errorf("Should not show 'from index' for loaded skill, got: %s", text)
	}
}

func TestFindSkill_IndexMatchLoadedSkill_ShowsPath(t *testing.T) {
	dir := t.TempDir()
	// Index has "github" alias "gh cli", and the skill is loaded
	writeTestIndex(t, dir, []SkillIndexEntry{
		{
			Name:        "github",
			Description: "Interact with GitHub using the gh CLI",
			Aliases:     []string{"gh cli", "pull request"},
			Categories:  []string{"git"},
		},
	})

	tool := newTestToolWithIndex(testSkills(), nil, dir)

	// Search for "gh cli" — should find github via alias, show file path
	result, err := tool.Execute(context.Background(), map[string]any{
		"query": "gh cli",
	})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	text := firstText(result)
	if !strings.Contains(text, "github") {
		t.Errorf("Expected 'github' found via alias, got: %s", text)
	}
	if !strings.Contains(text, "SKILL.md") {
		t.Errorf("Expected file path for loaded skill, got: %s", text)
	}
}

func TestFindSkill_SortingExactNameFirst(t *testing.T) {
	dir := t.TempDir()
	writeTestIndex(t, dir, []SkillIndexEntry{
		{
			Name:       "github",
			Aliases:    []string{"pr review"},
			Categories: []string{"git"},
		},
	})

	skills := []skill.Skill{
		{
			Name:        "github",
			Description: "GitHub tool",
			FilePath:    "/a/github.md",
		},
		{
			Name:        "pr-review-helper",
			Description: "PR review helper",
			FilePath:    "/a/pr-review.md",
		},
	}
	tool := newTestToolWithIndex(skills, nil, dir)

	// "github" should be the first result (exact name match)
	result, err := tool.Execute(context.Background(), map[string]any{
		"query": "github",
	})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	text := firstText(result)
	lines := strings.Split(text, "\n")
	// First non-empty line should mention github
	firstLine := ""
	for _, l := range lines {
		if strings.HasPrefix(l, "- ") {
			firstLine = l
			break
		}
	}
	if !strings.Contains(firstLine, "github") {
		t.Errorf("Expected 'github' as first result (exact name match), first line: %s", firstLine)
	}
}

// TestFindSkill_LoadFallbackToDisk tests that executeLoad falls back to reading
// from disk when a skill exists on disk but wasn't pre-loaded (e.g., missing frontmatter).
func TestFindSkill_LoadFallbackToDisk(t *testing.T) {
	// Create a temp skills directory with a skill that has no frontmatter
	tmpSkills := t.TempDir()
	skillDir := filepath.Join(tmpSkills, "my-unloaded-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Write a SKILL.md without YAML frontmatter (simulates session-analyzer)
	skillContent := "# My Unloaded Skill\n\nThis skill has no frontmatter.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create tool with empty loaded skills and custom skills dir
	tool := NewFindSkillTool([]skill.Skill{}, nil)
	tool.SetIndexPath("")
	tool.SetSkillsDir(tmpSkills)

	result, err := tool.Execute(context.Background(), map[string]any{
		"load": true,
		"name": "my-unloaded-skill",
	})
	if err != nil {
		t.Fatalf("executeLoad should fall back to disk, got error: %v", err)
	}

	text := firstText(result)
	if !strings.Contains(text, "My Unloaded Skill") {
		t.Errorf("Expected loaded content to contain 'My Unloaded Skill', got: %s", text)
	}
	if !strings.Contains(text, "no frontmatter") {
		t.Errorf("Expected loaded content to contain 'no frontmatter', got: %s", text)
	}
}

// TestFindSkill_LoadFallbackToDiskReal tests the disk fallback using
// the real session-analyzer skill that exists on disk but lacks frontmatter.
func TestFindSkill_LoadFallbackToDiskReal(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("Cannot determine home directory")
	}

	skillPath := filepath.Join(home, ".ai", "skills", "session-analyzer", "SKILL.md")
	if _, err := os.Stat(skillPath); err != nil {
		t.Skipf("session-analyzer skill not found at %s", skillPath)
	}

	// Create tool with empty loaded skills
	tool := NewFindSkillTool([]skill.Skill{}, nil)
	tool.SetIndexPath("")

	result, err := tool.Execute(context.Background(), map[string]any{
		"load": true,
		"name": "session-analyzer",
	})
	if err != nil {
		t.Fatalf("executeLoad should fall back to disk for session-analyzer, got error: %v", err)
	}

	text := firstText(result)
	if !strings.Contains(text, "Session Analyzer") {
		t.Errorf("Expected loaded content to contain 'Session Analyzer', got: %s", text[:min(len(text), 200, len(text))])
	}
}
