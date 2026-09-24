# pkg/skill

Skill loading, validation, formatting, and usage statistics for the agent skills system.

## Overview

Skills are markdown files (with optional YAML frontmatter) that provide specialized instructions to the agent. They are loaded from multiple sources with defined precedence, and validated against naming and format rules.

## Skill

```go
type Skill struct {
    Name                   string      // Skill name (e.g., "react-component")
    Description            string      // Skill description
    FilePath               string      // Path to the skill markdown file
    BaseDir                string      // Directory containing the skill file
    Source                 string      // "user", "project", or "path"
    Content                string      // Full markdown content (body only)
    Frontmatter            Frontmatter // Parsed frontmatter
    DisableModelInvocation bool        // Exclude from auto-prompt
    Pinned                 bool        // Always listed in prompt (survives topN cutoff)
    LoadedAt               time.Time   // When the skill was loaded
}
```

## Frontmatter

```go
type Frontmatter struct {
    Name                   string                 `yaml:"name"`
    Description            string                 `yaml:"description"`
    License                string                 `yaml:"license,omitempty"`
    Compatibility          string                 `yaml:"compatibility,omitempty"`
    Metadata               map[string]interface{} `yaml:"metadata,omitempty"`
    AllowedTools           []string               `yaml:"allowed-tools,omitempty"`
    DisableModelInvocation bool                   `yaml:"disable-model-invocation,omitempty"`
    Pinned                 bool                   `yaml:"pinned,omitempty"`
}
```

Skills use YAML frontmatter at the top of their markdown files:

```markdown
---
name: react-component
description: Guidelines for creating React components
---

# React Component Guidelines
...
```

## Loading Sources

Skills are loaded from three sources in priority order:

1. **User skills** — `~/.ai/skills/` (global, personal)
2. **Project skills** — `.agents/skills/` (project-specific)
3. **Path skills** — Explicit file paths passed at load time

When skills with the same name exist in multiple sources, the first-loaded skill wins. Collisions are recorded as diagnostics.

Legacy `.ai/skills/` is no longer loaded; if it exists, a warning diagnostic is emitted pointing to `.agents/skills/`.

## Validation

The loader enforces:
- Name length ≤ 64 characters
- Description length ≤ 1024 characters
- Valid characters in name (lowercase a-z, 0-9, hyphens)
- Name must match parent directory name

## Collision Detection

```go
type CollisionInfo struct {
    ResourceType string // "skill"
    Name         string
    WinnerPath   string // First loaded skill
    LoserPath    string // Later skill with same name
}
```

Collisions are reported as `Diagnostic` entries with type `"collision"`. The winner is the first skill loaded (higher priority source).

## LoadResult

```go
type LoadResult struct {
    Skills      []Skill
    Diagnostics []Diagnostic
}
```

Returned by the loader with all successfully loaded skills and any warnings/errors.

## Skill Usage Statistics

```go
type SkillStatsFile struct { ... }

func LoadStats(path string) *SkillStatsFile
func (s *SkillStatsFile) RecordUsage(skillName string)
func (s *SkillStatsFile) RecordShown(names []string)
func (s *SkillStatsFile) LastShownOf(name string) time.Time
func (s *SkillStatsFile) DecayForNewSession()
func (s *SkillStatsFile) Save() error
func (s *SkillStatsFile) TopSkills(n int) []string
```

Tracks skill usage with session-based decay for progressive disclosure ranking.
"Time" advances in agent sessions, not wall-clock time: `DecayForNewSession`
is called once per session start and multiplies every score by
`0.5^(1/4)` (half-life = 4 agent sessions), while `RecordUsage` adds +1 on top
of the decayed score. Idle time never decays scores. `RecordShown` marks which
skills appeared in the prompt (LastShown), which drives the exploration
rotation; `Save` is a no-op when no file path is set, and otherwise merges
the on-disk copy monotonically (per-entry max, union of entries) before the
atomic write, so concurrent agent processes don't clobber each other's updates.

```go
func (s *SkillStatsFile) SortByScore(names []string) []string // Rank names by decayed usage; unknown names last, stable order
```

## Formatting and Expansion

```go
func FormatForPrompt(skills []Skill, stats *SkillStatsFile) string
func ExpandCommand(text string, skills []Skill) string
func IsSkillCommand(text string) bool
func ExtractSkillName(text string) string
```

`FormatForPrompt` renders skills as a prompt section. The top-N slots are split
between exploitation (highest session-decayed score) and exploration (2
reserved slots filled by the least recently shown skills, never-shown first),
so low-frequency skills still rotate into the prompt. Skills with frontmatter
`pinned: true` and project-local skills (source `project` from
`.agents/skills/`, or explicitly added `path` skills) are always listed
regardless of ranking or the topN cutoff. When skills are omitted, the footer
lists the omitted skill names (ranked by usage when stats are available) as
`find_skill` search hints. `ExpandCommand` handles `/skill:name` invocations.

## Key Files

| File | Description |
|------|-------------|
| `skill.go` | `Skill`, `Frontmatter`, `LoadResult`, `Diagnostic`, `CollisionInfo`, constants |
| `loader.go` | `Loader`, `LoadOptions`, skill loading from directories and paths |
| `parser.go` | `parseFrontmatter`, `validateName`, `validateDescription` |
| `formatter.go` | `FormatForPrompt` — skill list formatting for system prompt |
| `expander.go` | `ExpandCommand`, `IsSkillCommand`, `ExtractSkillName` |
| `stats.go` | `SkillStatsFile`, `SkillUsageEntry`, usage tracking with decay |