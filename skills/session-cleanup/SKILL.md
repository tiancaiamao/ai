---
name: session-cleanup
description: Clean up ~/.ai/ disk space (runs, sessions, traces). Use when user says "清理", "磁盘空间", "cleanup", or wants to free space under ~/.ai/.
---

# Cleanup — ~/.ai/ Disk Space Management

One-command cleanup for `~/.ai/` storage. Safe by default, shows before deleting.

## User Contract

User says:
- "清理一下"
- "磁盘空间是不是多了"
- "帮我清一下 ai 的缓存"
- "cleanup"

Agent responds with: current usage → what to clean → confirm → execute.

## What Gets Cleaned

| Target | Location | Growth Rate | Typical Size |
|--------|----------|-------------|--------------|
| runs | `~/.ai/runs/` | **HIGH** — agent event logs (events.jsonl) | 10-40 GB |
| sessions | `~/.ai/sessions/` | Low — conversation journals | 100-1000 MB |
| traces | `~/.ai/traces/` | Medium — perfetto trace files | 1-10 GB |

**runs/ is always the biggest offender.** A single long-running agent can produce 1-19 GB of `events.jsonl`.

## Workflow

### Step 1: Show Current Usage

Always start here. User needs to see the situation before deciding.

```bash
du -sh ~/.ai/runs/ ~/.ai/sessions/ ~/.ai/traces/ 2>/dev/null
```

### Step 2: Analyze What's Cleanable

Pick the right analysis based on what's big:

**For runs/ (>1GB):**
```bash
# Count by age
echo "=== runs/ age distribution ===" && \
  find ~/.ai/runs/ -maxdepth 1 -type d -exec stat -f '%Sm' -t '%Y-%m-%d' {} \; 2>/dev/null | sort | uniq -c | sort -k2

# Show what would be deleted (default: >3 days)
echo "=== Would delete (>3 days) ===" && \
  find ~/.ai/runs/ -maxdepth 1 -type d -mtime +3 -exec du -sh {} + 2>/dev/null | sort -rh | head -20

# Calculate total
find ~/.ai/runs/ -maxdepth 1 -type d -mtime +3 -print0 | xargs -0 du -sm 2>/dev/null | awk '{sum+=$1} END {printf "Total: %.1f MB / %.2f GB\n", sum, sum/1024}'
```

**For sessions/ (>500MB):**
```bash
# Use bundled script — dry run first
bash $(dirname "$0")/scripts/clean-sessions.sh
```

**For traces/ (>2GB):**
```bash
# Count by age
echo "=== traces/ age distribution ===" && \
  find ~/.ai/traces/ -maxdepth 1 -type f -exec stat -f '%Sm' -t '%Y-%m-%d' {} \; 2>/dev/null | sort | uniq -c | sort -k2

# Show what would be deleted (>3 days)
find ~/.ai/traces/ -maxdepth 1 -type f -mtime +3 -exec du -sh {} + 2>/dev/null | awk '{sum+=$1} END {printf "Total: %.1f MB / %.2f GB\n", sum, sum/1024}'
```

### Step 3: Confirm and Execute

**Always confirm with user before deleting.** Show:
- What will be deleted (runs/sessions/traces)
- Age threshold (default: 3 days)
- Total space to free

**Default thresholds:**
- runs/: `mtime +3` (older than 3 days)
- sessions/: use bundled `scripts/clean-sessions.sh` with `MODE=stale STALE_DAYS=3`
- traces/: `mtime +3`

**Execute:**

```bash
# Clean runs (no confirmation prompt needed — user already confirmed)
find ~/.ai/runs/ -maxdepth 1 -type d -mtime +3 -exec rm -rf {} +

# Clean sessions (script path relative to skill root)
DRY_RUN=false MODE=stale STALE_DAYS=3 bash scripts/clean-sessions.sh <<< "yes"

# Clean traces
find ~/.ai/traces/ -maxdepth 1 -type f -mtime +3 -delete
```

### Step 4: Report Result

```bash
echo "=== After cleanup ===" && du -sh ~/.ai/runs/ ~/.ai/sessions/ ~/.ai/traces/ 2>/dev/null
```

Show before/after comparison.

## User Customization

If user specifies different preferences:
- "只清 runs" → skip sessions and traces
- "清 7 天前的" → change `mtime +3` to `mtime +7`
- "全部清掉" → `mtime +0` (everything not from today), use `MODE=all`
- "只看看" → dry-run only, show what would be deleted

## Anti-Patterns

- **Never delete without showing first.** Always run Step 1 and Step 2 before Step 3.
- **Never delete `~/.ai/skills/`, `~/.ai/prompts/`, `~/.ai/templates/`.** These are user config.
- **Never hardcode the confirmation.** Ask the user, don't assume "yes".
- **Don't run the session cleaner with `MODE=all` unless user explicitly says "全部".**

## Known Issues

- `events.jsonl` in runs/ can be 19GB+ for a single run due to empty thinking stream updates (a bug in the streaming layer). This is the #1 cause of disk bloat.
- traces/ has no built-in cleanup mechanism, so it grows unbounded without this skill.