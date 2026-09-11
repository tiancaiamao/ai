---
name: session-history
description: Retrieve content from earlier in a session that is no longer in the live window, e.g. after a compact, context reset, or when the user refers to previous discussion ("we talked about this before", 之前) or 历史 ("we said earlier"). Also for digging through ANOTHER agent's session when no run ID is known. Use to recover lost context via the `ai history` CLI: search past messages, locate sessions/runs on disk, list windows (compaction generations), and read entries with pagination.
---

# Session History (`ai history`)

## When to Use This Skill

- The user refers to something said **before** in this session ("earlier you said…", "我们之前说过…") and it is not in the live window.
- You need to **recover** details lost to a **compact** (summarization replaced the original messages).
- You want to verify what actually happened earlier rather than relying on a summary.

Every invocation is bounded: results are limited and truncated with explicit markers, so calling these commands never floods your context.

## First: Find Your Run ID

Your run ID is injected into your context in the `<agent:runtime_state/>` block as a `run_id:` line (6 hex characters). Pass it explicitly with `--id` — **`--id` is required**; there is no cwd-based auto-select, because an agent's working directory can change during its lifetime and no longer identifies the run:

```
ai history search "auth bug" --id cbbcf4
```

`--id` also accepts a unique prefix; ambiguous prefixes error with a bounded candidate list — use a longer prefix. Both running and finished runs are matched. `--session <path>` is an escape hatch that points directly at a session directory, bypassing run resolution.

That covers finding *your own* run ID. When you need to dig through **another agent's** session — or find which session did something without knowing any ID — see "Cross-Session Archaeology" below.

## Command Reference

All actions accept the global flags `--id <run-id|prefix>` and `--json` (JSONL output).

| Action | Purpose | Flags |
|---|---|---|
| `windows` | List compaction generations (windows) | `--limit <n>` (default 20, max 100), `--oldest-first` |
| `list` | List items in a window or along the current path | `--window <id>`, `--role user\|assistant\|tool\|system\|developer`, `--no-tool`, `--entry <id>` (entry + ancestor chain), `--limit <n>` (default 20, max 100), `--max-chars <n>` (default 400, max 2000), `--oldest-first` |
| `read` | Read one entry in full, character-paginated | `--entry <id>` (required), `--offset-chars <n>`, `--max-chars <n>` (default 20000, max 50000) |
| `search` | Literal substring search over messages and compaction snapshots | `<query>` (required, 1..1000 chars), `--window <id>`, `--role <role>`, `--no-tool`, `--limit <n>` (default 20, max 100), `--case-sensitive` |

`search` results include `entry_id`, `window_id`, a match excerpt, and `total_count`. Note that search scans tool results too — an agent's own earlier search output gets recorded as tool messages and self-matches; pass `--no-tool` to suppress that noise.

Truncation is always `--max-chars` (all actions); `read` adds `--offset-chars` for paging.

## JSON schema

`--json` emits one JSON object per line:

- `search`: `entry_id`, `role`, `window_id`, `timestamp`, `match` (excerpt), `total_count`
- `read`: `entry_id`, `role`, `window_id`, `timestamp`, `content`, `total_chars`
- `list`: same fields as `read` (`entry_id`, `role`, `window_id`, `timestamp`, `content`, `total_chars`)
- `windows`: `window_id`, `created_at`, `tokens_before`, `item_count`, `summary_preview`

## Composing with jq / shell

`--json` emits JSONL and every action exits 0/1, so output composes with standard tooling:

```
# Collect matching entry IDs, then read each in full
ai history search "timeout" --id cbbcf4 --json | jq -r '.entry_id' \
  | while read -r e; do ai history read --entry "$e" --id cbbcf4; done
```

## Example: search → read

Step 1 — search to locate the entry (get `entry_id` and `total_count`):

```
ai history search "database migration" --id cbbcf4
```

Step 2 — read the full content of the matching entry, paging if needed:

```
ai history read --entry e12ab34 --id cbbcf4
# If total_chars exceeds the returned content:
ai history read --entry e12ab34 --id cbbcf4 --offset-chars 20000 --max-chars 20000
```

## Cross-Session Archaeology (finding someone else's run/session)

`ai history` queries exactly one run or session — **there is no cross-session search** (no `--project` / `--all-runs`). To answer "which session did X", shortlist candidates on disk first, then use this skill's actions to read them:

- **Where sessions live**: `~/.ai/sessions/<munged-project-path>/<session-uuid>/messages.jsonl` (path separators become dashes, e.g. `--Users-genius-project-tinyactor--`).
- **Shortlisting**: `grep -l "keyword" ~/.ai/sessions/<project-dir>/*/messages.jsonl`, then rank candidates by mention count and file mtime. Raw grep is the right tool for this sweep; `ai history` takes over once a session is identified.
- **Binding a session to a run ID**: `ai ls` lists only live agents, and `~/.ai/runs/` holds thousands of opaque directories. Reliable trick: correlate the agent's **reply timestamp with the session file's mtime** (second-level match) to pair run id ↔ session directory.
- **Confirm a candidate**: `ai history list --session <path> --role user --oldest-first --max-chars 800` shows the session's opening task; then proceed with search → read.

## Forensics: keep tool messages

`--no-tool` answers "what did the assistant conclude". When reconstructing *what happened* (archaeology, incident review, verifying an agent's claims), **tool messages are the goldmine** — the commands that were run and the output they produced. Do not pass `--no-tool`; or use `--role tool` to target commands and outputs directly.

## Timestamps are UTC

Entry timestamps are UTC. Log lines *inside* message content that the agent printed itself are usually local time. Mind the offset (e.g. UTC+8) when correlating the two — an 8-hour gap is a timezone artifact, not a missing record.

## Discipline

- **Search first.** Use `search` to obtain the `entry_id` and `total_count` before any `read`.
- **Then read with pagination.** Use `--offset-chars` / `--max-chars` to page through long entries.
- **Do not blindly increase `--limit`.** Large limits produce output that gets truncated anyway at the 40000-character cap; prefer narrower queries or `--window` / `--role` filters instead.