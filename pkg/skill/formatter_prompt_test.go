package skill

import (
	"fmt"
	"strings"
	"testing"
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

	out := FormatForPrompt(skills)
	// Count markdown list items (- **skill-N**:)
	skillCount := strings.Count(out, "- **skill-")
	if skillCount != maxPromptSkills {
		t.Fatalf("expected %d skills in prompt, got %d", maxPromptSkills, skillCount)
	}
	if !strings.Contains(out, "*Note: 6 additional skills omitted for brevity.*") {
		t.Fatalf("expected omitted-skills note, got output: %s", out)
	}
}

func TestFormatForPromptTruncatesDescription(t *testing.T) {
	longDesc := strings.Repeat("a", maxSkillDescriptionRunes+50)
	out := FormatForPrompt([]Skill{{
		Name:        "demo",
		Description: longDesc,
		FilePath:    "/tmp/demo/SKILL.md",
	}})

	// Check markdown format: - **demo**: <description>
	expectedPrefix := "- **demo**:"
	if !strings.Contains(out, expectedPrefix) {
		t.Fatalf("expected markdown list item in output: %s", out)
	}

	// Extract description from markdown line: - **demo**: <description>
	start := strings.Index(out, expectedPrefix) + len(expectedPrefix)
	// Find the end of line (since we removed the path, description ends at newline)
	end := strings.Index(out[start:], "\n")
	if end == -1 {
		end = len(out) - start
	}
	desc := strings.TrimSpace(out[start : start+end])
	if len([]rune(desc)) != maxSkillDescriptionRunes {
		t.Fatalf("expected description length=%d, got %d", maxSkillDescriptionRunes, len([]rune(desc)))
	}
}
