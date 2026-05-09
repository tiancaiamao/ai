---
name: index-skills
description: Generate a rich search index of all skills at ~/.ai/skill-index.json. Use when skills change or when /skills reindex is triggered. Produces aliases, use-when triggers, and categories for each skill via LLM intelligence.
---

# Index Skills — Generate Skill Search Index

## Overview

Read all `SKILL.md` files from `~/.ai/skills/`, analyze them, and produce a rich search index at `~/.ai/skill-index.json`. This index enables semantic skill discovery — synonym matching, Chinese↔English equivalents, and use-case intent — without requiring an LLM call at query time.

## When to Use

- User triggers `/skills reindex`
- After adding, removing, or significantly updating skills
- On first run when `~/.ai/skill-index.json` does not exist

## Process

### Step 1: Discover all skill files

Use the `bash` tool to list all SKILL.md files:

```bash
find ~/.ai/skills -maxdepth 2 -name 'SKILL.md' -type f | sort
```

If no files found, produce an empty index (see Step 4) and stop.

### Step 2: Read every SKILL.md

For each discovered file, read its full content using the `read` tool. Extract from each file:
- **name**: from frontmatter `name:` field (or directory name as fallback)
- **description**: from frontmatter `description:` field
- **Full body text**: everything after the frontmatter — needed to infer use-cases, aliases, and categories

Read all files in parallel batches for speed.

### Step 3: Analyze and generate the index

Using your knowledge of ALL skills read in Step 2, produce one JSON object per skill with these fields:

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
  "categories": ["debugging", "development"]
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

### Step 4: Write the index file

Write the final JSON to `~/.ai/skill-index.json` using the `write` tool with this structure:

```json
{
  "version": 1,
  "generated_at": "2025-07-11T12:00:00Z",
  "entry_count": 21,
  "entries": [
    { "...per-skill object..." }
  ]
}
```

- `generated_at`: ISO 8601 timestamp of generation time (use current time)
- `entry_count`: Must equal `entries.length` — verify before writing
- Overwrite any existing file (idempotent operation)

### Step 5: Report results

After writing the file, report:
- How many skills were indexed
- The output file path (`~/.ai/skill-index.json`)
- A brief summary of the categories found

Example output:

```
Indexed 21 skills → ~/.ai/skill-index.json
Categories: orchestration (3), development (5), debugging (2), planning (2), git (2), system (3), documentation (2), testing (1), review (1)
```

## Edge Cases

- **No skills found**: Write an index with `entry_count: 0` and empty `entries` array
- **Skill missing frontmatter**: Use directory name as `name`, first heading paragraph as `description`
- **Duplicate names**: Should not happen; if it does, keep both with disambiguating descriptions
- **Re-run**: Always overwrites previous index; operation is idempotent