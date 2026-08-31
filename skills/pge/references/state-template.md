# State Template

`state.md` 是 PGE 的**唯一状态文件**。每个 task PASS 后必须更新。

```markdown
# State

## Task Status

| Task | Status | Eval |
|------|--------|------|
| P1-T1: WitnessStorage wiring | ✅ PASS | eval-p1-t1.md |
| P1-T2: conf_state tests | ✅ PASS | eval-p1-t2.md |
| P1-T3: trait contract tests | ⏳ In Progress | |

## Next Task
P1-T3

## Key Decisions
- Use `cargo check` not `cargo test` (macOS linker issue)
- Generator injected witness_storage in both `start()` and `run_node()`

## Attempt Log
- P1-T3: tried direct SQL approach → migration conflict, reverted
- P1-T3: switched to ORM approach → eval PASS

## Known Issues
- macOS arm64 linker error affects all rfstore tests (pre-existing)

## Phase Log
- Phase 1: commit f800f329c — 5 tasks, all PASS, review clean
- Phase 2: commit e35e8952a — 5 tasks, all PASS, 1 P1 fix applied
```

## 字段说明

| 字段 | 更新时机 | 说明 |
|------|----------|------|
| Task Status | 每个 task PASS 后 | 表格行 edit，标记 ✅/⏳ + eval 文件名 |
| Next Task | 每个 task PASS 后 | 改为下一个 task 名（仅 task 名，不写散文） |
| Key Decisions | 有重要决策时 | 影响后续 task 的架构/设计决策 |
| Attempt Log | 有被放弃的方案时 | 记录尝试过但放弃的路径 + 原因。每行不超过 20 字。帮助后续 Generator 避免重复踩坑 |
| Known Issues | 发现问题时 | 遗留问题，帮后续 Generator 避坑 |
| Phase Log | 每个 phase commit 后 | 一行：commit hash + task 数 + review 结果 |

## 为什么 Next Task 只写 task 名

在之前的设计中，Next Task 是散文（"P1-T2: Add unit tests for..."），agent 每次 `edit` 表格行但**从不更新 Next Task**——因为改散文比重写表格行难。改为仅 task 名后，更新操作是一行精准替换。