package skill

import (
	"fmt"
	"sort"
	"strings"

	"github.com/tiancaiamao/ai/pkg/truncate"
)

const (
	maxSkillDescriptionRunes = 220
	maxKeywordNames          = 10
	// explorationSlots reserves the tail of the top-N prompt slots for the
	// least recently shown skills, so low-frequency skills still rotate into
	// the prompt instead of starving behind high-frequency ones.
	explorationSlots = 2
)

// FormatForPrompt formats skills for inclusion in a system prompt.
// Uses XML format per Agent Skills standard.
// See: https://agentskills.io/integrate-skills
//
// Skills with DisableModelInvocation=true are excluded from the prompt
// (they can only be invoked explicitly via /skill:name commands).
//
// If stats is non-nil and has entries, the top-N prompt slots are split
// between exploitation (highest session-decayed score) and exploration
// (least recently shown, never-shown first). Pinned skills (frontmatter
// `pinned: true`) and project-local skills (source "project" from
// .agents/skills, or explicit "path") are always listed regardless of
// ranking or the topN cutoff. Otherwise (cold start / nil stats), all
// visible skills are shown capped at DefaultTopN.
//
// The selected skills are recorded via stats.RecordShown so the exploration
// rotation has a "least recently shown" signal; the prompt builder persists
// it.
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
		// Build lookup by name
		skillByName := make(map[string]*Skill, len(visibleSkills))
		for i := range visibleSkills {
			skillByName[visibleSkills[i].Name] = &visibleSkills[i]
		}

		// Exploitation: highest-ranked (session-decayed) skills first.
		exploitN := topN - explorationSlots
		if exploitN < 1 {
			exploitN = 1
		}
		for _, name := range stats.TopSkills(topN) {
			if len(selected) >= exploitN {
				break
			}
			if s, ok := skillByName[name]; ok {
				selected = append(selected, *s)
				delete(skillByName, name)
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
		} else if remaining := topN - len(selected); remaining > 0 && len(skillByName) > 0 {
			// Exploration: fill the remaining slots with the least recently
			// shown skills (never shown first, name order as tie-break).
			unselected := make([]Skill, 0, len(skillByName))
			for _, s := range skillByName {
				unselected = append(unselected, *s)
			}
			sort.Slice(unselected, func(i, j int) bool {
				li := stats.LastShownOf(unselected[i].Name)
				lj := stats.LastShownOf(unselected[j].Name)
				if !li.Equal(lj) {
					return li.Before(lj)
				}
				return unselected[i].Name < unselected[j].Name
			})
			if remaining > len(unselected) {
				remaining = len(unselected)
			}
			for _, s := range unselected[:remaining] {
				selected = append(selected, s)
				delete(skillByName, s.Name)
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

	// Pinned skills and project-local skills are always listed, even when
	// cut by the topN ranking.
	selected = ensureAlwaysListed(selected, visibleSkills)

	// Record which skills were shown so the exploration rotation can pick
	// the least recently shown next time.
	if stats != nil && len(selected) > 0 {
		shownNames := make([]string, len(selected))
		for i, s := range selected {
			shownNames[i] = s.Name
		}
		stats.RecordShown(shownNames)
	}

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

// ensureAlwaysListed appends pinned skills and project-local skills not
// already in selected. Project skills (source "project" from .agents/skills
// or explicitly added "path" skills) are always listed: they were
// deliberately placed for this project/agent and should not compete in the
// usage ranking. Survives the topN cutoff in both the ranked and
// cold-start paths.
func ensureAlwaysListed(selected, visible []Skill) []Skill {
	seen := make(map[string]bool, len(selected))
	for _, s := range selected {
		seen[s.Name] = true
	}
	for _, s := range visible {
		if (s.Pinned || s.Source == "project" || s.Source == "path") && !seen[s.Name] {
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
