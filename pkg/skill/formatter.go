package skill

import (
	"fmt"
	"strings"
)

const (
	maxPromptSkills          = 24
	maxSkillDescriptionRunes = 220
)

// FormatForPrompt formats skills for inclusion in a system prompt.
// Uses XML format per Agent Skills standard.
// See: https://agentskills.io/integrate-skills
//
// Skills with DisableModelInvocation=true are excluded from the prompt
// (they can only be invoked explicitly via /skill:name commands).
func FormatForPrompt(skills []Skill) string {
	// Filter out skills that shouldn't be auto-included
	visibleSkills := []Skill{}
	for _, skill := range skills {
		if !skill.DisableModelInvocation {
			visibleSkills = append(visibleSkills, skill)
		}
	}

	if len(visibleSkills) == 0 {
		return ""
	}

	omitted := 0
	if len(visibleSkills) > maxPromptSkills {
		omitted = len(visibleSkills) - maxPromptSkills
		visibleSkills = visibleSkills[:maxPromptSkills]
	}

	lines := []string{
		"## Skills",
		"The following skills provide specialized instructions for additional capabilities.",
		"",
		"**Skill Locations:**",
		"  - Global skills: ~/.ai/skills/",
		"  - Project skills: .ai/skills/",
		"  - Use bash tool to list skills: `ls ~/.ai/skills/*/SKILL.md` or `ls .ai/skills/*/SKILL.md`",
		"",
		"Skills may provide:",
		"  - New tools or utilities",
		"  - Domain-specific knowledge",
		"  - Workflow patterns",
		"",
		"Use the read tool to load a skill's file when the task matches its description.",
		"Skill files are located at: ~/.ai/skills/<skill-name>/SKILL.md or .ai/skills/<skill-name>/SKILL.md",
		"",
	}

	for _, skill := range visibleSkills {
		description := trimRunes(strings.TrimSpace(skill.Description), maxSkillDescriptionRunes)
		lines = append(lines, fmt.Sprintf("- **%s**: %s", skill.Name, description))
	}

	if omitted > 0 {
		lines = append(lines, fmt.Sprintf("*Note: %d additional skills omitted for brevity.*", omitted))
	}

	return strings.Join(lines, "\n")
}

// escapeXML escapes special XML characters.
func escapeXML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	return s
}

func trimRunes(s string, limit int) string {
	if limit <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit])
}
