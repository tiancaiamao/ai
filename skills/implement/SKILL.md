---
name: implement
description: 对话式执行实现阶段。agent 在后台完成任务分发、实现、评审与收尾。
---

# Implement — Conversation Interface

面向用户的交互是自然语言，不是 shell 命令。

## User Contract

用户可以这样说：

- "开始实现这个 plan"
- "继续 implement 阶段"
- "汇报剩余任务和阻塞依赖"
- "先暂停，等我确认后再继续"
- "把失败任务重试一轮"

用户不需要手工运行 `ag` 或脚本。

---

## ⛔ MANDATORY — Pre-Flight Checklist

**在写任何代码之前，必须完成以下全部项目。缺少任何一项 = 违规。**

- [ ] **读 PLAN** — 读取 `tasks.yml`，理解所有任务和依赖关系
- [ ] **输出 Pre-Flight** — 向用户输出以下格式的确认：

```
📋 Implementation Pre-Flight

PLAN: tasks.yml
任务总数: N
执行模式: 直接执行 / subagent mode
选择理由: [为什么选这个模式？≥3 个任务必须用 subagent mode]
并行度: [同时执行的任务数，subagent mode 最多 2 个 worker]
预计轮次: [几轮完成]
```

- [ ] **等待用户确认** — Pre-Flight 输出后等待用户说 "ok" / "开始" / 确认

**设计理由：**
1. 强制显式决策 — 不能默认跳到"直接干"
2. 输出即承诺 — 公开声明的计划遵守度更高
3. 用户可纠偏 — 看到计划不对可以立即干预

---

## ⛔ MANDATORY — Per-Task Ritual

**每完成一个任务后，必须执行以下三步，然后才能开始下一个任务：**

### Step 1: 持久化进度

```bash
wf note "Task X/N done: [brief summary], tests: [pass/fail]"
```

这确保 session 中断后进度不丢失。

### Step 2: 向用户汇报

```
✅ X/N done — [task name]
   Tests: [pass/fail] | Lines changed: [delta]
🔄 Next: Task [X+1] — [task name]
```

### Step 3: Self-Check

在开始下一个任务之前，快速检查：

| 检查项 | 通过条件 |
|--------|----------|
| 项目编译 | 零错误（如 `go build ./...`） |
| 受影响的包测试通过 | 零失败（如 `go test ./affected/pkg/...`） |
| 没有遗漏 TODO | 代码中无临时 HACK/FIXME |

**只有全部通过才继续下一个任务。** 如果失败，修复后再汇报。

---

## Agent Contract

agent 必须在后台完成以下流程：

1. 读取 `tasks.yml`，检查可执行性。
2. **输出 Pre-Flight Checkpoint，等待用户确认。**
3. 选择执行模式：
   - 小任务（1-2 个，无依赖）→ **直接执行**：自己实现，不需要 subagent。仍须 per-task `wf note` 和 group review。
   - 中/大任务（≥3 个）→ **subagent mode**（依赖感知并行）。
4. 对每个任务执行：实现 → 评审
5. 评审失败时进行受限重试（最多 3 轮）。
6. 任务完成后执行 Per-Task Ritual（note + 汇报 + self-check）。
7. 每个 **group** 全部完成后，执行 Group Review-Commit Ritual（见下）。
8. 全部 group 完成后输出最终报告。

### 执行模式选择规则

| 任务数 | 推荐模式 | 理由 |
|--------|---------|------|
| 1-2 | 直接执行 | 开销不值得 |
| ≥3 | subagent mode（必须） | 并行调度、依赖感知、进度汇报 |

**⚠️ 硬规则：tasks ≥ 3 时，必须使用 subagent mode，没有例外。**

subagent mode 使用 `ag task` + `ag agent` 组合实现。

---

## Subagent Mode（内部执行细节）

使用 `ag task` + `ag agent` 组合实现依赖感知并行执行。

### 核心流程

```
1. ag task import-plan tasks.yml        # 导入任务 + 依赖关系
2. for each group in group_order:
     a. 波次循环:
        while group 还有未完成任务:
          i.   ag task list --status pending   # 查看可执行任务
          ii.  ag task next --claimant worker-N # 返回 task ID
          ii-a. ag task show <id>              # 获取 title + description
          iii. ag agent spawn worker-N --input "Task <id>: <title>\n<description>"
          iv.  ag agent wait worker-N --timeout 600
          v.   检查结果:
               - 成功 → ag task done <task-id> --summary "$(ag agent output worker-N)"
               - 失败 → 停下来向用户汇报，不要自行重试或降级
          vi.  ag agent rm worker-N
          vii. wf note "Task X/N done: [summary]"
          viii. 向用户汇报 task 进度
     b. group 所有 tasks 完成 → 执行 Group Review-Commit Ritual
     c. review loop 通过 → commit → wf note → 汇报 → 下一个 group
3. 全部 group 完成 → 生成最终报告
```

### 关键命令对照

| 目的 | 命令 |
|------|------|
| 导入计划 | `ag task import-plan tasks.yml` |
| 查看任务状态 | `ag task list` |
| 获取下一个可执行任务 | `ag task next --claimant worker-1` |
| 获取任务详情 | `ag task show T001` |
| 标记任务完成 | `ag task done T001 --summary "..."`
| 标记任务失败 | `ag task fail T001 --reason "..."`
| 重试失败任务 | `ag task retry T001` |

### 并发控制

subagent mode 最多同时 2 个 worker（含主 agent 共 3 个 agent）。

---

## ⛔ MANDATORY — Group Review-Commit Ritual

**一个 group 没有完成，直到它的 review loop 通过 + 已 commit。**

"做完所有任务" ≠ "group 完成"。正确的定义是：

```
group 完成 = 所有 tasks 实现完毕 + review loop PASS + 已 commit
```

### 执行流程

```
┌─ 所有 tasks 实现完毕
│
▼
┌─ Worker-Judge Review Loop (最多 3 轮) ──────────┐
│                                                   │
│  1. 生成 diff: git diff (相对于上一个 group commit) │
│  2. ag agent spawn reviewer                       │
│     --system @/Users/genius/.ai/skills/review/reviewer.md │
│     --input "Review diff: $DIFF_FILE"             │
│  3. ag agent wait reviewer --timeout 300          │
│  4. 读取 reviewer 输出                            │
│                                                   │
│  ┌─ 有 P0/P1 findings?                           │
│  │  YES → 修复所有 P0/P1 问题                     │
│  │        → 回到循环顶部，重新生成 diff + review   │
│  │  NO  → PASS，退出循环 ✅                       │
│  └──────────────────────────────────────────────┘
│                                                   │
└───────────────────────────────────────────────────┘
  │
  ▼
  git add -A && git commit -m "<group commit_message>"
  │
  ▼
  wf note "Group [name]: review PASS, committed [hash]"
  向用户汇报 group 完成状态
  │
  ▼
  继续下一个 group
```

### 具体命令

```bash
# Review agent
DIFF_FILE=$(mktemp)
git diff "$BASE_REF" > "$DIFF_FILE"

ag agent spawn reviewer-group-N \
  --system @/Users/genius/.ai/skills/review/reviewer.md \
  --input "Review以下 diff for group <name>. File: $DIFF_FILE"

ag agent wait reviewer-group-N --timeout 300
REVIEW_OUTPUT=$(ag agent output reviewer-group-N)
ag agent rm reviewer-group-N

# 检查是否有 P0/P1，有则修复后重进循环，无则 commit
```

### P2/P3 findings

不阻塞 group 完成。记录到最终报告的 follow-up 部分，在所有 group 完成后统一处理。

### Group 完成汇报格式

```
📦 Group [name] — COMPLETE
   Tasks: [X/Y done]
   Review: ✅ PASS (round N, 0 blocking / M P2 findings recorded)
   Commit: [hash] — [message]
🔄 Next group: [name] (or "All groups done")
```

---

## ⛔ MANDATORY — Self-Check Assertions

**以下条件任一为真时立即停下。不是建议，是硬约束。**

| 断言 | 触发条件 | 修正 |
|------|----------|------|
| Pre-Flight 未输出 | 开始写代码但没输出 Pre-Flight | 先输出 Pre-Flight |
| 进度未记录 | 完成任务但没调 `wf note` | 补 note |
| 未用 subagent | ≥3 任务但直接实现 | 切换 subagent mode |
| 跳过测试 | 任务完成但没跑受影响的测试 | 先跑测试 |
| 一口气做完 | 连续 ≥2 任务没汇报 | 立即汇报 |
| 静默降级 | ag 工具链出错但没汇报就降级 | 回退，汇报，等指示 |
| 跳过 review | group tasks 做完但没 review loop | 执行 review loop |
| 未 commit | review PASS 但没 commit | 先 commit |
| P0/P1 未修 | review 有 blocking 但没修 | 修复后重进 review loop |

---

## Conversation-First Rules

1. 不把 CLI 参数当作用户主交互层。
2. 不要求用户自己执行命令来推进任务。
3. 出现失败时，**停下来向用户汇报**，不要尝试自行恢复或静默降级。等待用户决定。
4. 仅当用户明确要求时，才展示底层命令与脚本细节。

## Input

- `tasks.yml`（必需 — 来自 plan skill）
- `.wf/state.json` 中 plan phase 应该是 completed

## Tools

- `ag task ...` — 任务队列管理
- `ag agent ...` — subagent 生命周期
- `wf note ...` — 进度持久化