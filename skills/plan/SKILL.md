---
name: plan
description: 读取 design.md，产出 tasks.yml（含 plan-lint 验证），导入 ag task 队列。
---

# Plan

读取 `design.md`，产出 `tasks.yml`，验证后导入 `ag task` 队列。

## When to Use

- `design.md` 已完成并经用户确认
- 用户说 "拆解任务" / "写 plan"
- 作为 brainstorm → plan → implement 流程的中间环节

## Input

- `design.md`（required — 来自 brainstorm skill）
- `CONTEXT.md`（可选，来自 explore 阶段）

## The Planning Process

### Step 1: Read Inputs

读 `design.md`。如果 `CONTEXT.md` 存在，也读取。不要重新 explore。

确认 design.md 存在且内容合理（有现状、动机、决策、做法等维度）。如果 design.md 不存在或明显不完整，停下来向用户汇报。

### Step 2: Worker-Judge Loop (Planner + Reviewer)

plan 的核心是 **worker-judge loop**：
- **Worker**（planner）：读 design.md，产出 tasks.yml
- **Judge**（reviewer）：读 tasks.yml + design.md（仅作覆盖率参考），验证自包含性

使用 `ag` 的 pair.sh pattern，最多 3 轮。每轮：
1. planner 根据 design.md（+ 上轮 judge 反馈）生成 tasks.yml
2. plan-lint 验证 YAML 结构
3. reviewer 验证 task description 自包含性
4. 通过则退出循环，否则把 judge 反馈喂给 planner 重来

```bash
# 准备 input 文件
DESIGN_MD="$(pwd)/design.md"
TASKS_YML="$(pwd)/tasks.yml"
CONTEXT_MD="$(pwd)/CONTEXT.md"

cat > /tmp/plan-input.md << EOF
Read the design document at ${DESIGN_MD} and produce a tasks.yml plan.
Write the output to ${TASKS_YML}.
EOF

if [ -f "${CONTEXT_MD}" ]; then
  echo "Also read ${CONTEXT_MD} for codebase context." >> /tmp/plan-input.md
fi

# 使用 pair.sh 运行 worker-judge loop
~/.ai/skills/ag/patterns/pair.sh \
  /Users/genius/.ai/skills/plan/prompts/planner.md \
  /Users/genius/.ai/skills/plan/prompts/reviewer.md \
  /tmp/plan-input.md \
  3
```

**pair.sh 执行流程：**

```
Round 1:
  planner → tasks.yml → plan-lint → reviewer → APPROVED? ──→ ✅ 退出
                                                  ↓ REJECTED
Round 2:
  planner (带上 reviewer 反馈) → tasks.yml → plan-lint → reviewer → APPROVED? ──→ ✅ 退出
                                                                          ↓ REJECTED
Round 3 (final):
  planner (带上 reviewer 反馈) → tasks.yml → plan-lint → reviewer → APPROVED? ──→ ✅ 退出
                                                                          ↓ REJECTED
                                                                ❌ 报告给用户
```

**处理 loop 结果：**
- `exit 0`（APPROVED）→ tasks.yml 已就绪，继续 Step 3
- `exit 1`（max rounds reached）→ 停下来向用户汇报，展示 reviewer 的 findings，让用户决定是否手动修复
- pair.sh 执行失败 → **停下来向用户汇报**，不要跳过

**关于 plan-lint**：pair.sh 的 pair pattern 不内置 lint 步骤。如果 reviewer 通过但 plan-lint 失败，手动修 lint 后重跑：

```bash
~/.ai/skills/plan/bin/plan-lint tasks.yml
```

### Step 3: Import & Gate

1. **向用户展示摘要** — tasks 数量、group 列表、依赖链
2. 等待用户确认
3. 导入 ag task 队列：

```bash
ag task import-plan tasks.yml
```

4. 确认导入成功：`ag task ls`
5. 提示用户下一步：implement skill

| Size | Hours | Action |
|------|-------|--------|
| Too big | > 6h | Break down further |
| Just right | 2-4h | Keep as is |
| Too small | < 1h | Combine with related work |

## Grouping Principles

按 **user story / 业务价值** 分组，不是按技术层。每个 group 应产生可工作的增量。

❌ Bad: "models group" → "services group" → "API group"
✅ Good: "registration flow" → "email verification" → "activation"

## Output

- `tasks.yml` — plan 产出物，导入后不再更新状态
- `ag task` 队列 — 运行时唯一真相，implement skill 消费

## Skill Composition

```
brainstorm → design.md → plan (this skill) → ag task queue → implement
                               │
                               └─ Step 2: worker-judge loop (pair.sh)
                                    ├─ Worker: planner.md → reads design.md, writes tasks.yml
                                    └─ Judge: reviewer.md → validates self-containedness
                                         ↻ up to 3 rounds
```

## Tools

- `~/.ai/skills/plan/bin/plan-lint <tasks.yml>` — 验证 tasks.yml 结构和依赖
- `ag task import-plan <tasks.yml>` — 导入任务到运行时队列
- `ag task ls` — 查看导入结果

## ⛔ MANDATORY — Self-Check

| 断言 | 触发条件 | 修正 |
|------|----------|------|
| 未读设计 | 未读 design.md 就开始拆解 | 先读 design.md |
| worker-judge loop 失败 | pair.sh exit 1 或执行异常 | 停下来向用户汇报 |
| lint 未通过 | plan-lint 报错 | 修复后再继续 |
| 未展示产出就问确认 | tasks.yml 存在但未向用户展示 | 先展示摘要 |
| 未导入就结束 | plan 完成但没有 import-plan | 先导入 ag task |