# pkg/skill

Skill loading, validation, and collision detection for the agent skills system.

## Overview

Skills are markdown files (with optional YAML frontmatter) that provide specialized instructions to the agent. They are loaded from multiple sources with defined precedence, and validated against naming and format rules.

## Skill

```go
type Skill struct {
    Name                   string
    Description            string
    FilePath               string      // Path to the skill markdown file
    BaseDir                string      // Directory containing the skill file
    Source                 string      // "user", "project", or "path"
    Content                string      // Full markdown content
    Frontmatter            Frontmatter
    DisableModelInvocation bool        // Exclude from auto-prompt
    LoadedAt               time.Time
}
```

## Frontmatter

```go
type Frontmatter struct {
    Name        string `yaml:"name"`
    Description string `yaml:"description"`
    License     string `yaml:"license,omitempty"`
    // ... additional fields
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
2. **Project skills** — `.ai/skills/` (project-specific)
3. **Path skills** — Explicit file paths passed at load time

When skills with the same name exist in multiple sources, the first-loaded skill wins. Collisions are recorded as diagnostics.

## Validation

The loader enforces:
- Name length ≤ 64 characters
- Description length ≤ 1024 characters
- Valid characters in name
- Required frontmatter fields

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

## Key Files

| File | Description |
|------|-------------|
| `skill.go` | `Skill`, `Frontmatter`, `LoadResult`, `Diagnostic`, `CollisionInfo` |