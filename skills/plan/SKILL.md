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

plan 的核心是 **worker-judge loop**（ag patterns 中的 pair pattern）：
- **Worker**（planner subagent）：读 design.md，产出 tasks.yml
- **Judge**（reviewer subagent）：读 tasks.yml，验证自包含性

最多 3 轮。每轮：planner 生成 → reviewer 审查 → APPROVED 则退出，否则把 feedback 喂给 planner 重来。

```bash
DESIGN_MD="$(pwd)/design.md"
TASKS_YML="$(pwd)/tasks.yml"
REVIEW_JSON="/tmp/plan-review-result.json"

for ROUND in 1 2 3; do
  echo "=== Plan worker-judge round $ROUND ==="

  # --- Spawn planner (worker) ---
  PLANNER_INPUT="Read the design document at ${DESIGN_MD} and produce a tasks.yml plan."
  if [ "$ROUND" -gt 1 ] && [ -f "$REVIEW_JSON" ]; then
    PLANNER_INPUT="${PLANNER_INPUT} Address these review findings: $(cat $REVIEW_JSON)"
  fi
  PLANNER_INPUT="${PLANNER_INPUT} Write the output to ${TASKS_YML}."

  ag agent spawn "plan-w-${ROUND}" \
    --system @/Users/genius/.ai/skills/plan/prompts/planner.md \
    --input "${PLANNER_INPUT}"

  ag agent wait "plan-w-${ROUND}" --timeout 300
  ag agent rm "plan-w-${ROUND}"

  if [ ! -f "${TASKS_YML}" ]; then
    echo "❌ Planner did not produce tasks.yml in round $ROUND"
    continue
  fi

  # --- Spawn reviewer (judge) ---
  ag agent spawn "plan-j-${ROUND}" \
    --system @/Users/genius/.ai/skills/plan/prompts/reviewer.md \
    --input "Review the plan at ${TASKS_YML}. Reference design doc at ${DESIGN_MD} for coverage check only. Write JSON result to ${REVIEW_JSON}."

  ag agent wait "plan-j-${ROUND}" --timeout 300
  ag agent rm "plan-j-${ROUND}"

  # --- Check verdict ---
  if grep -q '"APPROVED"' "$REVIEW_JSON" 2>/dev/null; then
    echo "✅ Plan approved in round $ROUND"
    break
  fi

  echo "❌ Round $ROUND not approved"
  if [ "$ROUND" -eq 3 ]; then
    echo "Max rounds reached. Review findings:"
    cat "$REVIEW_JSON"
    echo "停下来向用户汇报 reviewer findings。"
  fi
done
```

**处理 loop 结果：**
- APPROVED → tasks.yml 已就绪，跑 plan-lint 后继续 Step 3
- 3 轮都未通过 → **停下来向用户汇报**，展示 reviewer findings，让用户决定
- Subagent 执行失败 → **停下来向用户汇报**，不要跳过

```bash
# Lint 是最后的安全网
if [ ! -f ~/.ai/skills/plan/bin/plan-lint ]; then
  echo "Building plan-lint..."
  (cd ~/.ai/skills/plan/cmd/plan-lint && go build -o ../../bin/plan-lint .)
fi
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

## Auto-Build

The `bin/plan-lint` binary is **not committed to git**. It must be built from source before first use.

**Build command:**
```bash
cd ~/.ai/skills/plan/cmd/plan-lint && go build -o ../../bin/plan-lint .
```

**Auto-build rule:** Before running `plan-lint`, always check if the binary exists. If not, build it automatically:
```bash
if [ ! -f ~/.ai/skills/plan/bin/plan-lint ]; then
  echo "Building plan-lint..."
  cd ~/.ai/skills/plan/cmd/plan-lint && go build -o ../../bin/plan-lint .
fi
```

Source code lives at `cmd/plan-lint/main.go`. The `bin/` directory is git-ignored.

## Tools

- `~/.ai/skills/plan/bin/plan-lint <tasks.yml>` — 验证 tasks.yml 结构和依赖（auto-built from `cmd/plan-lint/`）
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

**Reviewer 的 Must Pass 标准（任何一项失败 = 打回重做）：**
1. **Self-Containedness** — task description 自足，不需要外部文件
2. **Dependency Correctness** — 无循环、无悬空依赖
3. **Coverage** — design.md 每个关键决策和 P0 feature 都有 task 覆盖
4. **Acceptance Completeness** — design.md 每个 Acceptance Scenario 都有 task 的 done-when 覆盖
5. **YAML Structure** — 格式正确、字段完整