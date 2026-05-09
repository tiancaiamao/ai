# Design: Skill Unification & Progressive Disclosure

## Goals

- Unify skill directories: merge `~/.aiclaw/skills/` into `~/.ai/skills/`, single source of truth
- Progressive disclosure: only top-N high-frequency skills in system prompt, rest discoverable via `find_skill` tool
- Usage tracking with time decay to auto-rank skills by relevance
- LLM-generated search index for semantic skill discovery (aliases, use-when, categories)

## Non-Goals

- Changing the SKILL.md format or frontmatter schema
- Building a skill marketplace / remote registry (that's ClawHub's job)
- Changing how `/skill:name` expansion works (keep as-is)
- Real-time RAG with embedding vectors (overkill for ~56 skills)

---

## 1. 现状

### ai (coding agent)

- Skill loading: `cmd/ai/rpc_handlers.go` L224 — `skill.NewLoader(agentDir)` with `LoadOptions{CWD, AgentDir: "~/.ai"}`
- Loads from `~/.ai/skills/` (user) + `.ai/skills/` (project)
- 21 skills in `~/.ai/skills/`
- `FormatForPrompt()` in `pkg/skill/formatter.go` renders ALL skills into system prompt (up to 24, description capped at 220 runes)
- Current system prompt `pkg/prompt/prompt.md` has `%SKILLS%` placeholder that gets replaced
- Tool registry in `pkg/tools/registry.go`, interface `Tool` in `pkg/context/context.go` L60: `Name()`, `Description()`, `Parameters()`, `Execute()`
- `/skill:name` expansion in `pkg/skill/expander.go` — looks up skill by name, injects full content inline

### aiclaw (general agent)

- `~/.aiclaw/skills/` → symlink to `claw/skills/` (35 skills)
- `claw/cmd/aiclaw/main.go` L223 — same `skill.NewLoader(clawDir)`, reads `~/.aiclaw/skills/`
- `buildSkillsSummary()` in main.go — simpler format, just `- **name**: description` per skill
- Has `find-skills` skill (ClawHub marketplace search, not local discovery)

### Token cost of current approach

- 21 skill summaries in system prompt: ~4000 chars of name+description text
- Plus the `## Skills` header block (~300 chars)
- With 56 skills merged, that'd be ~12000 chars (~3000 tokens) just for skill listings

---

## 2. 为什么要改

**Token waste**: Most skills are irrelevant for any given task. Loading 56 skill descriptions into every prompt burns ~3000 tokens per turn. A coding session uses `implement`, `plan`, `ag` repeatedly but never touches `weather` or `discord`.

**Two divergent systems**: ai and aiclaw share `pkg/skill/` code but maintain separate directories. Adding a skill requires deciding which tree it belongs to, or duplicating it.

**No adaptivity**: Skills are listed alphabetically/by-load-order. A skill you use daily has the same prominence as one you've never touched.

---

## 3. 关键设计决策

### Decision 1: `find_skill` as a built-in tool vs meta-skill

| Option | Pros | Cons |
|--------|------|------|
| **Built-in tool** (chosen) | Zero extra reads; LLM sees it in tool schema; deterministic behavior | Go code change; tool schema in every prompt (small) |
| Meta-skill markdown | No code change | Agent must read the skill first; depends on LLM correctly running grep/read flow |

**Choice**: Built-in tool. Skill discovery is a core capability, not an optional behavior. Tool schema is ~200 tokens, much cheaper than 56 skill descriptions.

### Decision 2: Frequency storage format

| Option | Pros | Cons |
|--------|------|------|
| **Single JSON file** (chosen) | Simple; fast startup; one file read | Write contention if multiple agents run simultaneously |
| Per-skill dot files | No contention | 56+ file reads at startup; filesystem noise |
| Embedded in config | No extra file | Mixes concerns; config becomes mutable state |

**Choice**: Single JSON file `~/.ai/skill-stats.json`. Write contention is acceptable — two agents rarely update the exact same skill at the exact same millisecond, and a lost update just means one +1 is missed.

### Decision 3: How many skills in system prompt

Top **7** skills by decay score. This covers the typical "always needed" set (e.g., `ag`, `implement`, `plan`, `bash` usage patterns) while keeping the skill section under ~500 tokens. The number is configurable via the stats file if needed later.

### Decision 4: Skill search — keyword index vs substring match vs LLM-generated index

| Option | Pros | Cons |
|--------|------|------|
| Substring match on name+description only | Zero maintenance | Can't bridge synonyms, Chinese↔English, use-case intent |
| Manual keywords in frontmatter | Precise | Every new skill needs keyword curation, never complete |
| **LLM-generated index file** (chosen) | Zero manual maintenance; handles synonyms, i18n, intent; one-time LLM call per reindex | Requires LLM call to (re)generate; index can drift if skills change |

**Choice**: LLM-generated `~/.ai/skill-index.json`. A dedicated skill (`index-skills`) reads all SKILL.md files, calls the LLM once to produce a rich search index, and writes it to disk. `find_skill` searches this index — no LLM call at query time, millisecond response.

The index is regenerated when:
- Skill directory contents change (detected via file count + mtime hash)
- User triggers `/skills reindex`

### Decision 5: Who generates the index — built-in logic vs skill

| Option | Pros | Cons |
|--------|------|------|
| Built-in Go code in stats/indexer | Tight integration, no skill dependency | Hardcodes the LLM prompt, less flexible |
| **Dedicated skill** (`~/.ai/skills/index-skills/SKILL.md`) (chosen) | Prompt is just markdown, easy to iterate; skill can be improved without recompiling; follows the "skills extend capabilities" philosophy | One extra skill in the directory |

**Choice**: A skill. The index-generation prompt is itself a skill — easy to refine, version, and improve without touching Go code. The agent runs this skill (via `/skill:index-skills` or triggered by startup logic) to produce/update `skill-index.json`.

---

## 4. 怎么做

### 4.1 New file: `pkg/skill/stats.go` — Usage tracking

```go
package skill

// SkillStat tracks usage frequency for a single skill.
type SkillStat struct {
    Count    int       `json:"count"`
    LastUsed time.Time `json:"last_used"`
}

// SkillStatsFile is the on-disk format.
// Path: ~/.ai/skill-stats.json
type SkillStatsFile struct {
    // TopN: how many skills to show in system prompt (default 7)
    TopN int                  `json:"top_n,omitempty"`
    Stats map[string]SkillStat `json:"stats"`
}

// LoadStats reads from ~/.ai/skill-stats.json.
// Returns empty stats with defaults if file doesn't exist.
func LoadStats(path string) (*SkillStatsFile, error)

// RecordUsage increments count and updates last_used for a skill.
// Writes to disk immediately.
func (s *SkillStatsFile) RecordUsage(skillName string) error

// TopSkills returns the top N skill names by decay score.
// Score = count * exp(-0.1 * days_since_last_use)
// 7-day half-life: skills not used in ~7 days drop significantly.
func (s *SkillStatsFile) TopSkills(n int) []string
```

**Decay formula**: `score = count * e^(-0.1 * days_since_last_use)`
- Used today (0 days): score = count × 1.0
- Used 7 days ago: score = count × 0.5
- Used 30 days ago: score = count × 0.05
- A skill used 10 times 30 days ago (score 0.5) loses to a skill used once today (score 1.0)

### 4.2 New file: `~/.ai/skill-index.json` — LLM-generated search index

Generated by the `index-skills` skill. Consumed by `find_skill` tool at query time.

```json
{
  "version": 1,
  "generated_at": "2025-05-09T18:00:00Z",
  "skills": {
    "systematic-debugging": {
      "aliases": ["debug", "排错", "调试", "troubleshoot", "bug", "报错"],
      "use_when": "遇到 bug、测试失败、意外行为、崩溃、错误输出、需要排查问题",
      "category": "debugging"
    },
    "ag": {
      "aliases": ["subagent", "子代理", "并行", "spawn agent", "多步骤", "orchestrate"],
      "use_when": "复杂多步骤工作、worker-judge 循环、需要独立审查、任务编排",
      "category": "orchestration"
    },
    "pdf": {
      "aliases": ["PDF", "pdf", "文档处理", "merge pdf", "split pdf"],
      "use_when": "读取 PDF、合并拆分、旋转页面、提取文字或表格",
      "category": "file-processing"
    }
  }
}
```

Each skill entry has:
- `aliases`: synonyms and translations (mixed Chinese/English) for name matching
- `use_when`: natural language description of when to use this skill — used for substring matching
- `category`: grouping for browsing (e.g., "debugging", "orchestration", "file-processing")

### 4.3 New skill: `~/.ai/skills/index-skills/SKILL.md`

A standard skill that instructs the agent to regenerate `skill-index.json`:

```markdown
---
name: index-skills
description: Regenerate the skill search index. Run this when skills are added/removed/updated.
---

# Index Skills

Read all SKILL.md files from ~/.ai/skills/, then generate ~/.ai/skill-index.json.

## Steps

1. List all skill directories: `ls ~/.ai/skills/`
2. For each skill, read the SKILL.md frontmatter (name + description)
3. Based on name and description, generate for each skill:
   - `aliases`: 5-10 synonyms including Chinese translations
   - `use_when`: One sentence describing when to use this skill (Chinese)
   - `category`: A category tag
4. Write the result to ~/.ai/skill-index.json
5. Log how many skills were indexed
```

### 4.4 New file: `pkg/tools/find_skill.go` — Discovery tool

```go
package tools

type FindSkillTool struct {
    skills    []skill.Skill
    stats     *skill.SkillStatsFile
}

func NewFindSkillTool(skills []skill.Skill, stats *skill.SkillStatsFile) *FindSkillTool

// Name() → "find_skill"
// Description() → "Search and load skills by keyword or name.
//   Use 'query' to search skill names and descriptions (fuzzy match).
//   Use 'name' with 'load=true' to read the full skill content.
//   Always search first, then load the matching skill before acting."
//
// Parameters():
//   {
//     "name":  { type: "string", description: "Exact skill name to load" },
//     "query": { type: "string", description: "Search keyword for skill name/description" },
//     "load":  { type: "boolean", description: "Set true to return full skill content (requires 'name')" }
//   }

// Execute logic:
//   - If query is set: load skill-index.json, search across:
//     1. skill name (substring)
//     2. skill description (substring, from loaded skills list)
//     3. index aliases (substring match)
//     4. index use_when (substring match)
//     5. index category (exact or substring)
//     Return top 10 matches, deduplicated by skill name.
//   - If name is set and load=true: read SKILL.md, call stats.RecordUsage(name), return full content
//   - If name is set and load=false: return just name + description + path for that skill
//
// If skill-index.json doesn't exist, fall back to name+description substring only
// and suggest running /skill:index-skills to generate the index.
```

**Search return format** (query mode):
```
Found 3 skills matching "debug":

1. **systematic-debugging**: Use when encountering any bug, test failure, or unexpected behavior, before proposing fixes
   Path: /Users/genius/.ai/skills/systematic-debugging/SKILL.md

2. **learn-codebase**: Discover project conventions and surface security concerns
   Path: /Users/genius/.ai/skills/learn-codebase/SKILL.md

Use find_skill with name=<skill_name> and load=true to read the full skill.
```

**Load return format** (load mode):
Returns the full SKILL.md content (body only, frontmatter stripped), same as what `ExpandCommand` produces.

### 4.5 Changes to `pkg/skill/formatter.go` — Use stats for top-N

```go
// FormatForPrompt now takes stats to determine which skills to show.
func FormatForPrompt(skills []Skill, stats *SkillStatsFile) string
```

Logic:
1. If stats is nil or empty, fall back to current behavior (all skills, up to 24)
2. Get `stats.TopSkills(stats.TopN)` — returns top N skill names
3. Only render those skills in the prompt
4. Add a footer: `*Use the find_skill tool to discover more skills by keyword.*`
5. Remove the `maxPromptSkills` cap (now controlled by TopN from stats)

### 4.6 Changes to `cmd/ai/rpc_handlers.go`

In the initialization block (L209-L258):

```
1. Load skills (unchanged)
2. Load stats: skill.LoadStats("~/.ai/skill-stats.json")
3. Register find_skill tool: registry.Register(tools.NewFindSkillTool(skills, stats))
4. Pass stats to prompt builder: promptBuilder.SetSkills(skillResult.Skills).SetSkillStats(stats)
```

In `/skill:name` expansion handler:
```
After ExpandCommand(), call stats.RecordUsage(skillName)
```

### 4.7 Changes to `pkg/prompt/builder.go`

Add `skillStats *skill.SkillStatsFile` field to `Builder`.
Pass it through to `FormatForPrompt(skills, skillStats)`.

### 4.8 Changes to `claw/cmd/aiclaw/main.go`

- Change `clawDir` from `~/.aiclaw` to `~/.ai`
- `~/.aiclaw/skills/` symlink can be removed after migration
- Same stats loading, same `find_skill` tool registration
- `buildSkillsSummary()` replaced by `FormatForPrompt(skills, stats)`

### 4.9 Migration

```bash
# Move aiclaw skills into ~/.ai/skills/
cp -r claw/skills/* ~/.ai/skills/
rm ~/.aiclaw/skills  # remove symlink
# Optionally recreate symlink for backward compat:
# ln -s ~/.ai/skills ~/.aiclaw/skills
```

---

## 5. 边界条件

### Cold start (no stats file)
- `LoadStats` returns empty `SkillStatsFile` with `TopN=7`
- `TopSkills(7)` returns empty list
- `FormatForPrompt` falls back to showing ALL skills (current behavior), capped at 7
- After a few sessions, stats accumulate and auto-ranking kicks in

### Skill name collision after merge
- Both trees have a `github` skill and a `tmux` skill
- Loader already handles collisions: first-loaded wins, diagnostic logged
- After merge, both are in same directory → collision at directory level → needs manual resolution
- **Action**: Audit collisions before merge, pick the better version

### Concurrent access to skill-stats.json
- Two agent processes writing simultaneously → possible data loss (last-write-wins on the overwritten field)
- **Mitigation**: Use `os.OpenFile` with `O_WRONLY|O_CREATE|O_TRUNC` and keep writes small
- **Acceptable risk**: Losing a single +1 is inconsequential for ranking

### find_skill with no results
- Return helpful message: `"No skills found matching 'xyz'. Use find_skill with a different keyword."`
- Don't hallucinate skills

### Skills removed from disk but still in stats
- `TopSkills` returns a name → lookup in loaded skills → not found → skip
- No crash, just a missing entry. Stats entry becomes inert.

### Backward compatibility with /skill:name
- `/skill:name` expansion continues to work for ALL skills (not just top-N)
- It goes through `ExpandCommand` which searches the full skill list
- Stats recording on `/skill:name` is purely additive, doesn't change behavior

---

## Files to create

| File | Purpose |
|------|---------|
| `pkg/skill/stats.go` | Usage tracking with time decay |
| `pkg/skill/stats_test.go` | Unit tests for stats |
| `pkg/tools/find_skill.go` | Discovery tool implementation |
| `pkg/tools/find_skill_test.go` | Tool tests |
| `~/.ai/skills/index-skills/SKILL.md` | Skill that generates skill-index.json via LLM |

## Runtime artifacts (not in repo)

| File | Purpose |
|------|---------|
| `~/.ai/skill-stats.json` | Usage frequency data |
| `~/.ai/skill-index.json` | LLM-generated search index |

## Files to modify

| File | Change |
|------|--------|
| `pkg/skill/formatter.go` | Accept stats param, use TopSkills for filtering |
| `pkg/prompt/builder.go` | Add skillStats field, pass to FormatForPrompt |
| `cmd/ai/rpc_handlers.go` | Load stats, register find_skill tool, record usage on /skill:name |
| `claw/cmd/aiclaw/main.go` | Change agentDir to ~/.ai, use shared stats/tool logic |

## Migration (manual)

- Copy `claw/skills/*` → `~/.ai/skills/`
- Remove `~/.aiclaw/skills` symlink
- Resolve name collisions (github, tmux, skill-creator)