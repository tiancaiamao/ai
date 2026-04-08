## Mini Compact Protocol

Triggered when context needs light maintenance without full compaction.

Use these tools from context_management.md and task_tracking.md to manage context:

### Context Management Protocol

Follow the decision tree from context_management.md:

1. **Check telemetry** — read `runtime_state` for `tokens_percent`, `stale_tool_outputs`
2. **Scan for stale outputs** — identify tool outputs no longer needed
3. **Batch truncate** — use `context_management` with `truncate` action (batch all IDs)
4. **Evaluate compact** — if `tokens_percent ≥ 30%`, also call `compact`
5. **Task tracking** — always update task status when progress made

### Decision Flow

```
START → Is tokens_percent ≥ 30%?
         │
         ├─ YES → call context_management:compact ──┐
         │                                         │
         └─ NO → continue ─────────────────────────┘
                                                  │
         Any stale tool outputs?  ──── YES → context_management:truncate
                │
                NO → Update task_tracking (skip if no progress)
```

### Batch Truncation Rules

- Collect ALL stale output IDs before calling
- Submit ONE call with full `truncate_ids` array
- Don't truncate one ID at a time

### Task Tracking

Always call `task_tracking` when:
- Task status changes
- Decisions made
- Files changed
- Progress milestone reached
- Blocker resolved

Call `task_tracking` with `skip=true` when:
- Simple questions
- Routine responses
- No state changes

### When to Stop

Mini compact is complete when:
1. No stale tool outputs remain
2. `tokens_percent < 30%`
3. Task tracking is updated
4. User can continue with their original request

<critical>
- Prioritize proactive context management
- Always batch truncation calls
- Keep task tracking updated
- Return to user task when healthy
</critical>