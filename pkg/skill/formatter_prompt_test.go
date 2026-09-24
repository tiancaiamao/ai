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
	// With nil stats, default topN is 10
	if skillCount != 10 {
		t.Fatalf("expected 10 skills in prompt (nil stats, default cap), got %d", skillCount)
	}
	// 30 - 10 = 20 omitted; hint names are derived from the first omitted skills
	if !strings.Contains(out, "Try names: skill-10, skill-11, skill-12, skill-13, skill-14, skill-15, skill-16, skill-17, skill-18, skill-19") {
		t.Fatalf("expected derived omitted-names hint capped at 10, got output: %s", out)
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

	// Extract description from markdown line: - **demo**: <description>
	start := strings.Index(out, expectedPrefix) + len(expectedPrefix)
	relEnd := strings.Index(out[start:], "\n")
	if relEnd == -1 {
		t.Fatalf("description not found in output: %s", out)
	}
	end := start + relEnd
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

	// Footer should list the omitted skill names (ranked by usage: beta > delta)
	if !strings.Contains(out, "Try names: beta, delta") {
		t.Errorf("expected omitted-names hint 'beta, delta', got output: %s", out)
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

	// Should fall back to showing all loaded skills; both are shown, so
	// nothing is omitted and no names hint is expected.
	if !strings.Contains(out, "- **alpha**") {
		t.Error("expected alpha in output (all-stale fallback)")
	}
	if !strings.Contains(out, "- **beta**") {
		t.Error("expected beta in output (all-stale fallback)")
	}
	if strings.Contains(out, "Try names:") {
		t.Errorf("expected no names hint when nothing is omitted, got: %s", out)
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
		TopN:    10,
		Entries: map[string]*SkillUsageEntry{},
	}

	out := FormatForPrompt(skills, stats)

	// Should show all skills capped at TopN=10
	skillCount := strings.Count(out, "- **skill-")
	if skillCount != 10 {
		t.Fatalf("expected 10 skills in prompt (cold start, capped at TopN), got %d", skillCount)
	}
	// Hint should list the omitted skill names (no stats to rank by, input order)
	if !strings.Contains(out, "Try names: skill-10, skill-11, skill-12, skill-13, skill-14") {
		t.Errorf("expected omitted-names hint for cold start, got: %s", out)
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

func TestFormatForPromptPinnedAlwaysListed(t *testing.T) {
	skills := []Skill{
		{Name: "alpha", Description: "Alpha", FilePath: "/tmp/a/SKILL.md"},
		{Name: "beta", Description: "Beta", FilePath: "/tmp/b/SKILL.md"},
		{Name: "gamma", Description: "Gamma", FilePath: "/tmp/g/SKILL.md"},
		{Name: "session-history", Description: "History", FilePath: "/tmp/h/SKILL.md", Pinned: true},
	}

	// Stats rank only alpha; topN=1. Without pinning, session-history would
	// be invisible — the exact death spiral this fixes.
	stats := &SkillStatsFile{
		Version: 1,
		TopN:    1,
		Entries: map[string]*SkillUsageEntry{
			"alpha": {Name: "alpha", Count: 50, LastUsed: time.Now(), Score: 50},
		},
	}

	out := FormatForPrompt(skills, stats)

	if !strings.Contains(out, "- **session-history**") {
		t.Errorf("pinned skill should always be listed, got: %s", out)
	}
	if strings.Contains(out, "- **beta**") || strings.Contains(out, "- **gamma**") {
		t.Errorf("non-pinned unranked skills should be cut by topN, got: %s", out)
	}
}

func TestFormatForPromptPinnedNotDuplicated(t *testing.T) {
	skills := []Skill{
		{Name: "alpha", Description: "Alpha", FilePath: "/tmp/a/SKILL.md", Pinned: true},
		{Name: "beta", Description: "Beta", FilePath: "/tmp/b/SKILL.md"},
	}

	// alpha is both ranked and pinned — must appear exactly once.
	stats := &SkillStatsFile{
		Version: 1,
		TopN:    2,
		Entries: map[string]*SkillUsageEntry{
			"alpha": {Name: "alpha", Count: 5, LastUsed: time.Now(), Score: 5},
		},
	}

	out := FormatForPrompt(skills, stats)
	if n := strings.Count(out, "- **alpha**"); n != 1 {
		t.Errorf("pinned+ranked skill should appear exactly once, got %d: %s", n, out)
	}
}

func TestFormatForPromptPinnedColdStart(t *testing.T) {
	skills := make([]Skill, 0, 13)
	for i := 0; i < 12; i++ {
		skills = append(skills, Skill{Name: fmt.Sprintf("skill-%d", i), Description: "d", FilePath: "/tmp/s"})
	}
	// The 13th skill (index 12) would be cut by topN=10, but it is pinned.
	skills = append(skills, Skill{Name: "pinned-late", Description: "d", FilePath: "/tmp/p", Pinned: true})

	out := FormatForPrompt(skills, nil)

	if !strings.Contains(out, "- **pinned-late**") {
		t.Errorf("pinned skill should survive the cold-start cutoff, got: %s", out)
	}
	if strings.Contains(out, "- **skill-11**") {
		t.Errorf("unpinned skills beyond topN should still be cut, got: %s", out)
	}
}
