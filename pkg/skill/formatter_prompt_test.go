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
	if strings.Count(out, "<skill>") != maxPromptSkills {
		t.Fatalf("expected %d skills in prompt, got %d", maxPromptSkills, strings.Count(out, "<skill>"))
	}
	if !strings.Contains(out, "Note: 6 additional skills omitted for brevity.") {
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

	start := strings.Index(out, "<description>")
	end := strings.Index(out, "</description>")
	if start == -1 || end == -1 || end <= start {
		t.Fatalf("description tag not found in output: %s", out)
	}
	desc := out[start+len("<description>") : end]
	if len([]rune(desc)) != maxSkillDescriptionRunes {
		t.Fatalf("expected description length=%d, got %d", maxSkillDescriptionRunes, len([]rune(desc)))
	}
}
