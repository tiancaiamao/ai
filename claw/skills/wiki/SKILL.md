---
name: wiki
description: Maintain and query a personal LLM Wiki — an incremental, compiled knowledge base of structured Markdown files.
metadata:
  {
    "goclaw":
      {
        "emoji": "📚",
        "requires": {},
      },
  }
---

# Wiki — Personal Knowledge Base

An LLM-maintained wiki that compiles knowledge once and keeps it current, instead of re-deriving from raw sources on every query.

## Wiki Location

**Default**: `~/project/zen/`

This is an Obsidian vault. The wiki lives at:

```
~/project/zen/
├── AGENTS.md              # Wiki schema & conventions
├── raw/                   # Immutable source documents
│   ├── webclips/
│   ├── papers/
│   ├── repos/
│   └── assets/
├── wiki/
│   ├── index.md           # Hub — lists all pages, updated on every ingest
│   ├── log.md             # Append-only activity log
│   ├── summaries/         # One summary page per source
│   ├── concepts/          # Entity / topic pages (evolving synthesis)
│   └── entities/          # Entity pages (reserved)
```

If the user's project has a different vault path, adjust accordingly.

## Operations

### Ingest — Add a New Source

When the user provides a new source (file, URL, pasted text):

1. **Save raw source** to `raw/webclips/` (or `raw/papers/`, `raw/repos/` as appropriate). Filename: `slug-name.md`.
2. **Create summary page** in `wiki/summaries/slug-name.md`:
   - YAML frontmatter: `tags`, `date`, `sources`
   - One-sentence Summary
   - Extracted key points, organized by section
   - `## Related Notes` with `[[wikilinks]]`
3. **Create or update concept pages** in `wiki/concepts/`:
   - Extract key entities and topics from the source
   - Create new concept pages if they don't exist
   - Update existing concept pages with new information, flag contradictions
4. **Update `wiki/index.md`** — add new entries under the correct category
5. **Append to `wiki/log.md`** — format:
   ```
   ## [YYYY-MM-DD] ingest | Source Title
   - **Sources**: path to raw file(s)
   - **Created**: list of new wiki pages
   - **Updated**: list of updated wiki pages
   - **Notes**: brief description
   ```

### Query — Answer Questions

When the user asks a question about knowledge in the wiki:

1. **Read `wiki/index.md`** to locate relevant pages
2. **Read relevant pages** — summaries first, then concept pages for depth
3. **Synthesize an answer** citing specific wiki pages and raw sources
4. **Surface gaps** — if the question can't be fully answered, note what's missing and suggest sources to ingest

### Maintain — Keep the Wiki Healthy

Periodically or on request:

1. Re-read pages for inconsistencies
2. Merge duplicate pages
3. Update stale summaries
4. Strengthen or add missing `[[wikilinks]]`
5. Verify all pages listed in `index.md` still exist

## File Conventions

Every wiki page **must** have:

```yaml
---
tags: [relevant, tags]
date: YYYY-MM-DD
sources: [source-slug]
---
```

Every wiki page **should** have:
- A **one-sentence Summary** after the heading
- **`## Related Notes`** section with `[[wikilinks]]`

### Naming
- Filenames: `lowercase-with-hyphens.md`
- Wikilinks: `[[filename-without-ext]]` (Obsidian convention)

## Search Strategy

At current scale (~dozens of pages):
- `wiki/index.md` is sufficient for locating pages
- `wiki/scripts/wiki-search.sh <query>` for content search (uses `rg` if available)
- `wiki/scripts/wiki-search.sh <query> --raw` to also search raw/ sources

As the wiki grows (>100 pages):
- Consider [qmd](https://github.com/tobi/qmd) for hybrid BM25/vector search
- Install: `npm install -g @tobilu/qmd` (requires Node.js)

## Lint / Health Check

Run `wiki/scripts/wiki-lint.sh` to verify:
- Required directories and files exist
- YAML frontmatter completeness (tags, date)
- Wikilink resolution (broken links flagged as warnings)
- Index ↔ actual files consistency
- Source references in summaries point to real files in raw/

Exit code = error count (0 = healthy).

## Quick Reference

| Action | How |
|--------|-----|
| Add a source | Ingest workflow (5 steps above) |
| Ask a question | Read index → read pages → synthesize |
| Find a page | Check `wiki/index.md` |
| Search content | `wiki/scripts/wiki-search.sh "query"` |
| Search including raw | `wiki/scripts/wiki-search.sh "query" --raw` |
| Health check | `wiki/scripts/wiki-lint.sh` |
| Browse visually | Open Obsidian → Graph View |
| Check recent activity | `grep "^## \[" wiki/log.md \| tail -5` |
| Check wiki health | Verify index ↔ actual files match |