# Progress Tracking Template

维护 `.pge/progress.md`（append-only 事件流）。与 `state.md`（snapshot）互补——`progress.md` 记录执行历史，`state.md` 记录当前快照。compaction 后两者共同恢复上下文。

```markdown
## Phase: <name>
- [ ] Task 1: <name> — IN PROGRESS
- [x] Task 2: <name> — VALIDATED

## Validation Log

### Task 1: <name>
- Generator: gen-001 (alive)
- Evaluator: val-001 (killed)
- Eval: .pge/eval-{task1-name}.md — FAIL (round 1) → PASS (round 2)
- Status: VALIDATED ✅

## Phase Review
- Report: .pge/review-phase-1.md — 0 P1 issues
- Commit: abc123
```

**Every task entry MUST include eval report path and verdict.**