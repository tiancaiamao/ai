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
6. **2-3 轮收敛** — 正常 2-3 轮循环即可收敛。超过 3 轮说明 spec 有问题，应暂停报告确认方

## Three Roles

**Orchestrator（加载本技能的你）**：分析需求、写 spec、拆解任务、调度与验收，**永不写实现代码**。
**Generator（子 agent，coder role）**：读 task 文件，实现功能。
**Evaluator（子 agent，validator role）**：独立读代码、跑验证、写 eval report。

**Generator 和 Evaluator 是独立 agent，不共享上下文——这是质量保证的关键。** 所有 agent 共同维护 `.pge/progress.md`（见 Progress Log 章节）。

**Orchestrator 是流程角色，与 `--role` 无关**——无论你是主 agent 还是被 spawn 出来的 planner 子 agent（claw/coder 等任何 role），加载本技能即以此身份执行。主 agent 无需 spawn 自己；planner 子 agent 同样以 Orchestrator 身份执行，只是向上级回报——下文所有“用户/确认方”均指你的确认来源（用户，或 spawn 你的上级 agent）。

**允许亲自执行的操作**（不算“实现代码”）：只读信息收集（grep / git diff / 跑 build / 跑 test）、在 spec 中编写 Verify 命令、git restore/checkout 回滚（KC 1 的唯一例外）、git commit、写 `.pge/*` 流程文件（spec / state / progress / tasks / phase-start-commit；**eval-*.md 与 review-*.md 除外**，只有 Evaluator/Review agent 可写）。

## Prerequisite

- **`subagent`** — 子 agent 完整生命周期（spawn → watch → cleanup）
- **`worker-judge`** — Phase 3 的 Gen-Eval 循环是 Worker-Judge 模式的特化
- **`review`** — Phase 4 的代码审查使用 review 技能方法论

**⚠️ MUST：** 在执行任何子 agent 操作前，确认 `subagent` 技能已加载。如未加载，先 `find_skill(name="subagent", load=true)`。PGE 不重复定义 spawn/watch/kill 流程。

**按需加载：** 进入 Phase 3 前加载 `find_skill(name="worker-judge", load=true)`；进入 Phase 4 前加载 `find_skill(name="review", load=true)`。

## Decomposition Hierarchy

PGE 用于复杂 feature 开发，拆解层次为：

```
Design (用户需求)
  └── Phase / Milestone (大的阶段)
        └── Task (具体可执行的任务)
```

- **Phase** = milestone 概念，一组相关 task 的集合。例如"认证模块"、"API 层"、"前端集成"
- **Task** = 最小可执行单元，一个 Generator 一次完成（80-500 行）
- 不必一次性拆完所有 phase/task，可以**动态拆解**——先拆第一批，执行后根据结果调整

## Execution Flow

### Phase 1: Spec Alignment

1. **Understand** — 和用户讨论需求
2. **Write spec** — 写入 `.pge/spec.md`（模板见 [`references/spec-template.md`](references/spec-template.md)）
3. **Spec Quality Gate** — 每个 acceptance criterion 必须有可执行的 Verify 命令
4. **Get user confirmation** — 展示 spec，等确认方确认（主 agent = 用户；planner 子 agent = 上级）

### Phase 2: Decomposition

1. **划分 Phase** — 将 spec 按里程碑拆为多个 phase
2. **拆解 Task** — 当前 phase 内拆为具体 task，写入 `.pge/tasks/task-{name}.md`（模板见 [`references/task-template.md`](references/task-template.md)）
3. **Scope Guard** — 如果当前 phase 的 task 数 > 5，Orchestrator 应更精细化拆解 task：确保每份 task 描述包含完整上下文，**禁止模糊的任务边界**。多 task phase 中，每个 Generator 只知自己的 task 描述，不接收全局规则

**Task 拆解规则：**
- 每个任务 80-500 行（<80 合并，>500 拆分）
- 任务之间不共享文件（共享则改为串行）
- 给 WHAT（outcome），不给 HOW（实现），但包含足够上下文让 Generator 独立工作

### Phase 3: Generate, Evaluate, Iterate (Worker-Judge Loop)

**⚠️ 进入前加载 `worker-judge` 技能：** `find_skill(name="worker-judge", load=true)`

**⚠️ 每个 task PASS 后必须更新 `.pge/state.md`（模板见 [`references/state-template.md`](references/state-template.md)）。它是 compaction 后恢复进度的唯一依据，漏更新 = 流程断裂。更新规则见 State Tracking 章节。**

每个 task 执行以下循环：

```
1. 写任务文件 → progress.md → spawn Generator (coder role)
2. Watch Generator → 等待 DONE/BLOCKED
3. Generator 完成 → 写 progress.md（含产出文件列表）
   不要 kill，保持活着
4. **Kitchen Sink 检查**（Orchestrator 执行，不 spawn agent）：   `git status --porcelain --untracked-files=all` 列出所有变更文件（含新增、暂存、未暂存），对比 task 的 Write 文件列表
   对比基线 = task 开始时的已变更文件集（用户既有未提交修改、上一 task 未 commit 的产物不算越界）。
   有超出范围的文件 → 单文件机械越界：Orchestrator 直接 `git restore` 并记 progress.md；范围不清或跨多文件：progress.md → ai send 让 Generator 回滚 → 回到步骤 2
   未超出范围 → progress.md → 继续
5. Spawn Evaluator (validator role) → 独立读代码、跑验证命令
6. Evaluator 写报告 → .pge/eval-{task}.md → progress.md（含 PASS/FAIL + 摘要）

            ┌── PASS ──→ ① 更新 .pge/state.md（表格 + Next Task + Attempt Log）
   │              ② progress.md（state.md 已更新）
   │              ③ 可选 commit：git add -A && git commit -m "task-{name}: ..."
   │              ④ Kill Generator + Evaluator
   │              ⑤ 下一个 task
   │
   └── FAIL ──→ ai send eval feedback 给同一个 Generator
                 Generator 修复 → spawn 新 Evaluator → 回到步骤 5
                 ↑
                 └── 最多 3 轮，仍 FAIL → 停下来报告确认方
```

**FAIL 反馈模板：** 见 [`references/prompt-templates.md`](references/prompt-templates.md) 的 "Generator Fix Prompt" 部分

**门禁规则（核心执行约束）：**
- **PASS/FAIL 判定必须由独立 Evaluator 做，只有 Evaluator agent 可以写 `.pge/eval-*.md`**（Orchestrator 可以跑构建/测试收集信息，但不得创建 eval report）
- `.pge/eval-{task}.md` 不存在 = task 未完成 = **不能进入下一个 task**

**Eval Report 格式：** 见 [`references/eval-report-template.md`](references/eval-report-template.md)

**One task at a time.** 不要在 Task 1 通过前启动 Task 2。

**⚠️ 并发限制：** Generator + Evaluator = Orchestrator + 2 个子 agent = 3（无论 Orchestrator 自身是主 agent 还是 planner 子 agent，均计入基数），已达 `subagent` 技能的并发上限。Phase 4 spawn Review 前必须先 kill 当前 task 的 Generator 和 Evaluator。

### Phase 4: Phase Review & Commit

**⚠️ 进入前加载 `review` 技能：** `find_skill(name="review", load=true)`

**Phase 3 Eval vs Phase 4 Review 的区别：**

| 维度 | Evaluator (Phase 3) | Review (Phase 4) |
|------|---------------------|------------------|
| 问的问题 | "你完成了宣称的功能吗？" | "代码写得好吗？" |
| 验证对象 | 单个 task 的 acceptance criteria | 整个 phase 的跨 task 代码变更 |
| 粒度 | task 级（功能维度） | phase 级（质量维度） |
| 关注点 | 功能正确性：需求是否实现、测试是否通过 | 代码健康度：架构一致性、dead code、重复、边界处理、copy-paste 错误 |
| 触发时机 | 每个 task 完成后立即执行 | 当前 phase 所有 task 完成后执行 |
| 通过标准 | 所有 acceptance criteria ✅ | 无 P1 及以上问题 |
| 产出物 | `.pge/eval-{task}.md`（功能验证报告） | `.pge/review-phase{N}.md`（代码审查报告） |

**两者互补，职责不重叠，不能省略 Review。** Evaluator 确保功能正确（"做对了事"），Review 确保代码健康（"把事情做好"）。

1. **Record start commit** — 在 Phase 3 开始前（spawn 第一个 Generator 前）执行一次：`git rev-parse HEAD > .pge/phase-start-commit`（标记 phase 基线）
2. **Spawn Review agent**（`reviewer` role）— 审查整个 phase 的代码变更。使用 `git diff $(cat .pge/phase-start-commit)..HEAD` 作为 diff 输入（Phase 3 各 task 已独立 commit，diff 累计所有 task 变更）。使用 `review` 技能的 reviewer system prompt（`~/.ai/skills/review/reviewer.md`）
3. **Review agent 写** `.pge/review-phase{N}.md` — 包含发现的问题（P0-P3）
4. **Orchestrator 读 review report**：
   - **无 P1**: 可以 commit
   - **有 P1**: 写修复任务 → spawn Generator 修复 → spawn Evaluator 验证 → 回到 Review
      - **P2/P3**: 记录在 state.md 的 Known Issues 中，不阻塞 commit
5. **Phase 合入** — 前提：所有 eval report PASS + 全量回归通过 + review 无阻塞问题（P0/P1）。
   - 若 Phase 3 各 task 已独立 commit，执行 `git merge` 或 `git rebase` 合入目标分支
   - 若未独立 commit，执行最终 commit
   - Commit message 模板：`phase{N}: <phase-name>\n\n<task-list>\n\nReview: <review-file>`
6. **Update state.md Phase Log** — `edit` state.md 追加一行 Phase Log（commit hash + review 结果）
7. **Cleanup all subagents** — 检查 spawn 列表，逐个 cleanup
8. **下一个 Phase** — 如果还有未完成的 phase，回到 Phase 2 处理下一个 phase；所有 phase 完成则结束

## File Layout

```
.pge/
  spec.md              # 需求 + 验收标准
  state.md             # 全局状态快照（Orchestrator 维护，compaction 恢复依据）
  progress.md          # WAL 操作日志（所有 agent 追加写入，见证流程执行）
  tasks/
    task-{name}.md     # 任务描述
  eval-{task}.md       # Evaluator 报告
  review-phase{N}.md   # Review 报告
```

建议在 `.gitignore` 中添加 `.pge/`（过程文件，不提交到仓库）。

## Progress Log (WAL)

所有 agent 按时间顺序向 `.pge/progress.md` **追加**（禁止覆盖写入），作为流程执行的见证——eval report + git log 覆盖功能视角，progress.md 提供流程合规性视角。

**格式：** 每行一条：`[时间戳] 角色 | 事件`，如 `[2025-07-07 10:15:00] GENERATOR | gen-auth DONE. Write: pkg/auth/login.go`。角色名用 PGE 语义角色大写（GENERATOR/EVALUATOR/REVIEW，与 `--role` 无关）；planner 行无固定语义名，用你实际的 role 大写（如 ORCHESTRATOR/CLAW）

写入时机：Orchestrator 在 spawn/kill、Kitchen Sink、更新 state.md、commit 时；Generator 在 DONE/BLOCKED 时；Evaluator 在写完 eval report 后。

| 文件 | 职责 | 维护者 |
|------|------|--------|
| `state.md` | 状态快照（进度、决策） | Orchestrator 仅，`edit` |
| `progress.md` | 操作日志 | 所有 agent，`>>` 追加 |

## State Tracking

`state.md` 是全局状态快照（模板见 [`references/state-template.md`](references/state-template.md)），与 progress.md 配合用于 compaction 后恢复。

**更新规则（每个 task PASS 后）：**
1. **必做** `edit` Task Status 表格：标记 task 为 ✅ PASS + eval 文件名
2. **必做** `edit` Next Task：改为下一个 task 名
3. **条件触发** 如果有被放弃的方案 → `edit` Attempt Log 追加一行（路径 → 原因，≤20 字）
4. **条件触发** 如果本 task 的实现路径偏离 spec 预期（如发现已有现成机制、需求前提不成立）→ **重估剩余任务**：修改/取消后续 task 并在 Key Decisions 记录原因，不能让失效任务照原样执行
5. **条件触发**（仅在 phase 结束时）追加 Phase Log 一行：commit hash + review 结果

## Context Recovery（compaction 后）

当 context 被 compaction 压缩后，按以下步骤恢复：

1. Read `.pge/spec.md` — 回顾目标
2. Read `.pge/progress.md`（tail -30）— 了解最近操作序列
3. Read `.pge/state.md` — 了解当前进度、关键决策、被放弃的方案和下一步
4. Resume from `state.md` 的 "Next Task"

## ⛔ Anti-Patterns（必读）

| 反模式 | 症状 | 正确做法 |
|--------|------|----------|
| 无 spec 就开始 | 没有 .pge/spec.md 就执行 | 先写 spec，等用户确认 |
| Generator task 太模糊 | 任务描述 < 2 句话 | 加更多上下文 |
| Orchestrator 创建 eval report | `write .pge/eval-*.md` | 只有 Evaluator 可以写 eval report |
| Generator 用幻觉 API | grep 显示函数不存在 | `ai send` correction 给 Generator |
| 任务共享文件 | 两个任务改同一文件 | 改为串行 |
| Task 太大/太小 | >500 行 / <80 行 | 拆分 / 合并相邻任务 |
| PGE 结束但 agent 存活 | 流程结束未清理子 agent | 最后一步：检查 spawn 列表，逐个 cleanup |
| 用 `send --wait` 收集首次回复 | spawn 时传了任务，用 send 而非 watch 收首次结果 | 首次结果用 `watch --follow` 观察；`send --wait` 仅用于 FAIL 后发修复反馈 |
| watch 超时后直接 kill | watch 返回就 kill | 先 `git diff` 检查产出，有变化再 watch 一轮 |
| kill 后不检查就手动重做 | kill 后直接写代码 | 先 `git diff` 检查子 agent 产出 |
| 不写 progress.md | progress.md 为空或不存在 | 每个 agent 按写入时机追加，作为流程见证 |
| progress.md 被覆盖而非追加 | `write` 而非 `>>` 导致历史丢失 | 始终使用 `bash -c 'echo "..." >> .pge/progress.md'` |
| state.md / progress.md 混淆 | 在 state.md 里记流水账，或在 progress.md 里维护状态 | state.md=状态快照(Orchestrator)，progress.md=操作日志(所有 agent) |

## Error Handling

| Scenario | Action |
|----------|--------|
| Generator 无响应 | 连续两轮 watch 无输出且 `git diff` 无变化 → kill → 有产出+build 通过: spawn Evaluator; 否则: 报告确认方 |
| Generator outputs BLOCKED | Kill → address reason → respawn once |
| Agent crash | Check rpc.log → retry with modified instructions |
| Same task fails 3× | **Stop. Report to user.** |
| Build fails after Generator | `ai send` feedback to Generator, let it fix |
| Evaluator 无响应/crash | Kill → spawn new Evaluator |
| Malformed eval report | Kill Evaluator → spawn new one, clarify format in prompt |

**不要不变地重试同一任务**——每次重试必须带上上次失败的上下文。

## Key Constraints

1. **Orchestrator 永不写实现代码** — 所有对源文件的 edit/write 交给 Generator。这是流程策略（保持验收中立、防 self-evaluation bias），非能力限制；允许操作白名单见 Three Roles 章节
2. **Validate against spec, not against tasks** — tasks are means, spec is the end
3. **Generator and Evaluator are separate agents** — self-evaluation is unreliable
4. **FAIL 后 `ai send` 给同一个 Generator** — 保持上下文连续性，不 spawn 新的
5. **PASS 后才 kill Generator** — task 循环内保持存活
6. **Eval report 是门禁** — 文件必须存在且 PASS，才能进入下一个 task
7. **每个 task PASS 后更新 state.md** — 按需更新 Task Status + Next Task（必做）+ Attempt Log（条件）+ Phase Log（条件）。见 State Tracking 章节；compaction 后恢复上下文唯一依据
8. **Task 级 commit 可在 eval PASS 后执行** — 每个 task 通过 Evaluator 验证后即可独立 commit。Phase end 的 review 检查跨 task 代码质量，review 无 P1 后执行 Phase 合入
9. **Generator MUST read existing API before using it** — no hallucinated function calls
10. **Build MUST pass before DONE** — 但 task 级验证只保证局部成立；phase 合入前必须通过全量回归（见 Phase 4）
11. **Kitchen Sink 检查** — Generator DONE 后、spawn Evaluator 前，Orchestrator 跑 `git status --porcelain --untracked-files=all` 对比 task Write 范围，超范围则回滚；task 文件的 Constraints/Stop Conditions 节是事前声明，两者配合使用
12. **Generator DONE 必须附结果包** — Verified / Risks / OpenQuestions（见 prompt-templates.md）。Risks 和 OpenQuestions 非空时，Orchestrator 应在验收时复核这些项，不能当空处理
13. **只 kill 自己 spawn 的 agent** — 严禁批量 kill，遵循 `subagent` 安全规则

## Prompt Templates

角色映射（Generator→`coder`, Evaluator→`validator`, Review→`reviewer`）和 prompt 模板见 [`references/prompt-templates.md`](references/prompt-templates.md)。发起 PGE 的 agent 本身（即 Orchestrator）不 spawn 自己，任意 `--role` 均可。

## End-to-End Example

完整的单 task 生命周期示例见 [`references/end-to-end-example.md`](references/end-to-end-example.md)。
