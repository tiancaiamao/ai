# State Template

`state.md` 是**当前快照（snapshot）**——只保留最新状态，可覆写。与 `progress.md`（append-only 事件流）互补。

| 文件 | 性质 | 内容 |
|------|------|------|
| `state.md` | snapshot（可覆写） | 当前状态：完成了什么、关键决策、下一步 |
| `progress.md` | log（append-only） | 执行历史：谁做了什么、何时、结果如何 |

**为什么重要：** 当主 agent 的 context 被 compaction 压缩后，读 `state.md`（快速了解当前进度）+ `progress.md`（了解执行历史）恢复上下文。每个新 Generator 也读 `state.md` 来了解前序工作。

写入 `.pge/state.md`：

```markdown
# State

## Completed Tasks
- T001: Add JWT auth — done, files: src/auth/jwt.go, src/api/login.go
- T002: Add RBAC middleware — done, files: src/middleware/rbac.go

## Key Decisions
- Token in http-only cookie (not localStorage)
- Roles: admin, editor, viewer

## Known Issues
- Token refresh not yet implemented (T003)

## What's Next
- T003: Implement token refresh
```

## 更新规则

- **每个 task PASS 后立即更新** — 不要积累多个 task 再批量更新
- Completed Tasks 只记录已通过 eval 的 task
- Key Decisions 记录影响后续 task 的架构/设计决策
- Known Issues 记录遗留问题，帮助后续 Generator 避坑
- What's Next 指向下一个要执行的 task