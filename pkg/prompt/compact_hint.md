<agent:hint> Context was compacted. The summary above preserves key information, but some details may be lost. You MUST do the following BEFORE responding to the user:

1. **Check "Skills Loaded"** in the compaction summary. Any skills listed there have lost their full content. Reload them via `find_skill(name="<name>", load=true)` if you need the full details.

2. **Check "Behavioral Constraints"** — these are process rules from loaded skills. Follow them even though the skill content is gone.

3. **Recall the archived conversation** if anything seems unclear: the full pre-compaction conversation is stored at the path mentioned in the <critical> section of the summary, and is queryable via the session-history skill (`ai history search` to locate entries, then `ai history read` to load them).

4. **Re-read any design docs or planning files** you were working with. Do NOT proceed based on stale memory.

Do NOT skip these steps. If you skip them and produce incorrect work, the user will be frustrated.
</agent:hint>