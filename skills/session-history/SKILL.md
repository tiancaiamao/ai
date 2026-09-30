---
name: session-history
description: Retrieve content from earlier in a session that is no longer in the live window, e.g. after a compact, context reset, or when the user refers to previous discussion ("we talked about this before", 之前) or 历史 ("we said earlier"). Also for digging through ANOTHER agent's session when no run ID is known. Use to recover lost context via the `ai history` CLI: search past messages, locate sessions/runs on disk, list windows (compaction generations), and read entries with pagination.
pinned: true
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

`search` results include `entry_id`, `window_id`, a match excerpt, and `total_count`. Use `search` first to locate an entry, then `read` it.

`--max-chars` is accepted by `list` and `read` only. `windows` and `search` have no content-length flag: their output volume is bounded by `--limit` and the global 40000-character cap. Passing `--max-chars` to them is an error that lists the flags each action does accept.

## `search` Matches Literal Substrings, Not Patterns

`search` is a plain substring test (case-insensitive unless `--case-sensitive`). It is **not** grep: no regex, no multi-word AND, no cross-field matching. Every character you pass must appear contiguously inside one message.

```
❌ search "spawn.*Generator"          # * and . are literal characters
❌ search "M6-T1 implementation"      # the whole string, space included, must appear as-is
❌ search "[2e2e2e]"                  # [ ] are not a character class
❌ search "make test > /tmp/x.log"    # a whole command line is not a substring of anything
✅ search "spawn" --limit 20          # one token, coarse pass
✅ search "M6-T1" --role user         # narrow by role to where the answer likely lives
```

Note that some metacharacters are legitimate literal queries — `search "[2e2e2e]"` is how you look up a git object id, and `search " | "` finds pipes. The output tells you which case you are in: a metacharacter query that returns matches was understood as intended.

**When a query returns 0 matches, do not make it longer or more specific.** Escalating `"Same Generator"` → `"spawn.*Generator"` → `"M6-T1 implementation"` moves away from every hit, because the original token was already as short as it can get. Do this instead:

1. Re-run the **shortest single token** you can think of, with `--limit 20`.
2. Add `--role user` or `--role assistant` to aim the search.
3. If you know roughly *when*, use `windows --oldest-first` / `list --window` to browse structure, then read the entries that look relevant.

## Window IDs Are Not Entry IDs

`windows` (and the `window=` column of `search`) reports **window ids** — compaction generations, one per summarization. `search` and `list` report **entry ids** — individual messages. Both are short hex strings and look alike, so check which one you are holding before passing it to `read --entry`.

The standard path is `windows` → `list` → `read`:

```
ai history windows --id X                                # 1. find the relevant generation, note the WINDOW_ID column
ai history list --id X --window <W> --oldest-first       # 2. expand it, collect entry_ids
ai history read --id X --entry <E>                       # 3. read one in full
```

Looking for a specific phrase? Skip `windows` and go straight to `search` — it returns entry ids, and its `window=` column tells you which generation each hit came from.

Text output starts with a column legend (`WINDOW_ID` / `ENTRY_ID`, and `(UTC)` on timestamps) for exactly this reason. Addressing a window id with `read --entry` / `list --entry` fails with a message naming the mistake and the flag to use instead — keep your existing `--id` / `--session` when you run it.

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
- **Shortlisting**: `grep -rl "keyword" ~/.ai/sessions/<project-dir>/*/` — scan the whole session dir, not just `messages.jsonl`: after compaction, old-window messages live only in `compactions/*.jsonl` snapshots. Rank candidates by mention count and file mtime. Raw grep is the right tool for this sweep; `ai history` takes over once a session is identified.
- **Binding a session to a run ID**: preferred: `grep -l '"session": "<session-uuid>"' ~/.ai/runs/*/run.json` pairs run id ↔ session directly (newer runs record the session UUID in run.json; some older runs lack the field). Fallback for those: correlate the agent's **reply timestamp with the session file's mtime** (second-level match) — weaker, since resumed runs and message appends also bump mtime.
- **Confirm a candidate**: `ai history list --session <path> --role user --oldest-first --max-chars 800` shows the session's opening task; then proceed with search → read.

## Forensics: keep tool messages, but expect self-matches

`search` scans tool results, because for archaeology they *are* the goldmine — the commands that ran and the output they produced. The cost is self-matching: every `ai history` invocation is itself recorded as a tool message, so the **next** `search` in the same session matches the previous one's output. That noise is the newest content, so it ranks at the top, and repeated searching in one session makes it worse each time. Choose by question:

- **What did the assistant conclude?** → `search ... --no-tool`, usually with `--role assistant`.
- **What actually happened?** (commands run, errors emitted) → keep tool messages, use `--role tool` to aim at them, and prefer `windows` / `list --window` to narrow to a generation. A full-library search will drown in your own earlier search output.

## Timestamps are UTC

Entry timestamps are UTC — the `(UTC)` in the text-mode column legend marks this at the point of use. Log lines *inside* message content that the agent printed itself are usually local time. Mind the offset (e.g. UTC+8) when correlating the two — an 8-hour gap is a timezone artifact, not a missing record. `--json` output is always UTC.

## Discipline

- **Search first.** Use `search` to obtain the `entry_id` and `total_count` before any `read`.
- **Then read with pagination.** Use `--offset-chars` / `--max-chars` to page through long entries.
- **Do not blindly increase `--limit`.** Large limits produce output that gets truncated anyway at the 40000-character cap; prefer narrower queries or `--window` / `--role` filters instead.