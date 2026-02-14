package skill

import (
	"fmt"
	"strings"
)

// ExpandCommand expands /skill:name commands in the input text.
//
// Command format:
//
//	/skill:skill-name                    # Basic invocation
//	/skill:skill-name arguments         # With arguments
//
// The skill content is wrapped in XML format similar to the Agent Skills spec:
//
//	<skill name="skill-name" location="/path/to/skill.md">
//	References are relative to /path/to/skill/dir.
//
//	[skill content]
//	</skill>
//
// If arguments are provided, they are appended after the skill block.
// If the skill is not found, the original text is returned unchanged.
func ExpandCommand(text string, skills []Skill) string {
	if !strings.HasPrefix(text, "/skill:") {
		return text
	}

	// Extract skill name and arguments
	remaining := text[7:] // Remove "/skill:" prefix
	spaceIndex := strings.IndexAny(remaining, " \t\n")

	var skillName string
	var args string

	if spaceIndex == -1 {
		skillName = remaining
		args = ""
	} else {
		skillName = remaining[:spaceIndex]
		args = strings.TrimSpace(remaining[spaceIndex:])
	}

	if skillName == "" {
		return text
	}

	// Find the skill
	var foundSkill *Skill
	for i := range skills {
		if skills[i].Name == skillName {
			foundSkill = &skills[i]
			break
		}
	}

	if foundSkill == nil {
		// Skill not found, return original text
		return text
	}

	// Read skill content (strip frontmatter)
	bodyContent := foundSkill.Content

	// Build skill block in XML format
	skillBlock := fmt.Sprintf(`<skill name="%s" location="%s">
References are relative to %s.

%s
</skill>`,
		escapeXML(foundSkill.Name),
		escapeXML(foundSkill.FilePath),
		escapeXML(foundSkill.BaseDir),
		bodyContent,
	)

	// Append arguments if provided
	if args != "" {
		return skillBlock + "\n\n" + args
	}
	return skillBlock
}

// IsSkillCommand checks if the given text starts with a /skill: command.
func IsSkillCommand(text string) bool {
	return strings.HasPrefix(strings.TrimSpace(text), "/skill:")
}

// ExtractSkillName extracts the skill name from a /skill: command.
// Returns empty string if not a valid skill command.
func ExtractSkillName(text string) string {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "/skill:") {
		return ""
	}

	remaining := strings.TrimPrefix(trimmed, "/skill:")
	spaceIndex := strings.IndexAny(remaining, " \t\n")

	if spaceIndex == -1 {
		return remaining
	}
	return remaining[:spaceIndex]
}
