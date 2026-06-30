---
name: pge
description: Planner-Generator-Evaluator 编排模式。GAN 启发的多 agent 动态编排，通过 ai CLI 控制子 agent 完成复杂任务的拆解-执行-验证闭环。
---

# PGE — Planner-Generator-Evaluator

将 AI 编码拆为三个独立角色，实现动态的任务拆解-执行-验证闭环。

## When to Use

- 复杂功能实现（多文件、多模块、有验收标准）
- 用户说 "用 PGE 模式" / "pge" / "编排模式"
- 任务需要验证闭环（实现 → 验证 → 修复循环）

**不要用于：** 简单 bug 修复、单文件改动、快速问答

## Core Theory

1. **Self-evaluation bias** — Agent 审查自己的代码会自我偏见。Generator 和 Evaluator **必须分离为独立 agent**
2. **Context anxiety** — 上下文接近窗口限制时 Agent 会提前收摊。解法是 hard reset + 结构化 handoff 文件，而非 compaction
3. **Structured feedback** — Evaluator 应输出结构化反馈（每条标准的 ✅/❌ + 具体证据），而非简单的 pass/fail
4. **Progressive disclosure** — Agent 从小入口（spec.md）开始，按需深入代码
5. **Context firewall** — 每个 subagent 独立上下文窗口，隔离中间噪声
6. **2-3 轮收敛** — 正常 2-3 轮循环即可收敛。超过 3 轮说明 spec 有问题，应暂停报告用户

## Three Roles

```
┌─────────────────────────────────────────────────┐
│                  Orchestrator                    │
│  (Planner — 你，当前 agent)                       │
│                                                  │
│  • 分析需求，写 spec.md                           │
│  • 拆解任务，调度 Generator                       │
│  • 解读 Evaluator 反馈，决定下一步                 │
│  • 永不写实现代码                                 │
└──────────┬──────────────────┬────────────────────┘
           │ task             │ evaluation request
           ▼                  ▼
┌──────────────┐    ┌──────────────┐
│  Generator   │    │  Evaluator   │
│  (子 agent)  │    │  (子 agent)  │
│              │    │              │
│  • 读 spec   │    │  • 读 spec   │
│  • 实现功能   │    │  • 读代码    │
│  • 写代码     │    │  • 对照标准  │
│              │    │  • 写 eval   │
│              │    │    report    │
└──────────────┘    └──────────────┘
```

**Generator 和 Evaluator 是独立 agent，不共享上下文。** 这是质量保证的关键。

**You ARE the Orchestrator.** No need to spawn yourself.

## Prerequisite

- **`subagent`** — 子 agent 完整生命周期（spawn → watch → cleanup）

**⚠️ MUST：** 在执行任何子 agent 操作前，确认 `subagent` 技能已加载。如未加载，先 `find_skill(name="subagent", load=true)`。PGE 不重复定义 spawn/watch/kill 流程。

## Execution Flow

### Phase 1: Spec Alignment

1. **Understand** — 和用户讨论需求
2. **Write spec** — 写入 `.pge/spec.md`（模板见 [`references/spec-template.md`](references/spec-template.md)）
3. **Spec Quality Gate** — 每个 acceptance criterion 必须有可执行的 Verify 命令
4. **Get user confirmation** — 展示 spec，等用户说 ok

### Phase 2: Task Decomposition

分析 spec，拆解成可执行的 task。写入 `.pge/tasks/task-{name}.md`。

**Task 拆解规则：**
- 每个任务 80-500 行（<80 合并，>500 拆分）
- 任务之间不共享文件（共享则改为串行）
- 给 WHAT（outcome），不给 HOW（实现），但包含足够上下文让 Generator 独立工作

### Phase 3: Generate, Evaluate, Iterate (Worker-Judge Loop)

每个 task 执行以下循环：

```
1. 写任务文件 → 用 --input-file 传给 Generator (coder role)
2. Watch Generator → 等待 DONE/BLOCKED
3. Generator 完成 → 不要 kill，保持活着
4. Spawn Evaluator (validator role) → 独立读代码、跑验证命令
5. Evaluator 写报告 → .pge/eval-{task}.md

   ┌── PASS ──→ Kill Generator + Evaluator → 下一个 task
   │
   └── FAIL ──→ ai send eval feedback 给同一个 Generator
                 Generator 修复 → spawn 新 Evaluator → 回到步骤 4
                 ↑
                 └── 最多 3 轮，仍 FAIL → 停下来报告用户
```

**为什么 FAIL 后 `ai send` 给同一个 Generator？**
- 同一 agent 有完整上下文（已经读了所有相关源文件）
- 只需要处理 Evaluator 发现的具体问题，不需要重新构建上下文
- 只有 task 真正 PASS 了才 kill Generator

**门禁规则：**
- Orchestrator 不得创建 eval report — 只有 Evaluator 可以写
- `.pge/eval-{task}.md` 不存在 = task 未完成 = 不能进入下一个 task

**Eval Report 格式：** 见 [`references/eval-report-template.md`](references/eval-report-template.md)

**One task at a time.** 不要在 Task 1 通过前启动 Task 2。

### Phase 4: Phase Review & Commit

1. **Record start commit** — `git rev-parse HEAD > .pge/phase-start-commit`
2. **Spawn Review agent**（`coder` role）— 审查整个 phase 的代码变更（`git diff <start_commit>..HEAD`）
3. **Review agent 写** `.pge/review-phase{N}.md` — 包含发现的问题（P0-P3）
4. **Orchestrator 读 review report**：
   - **无 P1**: 可以 commit
   - **有 P1**: 写修复任务 → spawn Generator 修复 → spawn Evaluator 验证 → 回到 Review
   - **P2/P3**: 记录在 progress.md，不阻塞 commit
5. **Commit** — 前提：所有 eval report PASS + review 无 P1
6. **Cleanup all subagents** — 检查 spawn 列表，逐个 cleanup

## Progress Tracking

维护 `.pge/progress.md`，记录每个 task/phase 状态、commit hash、eval/review 结果。**压缩后此文件是恢复上下文的唯一依据。**（模板见 [`references/progress-template.md`](references/progress-template.md)）

## ⛔ Anti-Patterns（必读）

| 反模式 | 症状 | 正确做法 |
|--------|------|----------|
| 无 spec 就开始 | 没有 .pge/spec.md 就执行 | 先写 spec，等用户确认 |
| Generator task 太模糊 | 任务描述 < 2 句话 | 加更多上下文 |
| 跳过 evaluation | 任务完成但无 eval report | spawn Evaluator，等 `.pge/eval-{task}.md` |
| 自测判定通过 | 自己跑测试宣布 PASS | PASS/FAIL 必须由独立 Evaluator 判定 |
| Orchestrator 创建 eval report | `write .pge/eval-*.md` | 只有 Evaluator 可以写 eval report |
| 跳过 Review | phase 完成未 Review 就 commit | commit 前必须 spawn Review agent |
| 自己动手改源码 | `edit`/`write` src/ 中的文件 | 停下来。写任务描述交给 Generator |
| 凭猜测操作子 agent | 未加载 `subagent` 技能就写 ai serve/kill 命令 | 先 `find_skill(name="subagent", load=true)` |
| 无 eval report 就 commit | commit 时没有 `.pge/eval-{task}.md` PASS | 先读 eval report，必须存在且 PASS |
| Generator 用幻觉 API | grep 显示函数不存在 | `ai send` correction 给 Generator |
| >3 轮 eval | 同一任务反复 fail | 停下来报告用户——spec 有问题 |
| 任务共享文件 | 两个任务改同一文件 | 改为串行 |
| Task 太大/太小 | >500 行 / <80 行 | 拆分 / 合并相邻任务 |
| PGE 结束但 agent 存活 | 流程结束未清理子 agent | 最后一步：检查 spawn 列表，逐个 cleanup |
| kill 非自己 spawn 的 agent | `ai kill` 了别的 agent | ⛔ 严禁。遵循 `subagent` 安全规则 |
| 用 `send --wait` 收集 `--input-file` 任务回复 | spawn 时传了任务又 send | 用 `watch --follow` 观察 |
| watch 超时后直接 kill | watch 返回就 kill | 先 `git diff` 检查产出，有变化再 watch 一轮 |
| kill 后不检查就手动重做 | kill 后直接写代码 | 先 `git diff` 检查子 agent 产出 |

## Error Handling

| Scenario | Action |
|----------|--------|
| Generator 无响应 | 连续两轮 watch 无输出且 `git diff` 无变化 → kill → 有产出+build 通过: spawn Evaluator; 否则: 报告用户 |
| Generator outputs BLOCKED | Kill → address reason → respawn once |
| Agent crash | Check rpc.log → retry with modified instructions |
| Same task fails 3× | **Stop. Report to user.** |
| Build fails after Generator | `ai send` feedback to Generator, let it fix |
| Evaluator 无响应/crash | Kill → spawn new Evaluator |
| Malformed eval report | Kill Evaluator → spawn new one, clarify format in prompt |

**不要不变地重试同一任务**——每次重试必须带上上次失败的上下文。

## Key Constraints

1. **Orchestrator 永不写实现代码** — 所有对源文件的 edit/write 交给 Generator
2. **Validate against spec, not against tasks** — tasks are means, spec is the end
3. **Generator and Evaluator are separate agents** — self-evaluation is unreliable
4. **FAIL 后 `ai send` 给同一个 Generator** — 保持上下文连续性，不 spawn 新的
5. **PASS 后才 kill Generator** — task 循环内保持存活
6. **Eval report 是门禁** — 文件必须存在且 PASS，才能进入下一个 task
7. **Commit 只在 eval PASS + review 无 P1 之后**
8. **Generator MUST read existing API before using it** — no hallucinated function calls
9. **Build MUST pass before DONE**
10. **只 kill 自己 spawn 的 agent** — 严禁批量 kill，遵循 `subagent` 安全规则

## Prompt Templates

角色映射（Generator→`coder`, Evaluator→`validator`, Review→`coder`）和 prompt 模板见 [`references/prompt-templates.md`](references/prompt-templates.md)。