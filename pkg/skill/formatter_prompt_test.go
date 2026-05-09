package skill

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestFormatForPromptLimitsSkillCount(t *testing.T) {
	skills := make([]Skill, 0, 30)
	for i := 0; i < 30; i++ {
		skills = append(skills, Skill{
			Name:        fmt.Sprintf("skill-%d", i),
			Description: "desc",
			FilePath:    "/tmp/skill",
		})
	}

	out := FormatForPrompt(skills, nil)
	// Count markdown list items (- **skill-N**:)
	skillCount := strings.Count(out, "- **skill-")
	// With nil stats, default topN is 7
	if skillCount != 7 {
		t.Fatalf("expected 7 skills in prompt (nil stats, default cap), got %d", skillCount)
	}
	// 30 - 7 = 23 omitted
	if !strings.Contains(out, "*Note: 23 additional skills omitted for brevity.*") {
		t.Fatalf("expected 23 omitted note, got output: %s", out)
	}
	// No footer when stats is nil
	if strings.Contains(out, "find_skill") {
		t.Fatalf("expected no find_skill footer when stats is nil, got: %s", out)
	}
}

func TestFormatForPromptTruncatesDescription(t *testing.T) {
	longDesc := strings.Repeat("a", maxSkillDescriptionRunes+50)
	out := FormatForPrompt([]Skill{{
		Name:        "demo",
		Description: longDesc,
		FilePath:    "/tmp/demo/SKILL.md",
	}}, nil)

	// Check markdown format: - **demo**: <description> (/path)
	expectedPrefix := "- **demo**:"
	if !strings.Contains(out, expectedPrefix) {
		t.Fatalf("expected markdown list item in output: %s", out)
	}

	// Extract description from markdown line: - **demo**: <description> (/path)
	start := strings.Index(out, expectedPrefix) + len(expectedPrefix)
	end := strings.Index(out, " (/tmp/demo/SKILL.md)")
	if start == -1 || end == -1 || end <= start {
		t.Fatalf("description not found in output: %s", out)
	}
	desc := strings.TrimSpace(out[start:end])
	if len([]rune(desc)) != maxSkillDescriptionRunes {
		t.Fatalf("expected description length=%d, got %d", maxSkillDescriptionRunes, len([]rune(desc)))
	}
}

func TestFormatForPromptWithStatsRanked(t *testing.T) {
	// Create skills
	skills := []Skill{
		{Name: "alpha", Description: "Alpha skill", FilePath: "/tmp/alpha/SKILL.md"},
		{Name: "beta", Description: "Beta skill", FilePath: "/tmp/beta/SKILL.md"},
		{Name: "gamma", Description: "Gamma skill", FilePath: "/tmp/gamma/SKILL.md"},
		{Name: "delta", Description: "Delta skill", FilePath: "/tmp/delta/SKILL.md"},
	}

	// Stats rank gamma and alpha as top-2
	stats := &SkillStatsFile{
		Version: 1,
		TopN:    2,
		Entries: map[string]*SkillUsageEntry{
			"gamma": {Name: "gamma", Count: 10, LastUsed: time.Now(), Score: 10},
			"alpha": {Name: "alpha", Count: 5, LastUsed: time.Now(), Score: 5},
		},
	}

	out := FormatForPrompt(skills, stats)

	// Only gamma and alpha should appear
	if !strings.Contains(out, "- **gamma**") {
		t.Error("expected gamma in output")
	}
	if !strings.Contains(out, "- **alpha**") {
		t.Error("expected alpha in output")
	}
	if strings.Contains(out, "- **beta**") {
		t.Error("beta should not appear (not in top-2)")
	}
	if strings.Contains(out, "- **delta**") {
		t.Error("delta should not appear (not in top-2)")
	}

	// Footer should be present
	if !strings.Contains(out, "*Use the find_skill tool to discover more skills by keyword.*") {
		t.Error("expected find_skill footer when stats is non-nil")
	}
}

func TestFormatForPromptWithStatsStaleEntry(t *testing.T) {
	skills := []Skill{
		{Name: "alpha", Description: "Alpha skill", FilePath: "/tmp/alpha/SKILL.md"},
		{Name: "beta", Description: "Beta skill", FilePath: "/tmp/beta/SKILL.md"},
	}

	// Stats reference a deleted skill "deleted-skill" and "alpha"
	stats := &SkillStatsFile{
		Version: 1,
		TopN:    3,
		Entries: map[string]*SkillUsageEntry{
			"deleted-skill": {Name: "deleted-skill", Count: 20, LastUsed: time.Now(), Score: 20},
			"alpha":         {Name: "alpha", Count: 5, LastUsed: time.Now(), Score: 5},
		},
	}

	out := FormatForPrompt(skills, stats)

	// deleted-skill should be skipped (not in loaded list)
	if strings.Contains(out, "deleted-skill") {
		t.Error("deleted-skill should not appear in output")
	}
	// alpha should appear (it's ranked and exists)
	if !strings.Contains(out, "- **alpha**") {
		t.Error("expected alpha in output")
	}
	// beta should appear as supplement (topN=3, only 1 ranked found, 1 supplement needed)
	if !strings.Contains(out, "- **beta**") {
		t.Error("expected beta in output (supplemented to fill topN)")
	}
}

func TestFormatForPromptWithStatsAllStale(t *testing.T) {
	skills := []Skill{
		{Name: "alpha", Description: "Alpha skill", FilePath: "/tmp/alpha/SKILL.md"},
		{Name: "beta", Description: "Beta skill", FilePath: "/tmp/beta/SKILL.md"},
	}

	// All stats reference skills not in loaded list
	stats := &SkillStatsFile{
		Version: 1,
		TopN:    7,
		Entries: map[string]*SkillUsageEntry{
			"old-1": {Name: "old-1", Count: 10, LastUsed: time.Now(), Score: 10},
			"old-2": {Name: "old-2", Count: 5, LastUsed: time.Now(), Score: 5},
		},
	}

	out := FormatForPrompt(skills, stats)

	// Should fall back to showing all loaded skills
	if !strings.Contains(out, "- **alpha**") {
		t.Error("expected alpha in output (all-stale fallback)")
	}
	if !strings.Contains(out, "- **beta**") {
		t.Error("expected beta in output (all-stale fallback)")
	}
	// Footer should still be present (stats is non-nil)
	if !strings.Contains(out, "*Use the find_skill tool to discover more skills by keyword.*") {
		t.Error("expected find_skill footer")
	}
}

func TestFormatForPromptColdStart(t *testing.T) {
	skills := make([]Skill, 0, 15)
	for i := 0; i < 15; i++ {
		skills = append(skills, Skill{
			Name:        fmt.Sprintf("skill-%d", i),
			Description: "desc",
			FilePath:    "/tmp/skill",
		})
	}

	// Stats with empty entries (cold start)
	stats := &SkillStatsFile{
		Version: 1,
		TopN:    7,
		Entries: map[string]*SkillUsageEntry{},
	}

	out := FormatForPrompt(skills, stats)

	// Should show all skills capped at TopN=7
	skillCount := strings.Count(out, "- **skill-")
	if skillCount != 7 {
		t.Fatalf("expected 7 skills in prompt (cold start, capped at TopN), got %d", skillCount)
	}
	// Footer should be present (stats is non-nil)
	if !strings.Contains(out, "*Use the find_skill tool to discover more skills by keyword.*") {
		t.Error("expected find_skill footer")
	}
}

func TestFormatForPromptSupplementFillsTopN(t *testing.T) {
	skills := []Skill{
		{Name: "ranked", Description: "Ranked skill", FilePath: "/tmp/ranked/SKILL.md"},
		{Name: "unranked-a", Description: "Unranked A", FilePath: "/tmp/ua/SKILL.md"},
		{Name: "unranked-b", Description: "Unranked B", FilePath: "/tmp/ub/SKILL.md"},
	}

	// Only 1 skill in stats, TopN=3
	stats := &SkillStatsFile{
		Version: 1,
		TopN:    3,
		Entries: map[string]*SkillUsageEntry{
			"ranked": {Name: "ranked", Count: 5, LastUsed: time.Now(), Score: 5},
		},
	}

	out := FormatForPrompt(skills, stats)

	// All 3 should appear: 1 ranked + 2 supplemented
	if strings.Count(out, "- **") != 3 {
		t.Fatalf("expected 3 skills (1 ranked + 2 supplemented), got %d", strings.Count(out, "- **"))
	}
	if !strings.Contains(out, "- **ranked**") {
		t.Error("expected ranked skill")
	}
	if !strings.Contains(out, "- **unranked-a**") {
		t.Error("expected unranked-a (supplement)")
	}
	if !strings.Contains(out, "- **unranked-b**") {
		t.Error("expected unranked-b (supplement)")
	}
}
