---
name: session-history
description: Retrieve content from earlier in the session that is no longer in the live window, e.g. after a compact, context reset, or when the user refers to previous discussion ("we talked about this before", 之前) or 历史 ("we said earlier"). Use to recover lost context via the `ai history` CLI: search past messages, list windows (compaction generations), and read entries with pagination.
---

# Session History (`ai history`)

## When to Use This Skill

- The user refers to something said **before** in this session ("earlier you said…", "我们之前说过…") and it is not in the live window.
- You need to **recover** details lost to a **compact** (summarization replaced the original messages).
- You want to verify what actually happened earlier rather than relying on a summary.

Every invocation is bounded: results are limited and truncated with explicit markers, so calling these commands never floods your context.

## First: Find Your Run ID

Your run ID is injected into your context in the `<agent:runtime_state/>` block as a `run_id:` line (6 hex characters). Pass it explicitly with `--id`:

```
ai history search "auth bug" --id cbbcf4
```

If you omit `--id`, the command auto-selects the run matching the current working directory; with multiple candidate runs it errors and lists them (it never silently guesses).

## Command Reference

All actions accept the global flags `--id <run-id|prefix>` and `--json` (JSONL output).

| Action | Purpose | Flags |
|---|---|---|
| `windows` | List compaction generations (windows) | `--limit <n>` (default 20, max 100), `--oldest-first` |
| `list` | List items in a window or along the current path | `--window <id>`, `--role user\|assistant\|tool\|system\|developer`, `--no-tool`, `--entry <id>` (entry + ancestor chain), `--limit <n>` (default 20, max 100), `--max-chars <n>` (default 400, max 2000), `--oldest-first` |
| `read` | Read one entry in full, character-paginated | `--entry <id>` (required), `--offset-chars <n>`, `--limit-chars <n>` (default 20000, max 50000) |
| `search` | Literal substring search over messages and compaction snapshots | `<query>` (required, 1..1000 chars), `--window <id>`, `--role <role>`, `--limit <n>` (default 20, max 100), `--case-sensitive` |

`search` results include `entry_id`, `window_id`, a match excerpt, and `total_count`.

## Example: search → read

Step 1 — search to locate the entry (get `entry_id` and `total_count`):

```
ai history search "database migration" --id cbbcf4
```

Step 2 — read the full content of the matching entry, paging if needed:

```
ai history read --entry e12ab34 --id cbbcf4
# If total_chars exceeds the returned content:
ai history read --entry e12ab34 --id cbbcf4 --offset-chars 20000 --limit-chars 20000
```

## Discipline

- **Search first.** Use `search` to obtain the `entry_id` and `total_count` before any `read`.
- **Then read with pagination.** Use `--offset-chars` / `--limit-chars` to page through long entries.
- **Do not blindly increase `--limit`.** Large limits produce output that gets truncated anyway at the 40000-character cap; prefer narrower queries or `--window` / `--role` filters instead.