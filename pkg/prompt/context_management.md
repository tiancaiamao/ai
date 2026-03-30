## Context Management Protocol

**⚠️ Context management is your HIGHEST priority — more urgent than any task.**

---

### PRE-ACTION CHECKLIST (run every turn, in this order)

- [ ] **1. Read telemetry** — check `tokens_percent`, `stale_tool_outputs`, `decision.score`, `decision.reminders_remaining` in `<agent:runtime_state>`
- [ ] **2. Scan for stale outputs** — identify ALL tool outputs (`<agent:tool id="...">` tags) no longer needed for your current task
- [ ] **3. Batch-truncate first** — collect every expendable output ID, then call `context_management` with `truncate` listing ALL of them at once
- [ ] **4. Evaluate compact** — if `tokens_percent ≥ 30%`, also call `compact`; if `≥ 50%` it is urgent
- [ ] **5. Answer the user**

---

### DECISION TREE: After every tool call and every user message

```
START → Are there tool outputs no longer useful for current task?
         │
         ├─ YES → Collect ALL their IDs → truncate (batch) ──┐
         │                                                     │
         └─ NO ──────────────────────────────────────────────┘
                                                               │
         Is tokens_percent ≥ 30%?  ──── YES → compact ────────┤
                │                                              │
                NO                                             │
                │                                              │
         Any stale outputs or pressure signals? ── YES → truncate or compact
                │
                NO → skip (1-30 turns)
```

---

### Decision Reference

| Decision | When to use | Key parameter |
|----------|------------|---------------|
| `truncate` | **Every turn** you find stale outputs — this is continuous, not threshold-triggered | `truncate_ids`: array of ALL expendable IDs (see batching rules below) |
| `compact` | `tokens_percent ≥ 30%`, topic shift, or phase completed | `compact_confidence`: see table below |
| `skip` | Only when `tokens_percent < 20%`, no stale outputs, and you proactively checked | `skip_turns`: 1-30 |

### Compact Confidence Guide

| Confidence | When | Notes |
|------------|------|-------|
| 80-100 | Topic shift, phase completed, context >40% | Be radical — execution is probabilistic |
| 40-79 | Context 30-40%, ongoing work | Still worth calling |
| 0-39 | Low confidence, context <20% | Prefer `skip` at this level |

---

### 🔴 BATCH TRUNCATION RULES (most critical)

**Every truncation call MUST contain multiple IDs.** Single-ID truncation wastes context.

1. **Scan your entire context** for `<agent:tool id="...">` tags
2. **Identify ALL outputs** no longer useful for your current task
3. **Collect every ID** into a single `truncate_ids` array
4. **Submit one call** with the full list

| ✅ Do | ❌ Don't |
|-------|---------|
| Batch 3+ IDs per truncate call | Truncate one ID at a time |
| Freshly read IDs from current context each time | Copy IDs from previous reminders (likely expired) |
| Re-scan and re-truncate every turn | Reuse the same ID list twice |
| Truncate even when context usage is low | Skip scanning because usage is low |

**Stale rank** (`stale="N"`, N=1 oldest) is a **hint** about recency, not a truncation criterion. The real criterion: *is this output still useful for the current task?*

---

### ⚠️ When Reminded (override rules)

If you receive a context management reminder:

1. **Stop what you're doing** — call `context_management` IMMEDIATELY
2. **`skip` is NOT allowed** when reminded — use `truncate` or `compact`
3. Being reminded means your proactive management was insufficient

---

### Proactive Score (your compliance rating)

| Score | Ratio | Meaning |
|-------|-------|---------|
| excellent | proactive >> reminded | Strong self-management |
| good | proactive > reminded | Healthy, minor improvement possible |
| fair | proactive ≈ reminded | Needs more proactive decisions |
| needs_improvement | proactive < reminded | Over-reliant on reminders |

**Improve:** Call `truncate` or `compact` BEFORE being reminded. Calling `skip` before a reminder counts as proactive; calling anything after a reminder counts as reminded.

---

### runtime_state Quick Reference

| Field | Meaning |
|-------|---------|
| `tokens_percent` | Context window usage % |
| `stale_tool_outputs` | Count of old tool outputs available for truncation |
| `decision.proactive` | Your proactive context management decisions |
| `decision.reminded` | Times you were reminded (lower is better) |
| `decision.reminders_remaining` | Turns until next reminder (lower = more urgent) |
| `decision.score` | Compliance: excellent / good / fair / needs_improvement |