package skill

import (
	"fmt"
	"strings"

	"github.com/tiancaiamao/ai/pkg/truncate"
)

const (
	maxSkillDescriptionRunes = 220
	maxKeywordNames          = 10
)

// FormatForPrompt formats skills for inclusion in a system prompt.
// Uses XML format per Agent Skills standard.
// See: https://agentskills.io/integrate-skills
//
// Skills with DisableModelInvocation=true are excluded from the prompt
// (they can only be invoked explicitly via /skill:name commands).
//
// If stats is non-nil and has entries, only the top-N ranked skills from
// stats are shown. Otherwise (cold start / nil stats), all visible skills
// are shown capped at DefaultTopN. Pinned skills (frontmatter
// `pinned: true`) are always listed regardless of ranking or the topN cutoff.
func FormatForPrompt(skills []Skill, stats *SkillStatsFile) string {
	// Filter out skills that shouldn't be auto-included
	visibleSkills := make([]Skill, 0, len(skills))
	for _, skill := range skills {
		if !skill.DisableModelInvocation {
			visibleSkills = append(visibleSkills, skill)
		}
	}

	if len(visibleSkills) == 0 {
		return ""
	}

	topN := DefaultTopN
	if stats != nil && stats.TopN > 0 {
		topN = stats.TopN
	}

	// Determine which skills to show
	var selected []Skill

	if stats != nil && len(stats.Entries) > 0 {
		// Stats-based ranking: use TopSkills to determine order
		topNames := stats.TopSkills(topN)
		nameSet := make(map[string]bool, len(topNames))
		for _, n := range topNames {
			nameSet[n] = true
		}

		// Build lookup by name
		skillByName := make(map[string]*Skill, len(visibleSkills))
		for i := range visibleSkills {
			skillByName[visibleSkills[i].Name] = &visibleSkills[i]
		}

		// Add ranked skills that exist in the loaded list (in rank order)
		for _, name := range topNames {
			if s, ok := skillByName[name]; ok {
				selected = append(selected, *s)
				delete(skillByName, name)
			}
		}

		// Supplement with unranked skills to fill up to topN
		for _, s := range visibleSkills {
			if len(selected) >= topN {
				break
			}
			if _, ok := skillByName[s.Name]; ok {
				selected = append(selected, s)
				delete(skillByName, s.Name)
			}
		}

		// Edge case: all stats were stale (none matched loaded skills)
		// → fall back to showing all loaded skills, capped at topN
		if len(selected) == 0 {
			if len(visibleSkills) > topN {
				selected = visibleSkills[:topN]
			} else {
				selected = visibleSkills
			}
		}
	} else {
		// Cold start / nil stats: show all visible skills capped at topN
		if len(visibleSkills) > topN {
			selected = visibleSkills[:topN]
		} else {
			selected = visibleSkills
		}
	}

	// Pinned skills are always listed, even when cut by the topN ranking.
	selected = ensurePinned(selected, visibleSkills)

	lines := []string{
		"## Skills",
		"Skills are specialized instructions. Before starting any non-trivial task, check available skills first. If any skill's description matches your task, read the FULL skill file BEFORE acting — not after, not when stuck.",
		"Reading a skill costs ~1-2 minutes. Ignoring it and going in the wrong direction costs 30+ minutes. This applies ESPECIALLY under time pressure.",
		"",
	}

	for _, skill := range selected {
		description := truncate.TrimRunes(strings.TrimSpace(skill.Description), maxSkillDescriptionRunes)
		lines = append(lines, fmt.Sprintf("- **%s**: %s", skill.Name, description))
	}

	lines = append(lines, "",
		"When a skill file references a relative path, resolve it against the skill directory (parent of SKILL.md / dirname of the path) and use that absolute path in tool commands.",
	)

	// Hint about omitted skills so the LLM knows what to search for with
	// find_skill. Derived from the actual omitted skill names (ranked by
	// usage when stats are available), not a static keyword list.
	selectedNames := make(map[string]bool, len(selected))
	for _, s := range selected {
		selectedNames[s.Name] = true
	}
	var omittedNames []string
	for _, s := range visibleSkills {
		if !selectedNames[s.Name] {
			omittedNames = append(omittedNames, s.Name)
		}
	}
	if len(omittedNames) > 0 {
		if stats != nil {
			omittedNames = stats.SortByScore(omittedNames)
		}
		if len(omittedNames) > maxKeywordNames {
			omittedNames = omittedNames[:maxKeywordNames]
		}
		lines = append(lines, "",
			fmt.Sprintf("*Additional skills are available via `find_skill`. Try names: %s.*", strings.Join(omittedNames, ", ")))
	}

	return strings.Join(lines, "\n")
}

// ensurePinned appends any pinned skills not already in selected, so pinned
// skills survive the topN cutoff in both the ranked and cold-start paths.
func ensurePinned(selected, visible []Skill) []Skill {
	seen := make(map[string]bool, len(selected))
	for _, s := range selected {
		seen[s.Name] = true
	}
	for _, s := range visible {
		if s.Pinned && !seen[s.Name] {
			selected = append(selected, s)
			seen[s.Name] = true
		}
	}
	return selected
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
