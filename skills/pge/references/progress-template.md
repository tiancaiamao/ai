# Progress Tracking Template

维护 `.pge/progress.md`，记录每个 task/phase 状态、commit hash、eval/review 结果。**压缩后此文件是恢复上下文的唯一依据。**

```markdown
## Phase: <name>
- [ ] Task 1: <name> — IN PROGRESS
- [x] Task 2: <name> — VALIDATED

## Validation Log

### Task 1: <name>
- Generator: gen-001 (alive)
- Evaluator: val-001 (killed)
- Eval: .pge/eval-task-1.md — FAIL (round 1) → PASS (round 2)
- Status: VALIDATED ✅

## Phase Review
- Report: .pge/review-phase-1.md — 0 P1 issues
- Commit: abc123
```

**Every task entry MUST include eval report path and verdict.**