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

## Standard Startup Sequence

按这个顺序执行，不要跳步：

```
1. ag doctor                    # 工具链检查，必须全绿
2. ag task ls                   # 确认有 pending tasks
3. ai serve --help              # 手动确认 ai serve 可用（doctor 不检查这个）
4. plan-lint tasks.md           # 单独跑一次，exit 0 才继续
6. ag task import-plan tasks.md # 导入
7. ag task run --detach --design docs/design/xxx.md --callback "ag agent prompt <main-id> 'scheduler done'"
8. 主 agent 继续工作或等待，scheduler 完成后自动回调
```

**为什么 plan-lint 要在 import 前单独跑：** plan-lint exit 0 for warnings / exit 1 for errors。如果 plan 有 error（控制字符、空字段），import 进去就是脏数据。

## Pre-Flight Checklist

**在启动 scheduler 之前，必须确认：**

```bash
# 0. 工具链健康检查（新增！）
ag doctor
# 16 checks: storage, state machine, deps, cleanup, ai binary, git
# 任何 ❌ 都必须先修复才能继续

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
  --timeout 600 \
  --callback "ag agent prompt <main-id> 'scheduler done'"
```

**Callback 机制（推荐）：** `--callback` 参数指定 scheduler 全部完成后的回调命令。
scheduler 完成后自动执行该命令，主 agent 被 `prompt` 唤醒，无需轮询。

如果不支持 `--callback`，退化为手动监控：

```bash
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
- `ag task wait [timeout]` — 阻塞等待所有任务完成（替代重复轮询 `ag task ls`）
- `ag task ls` — 查看任务列表
- `ag task show <id>` — 查看任务详情
- `ag task transition <id> <state>` — 手动状态转换
- `ag task retry <id>` — 手动重试

## Monitoring Pattern

**❌ 不要重复轮询：**
```bash
# BAD — 会触发 loop guard
sleep 25 && ag task ls
sleep 25 && ag task ls
sleep 25 && ag task ls
```

**✅ 用 tmux + wait：**
```bash
# 1. scheduler 在 tmux 中运行
tmux new -s scheduler -d -c /path/to/worktree "ag task run --design docs/design.md --timeout 1200"

# 2. 阻塞等待所有任务完成（可设超时）
ag task wait 3600

# 3. 检查结果
ag task ls
git log --oneline -10
```

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

## Known Pitfalls

从实战中踩过的坑。如果重来一遍，这些是最容易翻车的地方：

### 1. `ai serve` 不可用
scheduler 依赖 `ai serve` spawn worker。`ag doctor` 检查 `ai` binary 但不测 `ai serve`。
**修复：** 启动前手动 `ai serve --help`，或在 Pre-Flight 阶段加一步测试。

### 2. 忘传 `--design`
worker 没有 design.md 做参考，实现会偏离设计意图。
**后果：** worker 按"自己的理解"实现，review 阶段才发现偏差，大量返工。
**修复：** `ag task run --design docs/design/xxx.md` 是必填参数，不要省略。

### 3. plan 有隐藏控制字符
从 plan skill 生成的 YAML 可能包含 `\x01`（regex 损坏残留）或其它控制字符。
plan-lint 现在会检测这些，但必须在 import 前跑。
**修复：** `plan-lint tasks.md && ag task import-plan tasks.md` 作为固定流水线。

### 4. cleanup 删除 task 后 show 报错
`ag task cleanup` 会删除 done/failed 的 task。之后 `ag task show t001` 报 `task not found`。
**修复：** cleanup 放在所有检查之后，不要在调试中途跑。

### 5. Worker 的 done-when 标准模糊
task description 里的 done-when 如果写得模糊，worker 会按自己的标准完成。
**修复：** 在 plan 阶段用 reviewer 检查 done-when 是否具体、可验证。

### 6. Worker 超时但实际已完成（"成功的失败"）
**症状：** scheduler 标记 task failed ("timed out after 18m")，但 git log 显示 worker 已经 commit 了代码，测试也通过。
**根因：** scheduler 的 `checkRunning` 函数里，超时检查在完成检查（`checkAIServeRun`）之前执行。Worker 已经完成工作（events.jsonl 里有 `agent_end`），但 poll 时先命中超时分支，直接 Fail，`checkAIServeRun` 永远没机会运行。
**已修复（ag v2）：** 调整 `checkRunning` 的检查顺序——先检查 events.jsonl 的 `agent_end`（完成检测），再检查超时。超时作为兜底，并且加了 `hasWorkerCommit()` 二次兜底检查 git commit。
**检测方法（如仍遇到）：**
```bash
# 1. 看 failed task 的 git log（检查是否有 worker commit）
git log --oneline -5

# 2. 检查 worker 的 events.jsonl 是否有 agent_end
cat ~/.ai/runs/<runID>/events.jsonl | python3 -c "
import sys,json
for l in sys.stdin:
    d=json.loads(l.strip())
    if d.get('type')=='agent_end': print('FOUND agent_end'); break
else: print('NO agent_end')
"

# 3. 验证代码质量
go build ./... && go test ./affected/... -v
```

### 7. Worker 分支残留
Worker 在执行时会 `git checkout -b refactor/T010-xxx`。如果 task failed，分支还在。
**后果：** 下一个 task 的 worker 可能从错误分支开始，或者 merge 冲突。
**修复：** failed task 处理完后，检查 `git branch` 清理残留分支。手动 merge 或 rebase 到正确的 base branch。

### 8. Worker 修改未 Commit
Worker 做了代码修改但没有 git commit。Reviewer pass 后也没有 commit。
**后果：** 所有 worker 的改动堆积在工作区（unstaged/uncommitted），任务完成后代码丢失风险高，且无法追踪每个 task 的变更。
**根因：** `spawnReviewer` 在 REVIEW_PASS 后直接 `Done()`，没有 `git add + commit`。
**已修复（ag v2）：** 添加 `commitChanges()` 函数，在 reviewer pass 后自动 `git add -A && git commit`。Review fail 时也 best-effort commit，避免工作丢失。
**手动补救（如已发生）：** 在 worktree 中 `git add -A && git commit -m "refactor: worker changes batch"`。

## Health Signals

scheduler 运行过程中，通过这些信号判断是否正常：

| 信号 | 正常 | 异常 |
|------|------|------|
| `ag task ls` ELAPSED 列 | 在涨（如 `12s`, `35s`） | 空白或不变 |
| `ag task log` 输出 | 有 `🚀 Started` / `✅ Done` | 无输出超过 30s |
| `ag task stop` heartbeat | `heartbeat: 3s ago (alive)` | `heartbeat: 2m old (may be dead)` |
| 熔断器 | 未触发 | `⛔ Circuit breaker: 3 consecutive failures` |
| `ag task ls` 状态 | pending → claimed → running → done | 大量 failed |

**异常处理流程：**
1. `ag task stop` 停 scheduler
2. `ag task log --tail 50` 看最后输出
3. `ag task show <failed-id>` 看错误详情（现在包含 worker 最后 300 字符输出）
4. 修复问题后 `ag task retry <id>`
5. 重新 `ag task run --detach`