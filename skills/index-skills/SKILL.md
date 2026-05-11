---
name: index-skills
description: Generate a rich search index of all skills at ~/.ai/skill-index.json. Use when skills change or when /skills reindex is triggered. Produces aliases, use-when triggers, and categories for each skill via LLM intelligence. Supports incremental updates.
---

# Index Skills — Generate Skill Search Index

## Overview

Read `SKILL.md` files from `~/.ai/skills/`, analyze them, and produce a rich search index at `~/.ai/skill-index.json`. This index enables semantic skill discovery — synonym matching, Chinese↔English equivalents, and use-case intent — without requiring an LLM call at query time.

Supports **incremental updates**: only re-index skills that were added, removed, or modified since the last run.

## When to Use

- User triggers `/skills reindex`
- After adding, removing, or significantly updating skills
- On first run when `~/.ai/skill-index.json` does not exist

## Process

### Step 1: Discover all skill files and collect mtimes

Use the `bash` tool to list all SKILL.md files with their modification timestamps:

```bash
find -L ~/.ai/skills -maxdepth 2 -name 'SKILL.md' -type f -not -path '*/.worktrees/*' -not -path '*/.ag/*' | while read f; do
  dir=$(basename "$(dirname "$f")")
  mtime=$(stat -f '%m' "$f" 2>/dev/null || stat -c '%Y' "$f" 2>/dev/null)
  echo "$dir|$mtime"
done | sort
```

Parse the output into a map of `{ name → mtime }` for all discovered skills.

If no files found, produce an empty index (see Step 4) and stop.

### Step 2: Determine incremental changes

Read the existing index file at `~/.ai/skill-index.json` (if it exists). Compare against discovered skills:

| Condition | Action |
|---|---|
| Index file does not exist | **Full rebuild** — read all SKILL.md files |
| Skill exists on disk but not in index (`added`) | Read and index that skill |
| Skill in index but not on disk (`removed`) | Remove that entry from index |
| Skill on disk has mtime newer than index entry (`modified`) | Read and re-index that skill |
| Skill on disk and mtime unchanged | **Skip** — keep existing entry |

**Mtime comparison rule:** Each index entry stores an `mtime` field (Unix epoch seconds). If the on-disk mtime is strictly greater than the stored mtime, the skill is considered modified.

If ALL skills are unchanged (no added/removed/modified), report "Index up to date, no changes needed" and stop.

### Step 3: Read changed SKILL.md files

For each skill identified as `added` or `modified` in Step 2, read its full content using the `read` tool. Extract:
- **name**: from frontmatter `name:` field (or directory name as fallback)
- **description**: from frontmatter `description:` field
- **Full body text**: everything after the frontmatter — needed to infer use-cases, aliases, and categories

Read all changed files in parallel for speed.

### Step 4: Analyze and generate entries

For each changed skill, produce one JSON object with these fields:

```json
{
  "name": "skill-directory-name",
  "description": "One-sentence description of what the skill does",
  "aliases": ["synonym1", "synonym2", "debug", "调试", "troubleshoot"],
  "use_when": [
    "encountering bugs",
    "test failures",
    "unexpected behavior"
  ],
  "categories": ["debugging", "development"],
  "mtime": 1720712345
}
```

#### Field rules

- **name**: Must match the directory name under `~/.ai/skills/`
- **description**: Concise, in English, derived from frontmatter + first heading
- **aliases**: 3–8 entries. Include:
  - Common abbreviations and synonyms
  - Chinese translations if the skill description or body contains Chinese
  - Related verbs/nouns a user might search for
  - Do NOT include the name itself (it's already matched)
- **use_when**: 2–5 short phrases describing WHEN a user would want this skill. Think about user intent, not skill mechanics.
- **categories**: 1–3 broad category labels. Use consistent categories across skills (e.g., "development", "debugging", "testing", "orchestration", "git", "documentation", "system", "planning").
- **mtime**: Unix epoch seconds from Step 1, stored for future incremental comparisons.

When re-indexing a modified skill, review its existing aliases and categories for consistency with the updated content, but preserve stable entries that still apply.

### Step 5: Merge and write the index

Construct the final index by merging:

1. **Keep** all unchanged entries from the existing index (verbatim, no re-analysis)
2. **Replace** entries for modified skills with newly analyzed ones
3. **Add** entries for new skills
4. **Remove** entries for deleted skills

Write the merged result to `~/.ai/skill-index.json`:

```json
{
  "version": 1,
  "generated_at": "2025-07-11T12:00:00Z",
  "entry_count": 21,
  "entries": [
    { "...per-skill object with mtime..." }
  ]
}
```

- `generated_at`: ISO 8601 timestamp of generation time (use current time)
- `entry_count`: Must equal `entries.length` — verify before writing
- Overwrite any existing file

### Step 6: Report results

After writing the file, report:
- How many skills were changed (added/modified/removed)
- Total skills in index
- The output file path
- A brief summary of the categories found

Example outputs:

```
Incremental update: +1 added, 0 modified, 0 removed → 55 total skills
Indexed 55 skills → ~/.ai/skill-index.json
Categories: orchestration (3), development (5), ...
```

```
Index up to date, no changes needed. (55 skills)
```

```
Full rebuild: 55 skills → ~/.ai/skill-index.json
Categories: orchestration (3), development (5), ...
```

## Edge Cases

- **No skills found**: Write an index with `entry_count: 0` and empty `entries` array
- **Skill missing frontmatter**: Use directory name as `name`, first heading paragraph as `description`
- **Duplicate names**: Should not happen; if it does, keep both with disambiguating descriptions
- **Force full rebuild**: User can say "full reindex" to force reading all skills regardless of mtime
- **Legacy index without mtime**: If existing entries lack `mtime` field, treat them as modified (re-read those skills)