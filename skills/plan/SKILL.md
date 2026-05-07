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

### Step 2: Generate tasks.yml

产出任务拆解。遵循下面的格式和粒度规则。

**⚠️ 关键：每个 task 的 `description` 必须是自包含的微 spec。** 一个没有读过 design.md 的 subagent 拿到这个 description 就能动手实现。

**tasks.yml 结构:**

```yaml
version: "1"
metadata:
  spec_file: "design.md"
  created_at: "2025-07-11"

tasks:
  - id: T001
    title: "Task title"
    description: |
      ## Goal
      One sentence: what this task achieves.

      ## Key changes
      - Specific change 1 (e.g., "Add flock() call in Load()")
      - Specific change 2

      ## Files
      - MODIFY: path/to/file.go
      - CREATE: path/to/new_file.go

      ## Design decision
      Why this approach over alternatives. Reference design.md if needed.

      ## Edge cases
      - Edge case 1 and how to handle it
      - Edge case 2 and how to handle it

      ## Done when
      - [ ] Testable criterion 1
      - [ ] Testable criterion 2
      - [ ] go build ./... passes
    group: group-name
    dependencies: []

groups:
  - name: group-name
    title: "Group Title"
    description: "What this group delivers as a working increment"
    tasks: [T001, T002]
    commit_message: "feat(scope): description"

group_order: [group-name]
risks:
  - area: "Area"
    risk: "What could go wrong"
    mitigation: "How to prevent it"
```

**description 必填段落：**

| 段落 | 用途 | 最低要求 |
|------|------|----------|
| `## Goal` | subagent 知道任务目标 | 一句话 |
| `## Key changes` | subagent 知道具体改什么 | ≥1 条 |
| `## Files` | subagent 不需要自己找文件 | ≥1 个文件 |
| `## Done when` | subagent 知道什么时候算完 | ≥1 条可验证标准 |

**description 可选但推荐段落：**

| 段落 | 什么时候需要 |
|------|-------------|
| `## Design decision` | 有多种实现方式时 |
| `## Edge cases` | 有明显边界条件时 |

### Step 3: Validate with plan-lint

```bash
~/.ai/skills/plan/bin/plan-lint tasks.yml
```

如果 lint 失败，修复 YAML 并重跑直到 clean。

### Step 4: Peer Review

plan-lint 通过后，spawn reviewer agent 做第二双眼睛检查。

**Review 聚焦点：task context 是否足够。** 这是 subagent 能否独立执行的关键。

```bash
# 准备 review 输入
cat > /tmp/plan-review-input.md << 'EOF'
Review this tasks.yml for two things:

1. **Task self-containedness**: Can a subagent who has NOT read design.md implement each task using ONLY the description field?
   - Does each task have Goal, Key changes, Files, Done when?
   - Are file paths concrete (not "the relevant file")?
   - Are Done-when criteria testable?

2. **Task boundary correctness**:
   - Can each task be completed without breaking compilation?
   - Are hidden dependencies declared?
   - Do groups produce compilable, runnable increments?

Read the tasks.yml at: <path>
Read the design doc at: <path> (for context only)

Write findings to /tmp/plan-review-result.json:
{"findings": [{"id": "T004", "issue": "...", "suggestion": "..."}], "pass": true/false}
EOF

ag agent spawn plan-reviewer \
  --input @/tmp/plan-review-input.md \
  --timeout 300

ag agent wait plan-reviewer --timeout 300
cat /tmp/plan-review-result.json
ag agent rm plan-reviewer
```

**处理 review 结果：**
- `"pass": true` → 继续 Step 5
- `"pass": false` → 根据 findings 修复 tasks.yml，重跑 plan-lint + review
- Reviewer agent 失败 → **停下来向用户汇报**，不要跳过 review

### Step 5: Self-Review Checklist

| 检查项 | 通过条件 |
|--------|----------|
| description 自包含 | 每个 task 的 description 包含 Goal/Key changes/Files/Done when 四段 |
| design 维度覆盖 | 现状、动机、决策、做法、边界 — 每个维度至少有一个 task |
| 依赖无环 | plan-lint 已验证；人工确认无缺失 ID |
| 粒度合适 | 所有 task 在 2-4h 范围（<1h 合并，>6h 拆分） |
| group 顺序 | 尊重依赖关系，前面的 group 不依赖后面的 |
| 可交付增量 | 每个 group 完成后代码可编译可运行 |

全部通过 → 继续。有遗漏 → 修复 tasks.yml 后重跑 plan-lint。

### Step 6: Import & Gate

1. **向用户展示摘要** — tasks 数量、group 列表、依赖链
2. 等待用户确认
3. 导入 ag task 队列：

```bash
ag task import-plan tasks.yml
```

4. 确认导入成功：`ag task ls`
5. 提示用户下一步：implement skill

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

## Output

- `tasks.yml` — plan 产出物，导入后不再更新状态
- `ag task` 队列 — 运行时唯一真相，implement skill 消费

## Skill Composition

```
brainstorm → design.md → plan (this skill) → ag task queue → implement
```

## Tools

- `~/.ai/skills/plan/bin/plan-lint <tasks.yml>` — 验证 tasks.yml 结构和依赖
- `ag task import-plan <tasks.yml>` — 导入任务到运行时队列
- `ag task ls` — 查看导入结果

## ⛔ MANDATORY — Self-Check

| 断言 | 触发条件 | 修正 |
|------|----------|------|
| 未读设计 | 未读 design.md 就开始拆解 | 先读 design.md |
| description 不自包含 | 缺少 Goal/Key changes/Files/Done when 任一段 | 补充完整 |
| lint 未通过 | plan-lint 报错 | 修复后再继续 |
| 跳过 peer review | 没有 spawn reviewer agent 就展示 | 先做 Step 4 |
| 未展示产出就问确认 | tasks.yml 存在但未向用户展示 | 先展示摘要 |
| 跳过 self-review | 没走 checklist 就展示 | 先走 checklist |
| 未导入就结束 | plan 完成但没有 import-plan | 先导入 ag task |