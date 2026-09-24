<agent:hint> Context was compacted. The summary above preserves key information, but some details may be lost. You MUST do the following BEFORE responding to the user:

1. **Check "Skills Loaded"** in the compaction summary. A listed skill's full content may be gone, but the summary may preserve the instructions you need. Check the summary first; reload a skill with `find_skill(name="<name>", load=true)` only when you need details that are not preserved there.

2. **Check "Behavioral Constraints"** — these are process rules from loaded skills. Follow them even though the skill content is gone.

3. **Recall the archived conversation** if anything seems unclear: the full pre-compaction conversation is archived as pages and queryable via the `ai history` CLI — use the exact commands in the <critical> section of the summary (`windows` for the page index, `search` to locate entries, `read` to load them). The `session-history` skill (find_skill) documents the full CLI reference. Never read the raw JSONL files under compactions/ directly.

4. **Re-read any design docs or planning files** you were working with. Do NOT proceed based on stale memory.

Do NOT skip these steps. If you skip them and produce incorrect work, the user will be frustrated.
</agent:hint>