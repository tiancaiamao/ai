---
name: plan
description: 读 design.md，产出 tasks.yml（含 plan-lint 验证）。用 worker-judge loop 保证质量。
---

# Plan

读 `design.md`，产出 `tasks.yml` — 一个结构化的任务拆解，可以被 implement skill 消费。

## When to Use

- design.md 已通过 gate（`wf approve`）
- 用户说 "拆解任务" / "plan this" / "写 plan"
- 作为 brainstorm 之后的下一步

## Input

- `design.md`（必需 — 来自 brainstorm skill）
- `.wf/state.json` 中 design phase 应该是 completed

## The Planning Process

### Step 1: Read Design

```bash
# 确认 design phase 已完成
wf status --json
```

读 `design.md`，理解方案。重点关注：
- "怎么做" 部分 — 接口定义、调用链、文件列表
- "现状" 部分 — 涉及哪些文件和数据结构
- "边界条件" — 容易遗漏的 corner cases

### Step 2: Generate tasks.yml

使用 planner persona（`prompts/planner.md`）产出结构化计划。

**tasks.yml structure:**

```yaml
version: "1"
metadata:
  spec_file: "design.md"
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
    description: "What this group achieves"
    tasks: ["T001", "T002"]
    commit_message: "feat(scope): description"

group_order: ["group-name"]

risks:
  - area: "Area"
    risk: "What could go wrong"
    mitigation: "How to prevent it"
```

### Step 3: Validate

```bash
# plan-lint 校验格式、依赖、循环
~/.ai/skills/plan/bin/plan-lint tasks.yml
```

lint 失败则修复 YAML 再跑，直到通过。

### Step 4: Review via Worker-Judge Loop

使用 planner + reviewer persona（最多 3 轮）：

```bash
~/.ai/skills/ag/patterns/pair.sh \
  "$(cat ~/.ai/skills/plan/prompts/planner.md)" \
  "$(cat ~/.ai/skills/plan/prompts/reviewer.md)" \
  design.md \
  3
```

Reviewer 检查：
- design.md 中所有方案点是否被 task 覆盖
- 依赖关系是否正确（无循环、无缺失 ID）
- group 顺序是否尊重依赖
- 是否有测试 task

### Step 5: Render (optional)

```bash
~/.ai/skills/plan/bin/plan-render tasks.yml > PLAN.md
```

### Step 6: Present & Gate

1. 向用户展示任务摘要（按 group）
2. 运行 `wf approve --message "<用户原话>"`
3. 运行 `wf advance --output tasks.yml`
4. 提示用户下一步：implement skill

## Task Granularity Rules

| Size | Hours | Action |
|------|-------|--------|
| Too big | > 6h | Break down |
| Just right | 2-4h | Keep |
| Too small | < 1h | Combine |

## Grouping Principles

按 **业务价值 / 用户故事** 分组，不是按技术层。
每个 group 应产出可工作的增量。

❌ Bad: "models group" → "services group" → "API group"
✅ Good: "registration flow" → "email verification" → "activation"

## Scope Adaptation

| Scope | Tasks | Strategy |
|-------|-------|----------|
| Small | 1-2 | implement 直接执行 |
| Medium | 3-6 | group by story, serial or light parallel |
| Large | 7+ | full fan-out with parallel workers per group |

## Tools

- `~/.ai/skills/plan/bin/plan-lint <tasks.yml>` — 校验 YAML 格式、依赖、循环
- `~/.ai/skills/plan/bin/plan-render <tasks.yml>` — 渲染为 markdown
- `prompts/planner.md` — planner persona
- `prompts/reviewer.md` — plan reviewer persona

## ⛔ MANDATORY — Self-Check

| 断言 | 触发条件 | 修正 |
|------|----------|------|
| design 未完成 | wf status 显示 design phase 不是 completed | 先完成 design + gate |
| lint 失败 | plan-lint 返回非 0 | 修复 YAML 直到通过 |
| 未 approve 就 advance | wf advance 返回错误 | 先展示给用户 + approve |
| 缺少依赖关系 | tasks 之间有隐式依赖但没声明 | 补全 dependencies |