package skill

import (
	"fmt"
	"strings"
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

	lines := []string{
		"## Skills",
		"The following skills provide specialized instructions for additional capabilities.",
		"Skills may provide:",
		"  - New tools or utilities",
		"  - Domain-specific knowledge",
		"  - Workflow patterns",
		"",
		"Use the read tool to load a skill's file when the task matches its description.",
		"When a skill file references a relative path, resolve it against the skill directory (parent of SKILL.md / dirname of the path) and use that absolute path in tool commands.",
		"",
		"<available_skills>",
	}

	for _, skill := range visibleSkills {
		lines = append(lines, "  <skill>")
		lines = append(lines, fmt.Sprintf("    <name>%s</name>", escapeXML(skill.Name)))
		lines = append(lines, fmt.Sprintf("    <description>%s</description>", escapeXML(skill.Description)))
		lines = append(lines, fmt.Sprintf("    <location>%s</location>", escapeXML(skill.FilePath)))
		lines = append(lines, "  </skill>")
	}

	lines = append(lines, "</available_skills>")

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
