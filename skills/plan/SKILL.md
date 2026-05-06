---
name: plan
description: 读取 design.md，产出 tasks.yml（含 plan-lint 验证）。任务拆解按 2-4 小时粒度，支持依赖关系和分组。
---

# Plan

读取 `design.md`，产出 `tasks.yml`。

## When to Use

- `design.md` 已完成并经用户确认
- 用户说 "拆解任务" / "写 plan"
- 作为 design → implement 的中间环节

## Input

- `design.md`（required）
- `CONTEXT.md`（可选，来自 explore 阶段）

## The Planning Process

### Step 1: Read Inputs

读 `design.md`。如果 `CONTEXT.md` 存在，也读取。不要重新 explore。

### Step 2: Generate tasks.yml

使用 planner persona 产出任务拆解。

**tasks.yml 结构:**

```yaml
version: "1"
metadata:
  spec_file: "design.md"  # 注意：字段名是 spec_file，值写 design.md
  created_at: "2026-05-06"

tasks:
  - id: T001
    title: "Task title"
    description: "What to do (actionable, specific)"
    priority: high|medium|low
    estimated_hours: 2
    dependencies: []
    file: "path/to/target.go"
    done: false

groups:
  - name: group-name
    title: "Group Title"
    tasks: [T001, T002]
    commit_message: "feat(scope): description"

group_order: [group-name]
risks:
  - area: "Area"
    risk: "What could go wrong"
    mitigation: "How to prevent it"
```

注意：`metadata.spec_file` 是 plan-lint 要求的字段名（历史遗留），值填写实际的 design.md 路径。

### Step 3: Validate with plan-lint

```bash
~/.ai/skills/plan/bin/plan-lint tasks.yml
```

如果 lint 失败，修复 YAML 并重跑直到 clean。

### Step 4: Review via Worker-Judge Loop

使用 `pair.sh`（最多 3 轮）：

```bash
~/.ai/skills/ag/patterns/pair.sh \
  "$(cat ~/.ai/skills/plan/prompts/planner.md)" \
  "$(cat ~/.ai/skills/plan/prompts/reviewer.md)" \
  design.md \
  3
```

Reviewer 检查：
- design.md 的 5 个内容维度都被任务覆盖
- 依赖正确（无循环、无缺失 ID）
- group 顺序尊重依赖
- 包含测试任务

### Step 5: Render (可选)

```bash
~/.ai/skills/plan/bin/plan-render tasks.yml > PLAN.md
```

### Step 6: Gate

1. 向用户展示 tasks.yml 摘要
2. 运行 `wf approve --message "<用户原话>"`
3. 运行 `wf advance --output tasks.yml`
4. 提示用户下一步：implement skill

## Task Granularity Rules

| Size | Hours | Action |
|------|-------|--------|
| Too big | > 6h | Break down further |
| Just right | 2-4h | Keep as is |
| Too small | < 1h | Combine with related work |

## Grouping Principles

按 **user story / 业务价值** 分组，不是按技术层。每个 group 应产生可工作的增量。

❌ Bad: "models group" → "services group" → "API group"
✅ Good: "registration flow" → "email verification" → "activation"

## Scope Adaptation

| Scope | Tasks | Strategy |
|-------|-------|----------|
| Small | 1-2 | Agent executes directly |
| Medium | 3-6 | Group by story, serial or light parallel |
| Large | 7+ | Full fan-out with parallel workers per group |

## Output

- `tasks.yml` — 机器可读的任务列表
- `PLAN.md` — 人类可读的渲染版本（可选）

## Skill Composition

```
design → plan (this skill) → implement
    or
direct design → plan → implement
```

## Tools

- `~/.ai/skills/plan/bin/plan-lint <tasks.yml>` — 验证 tasks.yml
- `~/.ai/skills/plan/bin/plan-render <tasks.yml>` — 渲染 tasks.yml → PLAN.md
- `~/.ai/skills/plan/prompts/planner.md` — planner persona
- `~/.ai/skills/plan/prompts/reviewer.md` — plan reviewer persona

## ⛔ MANDATORY — Self-Check

| 断言 | 触发条件 | 修正 |
|------|----------|------|
| 未读设计 | 未读 design.md 就开始拆解 | 先读 design.md |
| lint 未通过 | plan-lint 报错 | 修复后再继续 |
| 未 approve 就 advance | `wf advance` 返回错误 | 先 `wf approve --message "用户原话"` |
| self-approve | `--message` 不是用户说的话 | 等用户确认 |
| 未展示产出就问确认 | tasks.yml 存在但未向用户展示 | 先展示摘要 |