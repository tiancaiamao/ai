package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"github.com/tiancaiamao/ai/pkg/skill"
)

// SkillIndexEntry represents one skill entry in the generated search index.
type SkillIndexEntry struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Aliases     []string `json:"aliases"`
	UseWhen     []string `json:"use_when"`
	Categories  []string `json:"categories"`
}

// SkillIndex represents the structure of ~/.ai/skill-index.json.
type SkillIndex struct {
	Version      int               `json:"version"`
	GeneratedAt string            `json:"generated_at"`
	EntryCount  int               `json:"entry_count"`
	Entries      []SkillIndexEntry `json:"entries"`
}

// FindSkillTool lets the LLM discover skills by keyword search
// and optionally load full skill content.
type FindSkillTool struct {
	skills    []skill.Skill
	stats     *skill.SkillStatsFile
	indexPath string // path to skill-index.json; defaults to ~/.ai/skill-index.json
}

// NewFindSkillTool creates a new FindSkillTool with the given skills and stats.
func NewFindSkillTool(skills []skill.Skill, stats *skill.SkillStatsFile) *FindSkillTool {
	home, _ := os.UserHomeDir()
	indexPath := ""
	if home != "" {
		indexPath = filepath.Join(home, ".ai", "skill-index.json")
	}
		return &FindSkillTool{
		skills:    skills,
		stats:     stats,
		indexPath: indexPath,
	}
}

// SetIndexPath overrides the default skill-index.json path.
// Useful for testing to avoid loading the real index.
func (t *FindSkillTool) SetIndexPath(path string) {
	t.indexPath = path
}

// Name returns the tool name.
func (t *FindSkillTool) Name() string {
	return "find_skill"
}

// Description returns the tool description.
func (t *FindSkillTool) Description() string {
	return "Search and load agent skills by keyword. Use this to discover available skills before loading them. Returns matching skill names and descriptions; use load=true to read full skill content."
}

// Parameters returns the JSON Schema for the tool parameters.
func (t *FindSkillTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "Keyword to search — matches against skill name, description, aliases, use-when triggers, and categories (case-insensitive substring)",
			},
			"load": map[string]any{
				"type":        "boolean",
				"description": "If true, returns full skill content instead of listing. Requires name parameter.",
			},
			"name": map[string]any{
				"type":        "string",
				"description": "Exact skill name to load (used with load=true for direct loading)",
			},
		},
				"required": []string{},
	}
}

// Execute runs the find_skill tool.
func (t *FindSkillTool) Execute(ctx context.Context, args map[string]any) ([]agentctx.ContentBlock, error) {
	query, _ := args["query"].(string)
	load, _ := args["load"].(bool)

	if load {
		// In load mode, use the name parameter if provided, otherwise use query
		name := query
		if n, ok := args["name"].(string); ok && n != "" {
			name = n
		}
		return t.executeLoad(name)
	}

	// Search mode: require query
	if query == "" {
		return nil, fmt.Errorf("query parameter is required")
	}

	return t.executeSearch(query)
}

// searchResult holds a matched skill with a relevance rank for sorting.
// Lower rank = more relevant.
type searchResult struct {
	skill skill.Skill
	rank  int
}

const (
	rankExactName  = 0
	rankAliasMatch = 1
	rankCategory   = 2
	rankUseWhen    = 3
	rankDescMatch  = 4
	rankNameMatch  = 5
)

// executeSearch searches all loaded skills for a case-insensitive substring match
// on Name and Description, then enriches results from the skill index.
// Returns up to 5 results sorted by relevance.
func (t *FindSkillTool) executeSearch(query string) ([]agentctx.ContentBlock, error) {
	lowerQuery := strings.ToLower(query)

	// Phase 1: direct name/description matching on loaded skills
	directMatches := make(map[string]bool) // names already found
	var results []searchResult

	for _, s := range t.skills {
		lowerName := strings.ToLower(s.Name)
		lowerDesc := strings.ToLower(s.Description)

		if lowerName == lowerQuery {
			results = append(results, searchResult{skill: s, rank: rankExactName})
			directMatches[s.Name] = true
		} else if strings.Contains(lowerName, lowerQuery) {
			results = append(results, searchResult{skill: s, rank: rankNameMatch})
			directMatches[s.Name] = true
		} else if strings.Contains(lowerDesc, lowerQuery) {
			results = append(results, searchResult{skill: s, rank: rankDescMatch})
			directMatches[s.Name] = true
		}
	}

	// Phase 2: enrich from skill index
	idx := loadSkillIndex(t.indexPath)
	if idx != nil {
		for _, entry := range idx.Entries {
			if directMatches[entry.Name] {
				continue // already found via direct match
			}

			matchedRank := -1

			// Check aliases
			for _, alias := range entry.Aliases {
				if strings.Contains(strings.ToLower(alias), lowerQuery) {
					matchedRank = rankAliasMatch
					break
				}
			}

			// Check categories
			if matchedRank < 0 {
				for _, cat := range entry.Categories {
					if strings.Contains(strings.ToLower(cat), lowerQuery) {
						matchedRank = rankCategory
						break
					}
				}
			}

			// Check use_when
			if matchedRank < 0 {
				for _, uw := range entry.UseWhen {
					if strings.Contains(strings.ToLower(uw), lowerQuery) {
						matchedRank = rankUseWhen
						break
					}
				}
			}

			if matchedRank >= 0 {
				// Find the loaded skill for this index entry
				if s, ok := t.findLoadedSkill(entry.Name); ok {
					results = append(results, searchResult{skill: s, rank: matchedRank})
				} else {
					// Index references a skill not currently loaded — include with index info
					results = append(results, searchResult{
						skill: skill.Skill{
							Name:        entry.Name,
							Description: entry.Description,
						},
						rank: matchedRank,
					})
				}
				directMatches[entry.Name] = true
			}
		}
	}

	if len(results) == 0 {
		return []agentctx.ContentBlock{
			agentctx.TextContent{
				Type: "text",
				Text: fmt.Sprintf("No skills found matching '%s'. Try a different keyword.", query),
			},
		}, nil
	}

	// Sort by rank (relevance)
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].rank < results[j].rank
	})

	// Limit to 5 results
	if len(results) > 5 {
		results = results[:5]
	}

	var b strings.Builder
	for _, r := range results {
		m := r.skill
		desc := m.Description
		if utf8.RuneCountInString(desc) > 150 {
			desc = string([]rune(desc)[:150]) + "..."
		}
		if m.FilePath != "" {
			fmt.Fprintf(&b, "- %s: %s\n  Path: %s\n", m.Name, desc, m.FilePath)
		} else {
			fmt.Fprintf(&b, "- %s: %s\n  (from index — skill may not be currently loaded)\n", m.Name, desc)
		}
	}
	b.WriteString("Use find_skill with name=<skill_name> and load=true to read the full skill.")

	// Record usage for matched skills that are loaded
	for _, r := range results {
		if r.skill.FilePath != "" {
			t.recordUsage(r.skill.Name)
		}
	}

	return []agentctx.ContentBlock{
		agentctx.TextContent{
			Type: "text",
			Text: b.String(),
		},
	}, nil
}

// findLoadedSkill looks up a skill by name in the loaded skills list.
func (t *FindSkillTool) findLoadedSkill(name string) (skill.Skill, bool) {
	for _, s := range t.skills {
		if s.Name == name {
			return s, true
		}
	}
	return skill.Skill{}, false
}

// executeLoad returns the full content of a skill by exact name match.
func (t *FindSkillTool) executeLoad(name string) ([]agentctx.ContentBlock, error) {
	for _, s := range t.skills {
		if s.Name == name {
			t.recordUsage(name)
			return []agentctx.ContentBlock{
				agentctx.TextContent{
					Type: "text",
					Text: s.Content,
				},
			}, nil
		}
	}

	return nil, fmt.Errorf("skill '%s' not found", name)
}

// recordUsage records skill usage in stats if stats is available.
// Errors are silently ignored (fire-and-forget).
func (t *FindSkillTool) recordUsage(skillName string) {
	if t.stats == nil {
		return
	}
	t.stats.RecordUsage(skillName)
	_ = t.stats.Save()
}

// loadSkillIndex reads and parses the skill index file at the given path.
// Returns nil if the file doesn't exist or is malformed (logs a warning).
func loadSkillIndex(path string) *SkillIndex {
	if path == "" {
		return nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("failed to read skill index", "path", path, "error", err)
		}
		return nil
	}

	var idx SkillIndex
	if err := json.Unmarshal(data, &idx); err != nil {
		slog.Warn("failed to parse skill index", "path", path, "error", err)
		return nil
	}

	return &idx
}