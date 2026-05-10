---
name: implement
description: 代码驱动的任务执行。ag task run scheduler 自动驱动 task 执行、review 和 commit。
---

# Implement — Code-Driven Task Execution

**agent 只做填空，代码驱动流程。**

## User Contract

用户可以这样说：

- "开始实现"
- "继续 implement"
- "汇报进度"
- "先暂停"

## 运行时真相

**`ag task` 是唯一真相。** 任务状态、进度、依赖关系全部在 `ag task` 中查询。

状态机（代码强制，不可绕过）：

```
pending → claimed → running → done
                              ↘ failed → pending (retry ×3)
                  ↘ review → revision → review (max 2轮) → done
```

## Pre-Flight Checklist

**在启动 scheduler 之前，必须确认：**

```bash
# 1. 确认有 pending tasks
ag task ls
# 如果 "No tasks" → 提示用户先跑 plan skill

# 2. 确认 design.md 存在（可选，提供更好 context）
ls docs/design/*.md

# 3. 确认在正确的 git branch
git branch --show-current
# 不应该在 main 上
```

## Execution: `ag task run`

**Two modes: foreground (default) or background (`--detach`).**

### Background mode (recommended for long runs)

```bash
ag task run --detach \
  --design docs/design/xxx.md \
  --max-concurrent 2 \
  --timeout 600

# Monitor progress:
ag task log              # tail -f style, follow scheduler output
ag task log --tail 20    # snapshot last 20 lines
ag task ls               # see current task statuses
ag task stop             # stop the scheduler
```

**Foreground logs are also written to `.ag/scheduler.log`** — so `ag task log` works even in foreground mode.

### Foreground mode (for quick tasks / debugging)

```bash
ag task run \
  --design docs/design/xxx.md \
  --max-concurrent 2 \
  --timeout 600 \
  --poll 5000
```

Use foreground when you expect < 2 minutes total runtime. For anything longer, use `--detach`.

**Parameters:**
- `--detach`: Run in background, log to `.ag/scheduler.log`
- `--design`: Path to design.md for worker context
- `--max-concurrent`: Max parallel workers (default 2)
- `--timeout`: Seconds per task (default 600 = 10 min)
- `--poll`: Milliseconds between status checks (default 5000)
- `--skip-review`: Skip review phase (requires user approval)

**scheduler 自动处理：**
1. 选取 dependency-unblocked 的 pending tasks
2. Spawn worker agent（每个 task 一个，受 max-concurrent 限制）
3. 检测 agent 完成 → done / failed
4. Failed tasks 自动 retry（最多 3 次）
5. Group 全部 done → spawn reviewer agent
6. Review pass → commit
7. 所有 tasks done → 输出最终 summary

**主 agent 不需要手动 spawn/wait/review。** 这些全部由 scheduler 代码驱动。

## Progress Monitoring

scheduler 运行过程中实时输出进度：

```
🚀 Started T001: Add session lock
🚀 Started T002: Add retry mechanism
✅ Done T001: flock acquired in Load and Save
✅ Done T002: retry with exponential backoff
🔍 Reviewing group core
✅ Review passed for group core
✅ All tasks completed.
  ✅ T001: Add session lock
  ✅ T002: Add retry mechanism
```

主 agent 将这些进度转述给用户。

## Failure Handling

| 场景 | scheduler 行为 | 主 agent 行动 |
|------|---------------|--------------|
| 单个 task failed | 自动 retry（最多 3 次） | 汇报 retry 情况 |
| Retry 耗尽 | 保持 failed 状态 | **停下来向用户汇报** |
| Agent 超时 | 标记 failed + retry | 汇报超时 |
| Agent 崩溃 | 标记 failed + retry | 汇报崩溃 |
| 所有 tasks failed | scheduler 停止 | 汇报失败原因 |

**出现无法自动恢复的失败时，停下来向用户汇报，不要静默继续。**

## Manual Task Operations

如果需要手动干预（非 scheduler 模式）：

```bash
# 手动查看/操作单个 task
ag task show T001
ag task transition T001 running
ag task done T001 --summary "completed"
ag task fail T001 --error "reason"
ag task retry T001 --max-retries 3

# 查看 group 状态
ag task ls --status pending
ag task ls --status running
```

## Input

- `ag task` 队列（必需 — 来自 plan skill 的 `ag task import-plan`）

**关键约束：** Worker 的验证标准来自 task description 的 done-when，不是 worker 自己的判断。如果 worker 发现 done-when 标准不够，应该报告问题而不是自己发明新的验证标准。

## Tools

- `ag task run` — 启动 scheduler（核心命令）
- `ag task ls` — 查看任务列表
- `ag task show <id>` — 查看任务详情
- `ag task transition <id> <state>` — 手动状态转换
- `ag task retry <id>` — 手动重试

## ⛔ MANDATORY — Self-Check

| 断言 | 触发条件 | 修正 |
|------|----------|------|
| 跳过 preflight | 没跑 `ag task ls` 就启动 | 先确认 task queue |
| 手动 spawn agent | 用 `ag agent spawn` 而非 `ag task run` | 改用 `ag task run` |
| 手动标记 done | 用 `ag task done` 绕过 scheduler | 除非手动干预模式 |
| 跳过 review | `--skip-review` 但没有用户同意 | 默认不跳过 |
| 静默失败 | scheduler 报错但没向用户汇报 | 停下来汇报 |
| 在 main 上运行 | git branch 是 main | 切换到 feature branch |

## Post-Implementation Checklist

After `ag task run` completes (all tasks done), perform these cleanup steps:

```bash
# 1. Verify all tasks are done
ag task ls
# Should show all tasks in "done" state

# 2. Run full test suite
go test ./...
# If E2E tests exist:
go test -tags=e2e ./test/e2e/

# 3. Clean worker artifacts
rm -rf .ag/agents/worker-*

# 4. Clean stale lock files
find .ag/tasks -name '.claim-lock' -delete

# 5. Clean binary artifacts
rm -f ai3 ai3_bin

# 6. Show final status
ag task ls
git log --oneline -10

# 7. Ask user about next steps:
#    - Merge feature branch to main?
#    - Run comparison tests?
#    - Archive task files?
```

**Do not skip this checklist.** Worker artifacts, stale locks, and untested code are the most common sources of post-implementation issues.