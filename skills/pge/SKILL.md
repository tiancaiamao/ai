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

**所有 agent 共同维护 `.pge/progress.md`**（操作日志），详见 Progress Log (WAL) 章节。

**You ARE the Orchestrator.** No need to spawn yourself.

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
4. **Get user confirmation** — 展示 spec，等用户说 ok

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

> **🔥 state.md 是 PGE 的生命线。** 每个 task PASS 后，Orchestrator **必须**更新 `.pge/state.md`（模板见 [`references/state-template.md`](references/state-template.md)）。
>
> 为什么？context 会被 compaction 压缩，`state.md` 是恢复上下文的核心依据。**漏了一次更新 = 下次 compaction 后丢失进度 = 整个流程断裂。**
>
> 这不是可选步骤。它和 eval report 门禁一样是硬性约束。

每个 task 执行以下循环：

```
1. 写任务文件 → progress.md → spawn Generator (coder role)
2. Watch Generator → 等待 DONE/BLOCKED
3. Generator 完成 → 写 progress.md（含产出文件列表）
   不要 kill，保持活着
4. **Kitchen Sink 检查**（Orchestrator 执行，不 spawn agent）：
   `git status --porcelain --untracked-files=all` 列出所有变更文件（含新增、暂存、未暂存），对比 task 的 Write 文件列表
   有超出范围的文件 → progress.md → ai send 让 Generator 回滚 → 回到步骤 2
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
                 └── 最多 3 轮，仍 FAIL → 停下来报告用户
```

**为什么 FAIL 后 `ai send` 给同一个 Generator？**
- 同一 agent 有完整上下文（已经读了所有相关源文件）
- 只需要处理 Evaluator 发现的具体问题，不需要重新构建上下文
- 只有 task 真正 PASS 了才 kill Generator

**FAIL 反馈模板：** 见 [`references/prompt-templates.md`](references/prompt-templates.md) 的 "Generator Fix Prompt" 部分

**门禁规则（核心执行约束）：**
- **Orchestrator 不得创建 eval report** — 只有 Evaluator agent 可以写 `.pge/eval-*.md`
- `.pge/eval-{task}.md` 不存在 = task 未完成 = **不能进入下一个 task**
- Orchestrator 可以运行构建和测试命令收集信息，但 **PASS/FAIL 判定必须由独立 Evaluator 做**
- progress.md 会记录下全部角色的操作历史，这会被用于审核 agent 是否有按照 pge 技能要求执行

**Eval Report 格式：** 由 `~/.ai/roles/validator/system_prompt.md` 定义（✅/❌/⚠️ 格式），参考 [`references/prompt-templates.md`](references/prompt-templates.md) 的 Eval Report Format 章节。

**One task at a time.** 不要在 Task 1 通过前启动 Task 2。

**⚠️ 并发限制：** Generator + Evaluator = 主 agent + 2 个子 agent = 3，已达 `subagent` 技能的并发上限。Phase 4 spawn Review 前必须先 kill 当前 task 的 Generator 和 Evaluator。

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
2. **Spawn Review agent**（`coder` role）— 审查整个 phase 的代码变更。使用 `git diff $(cat .pge/phase-start-commit)..HEAD` 作为 diff 输入（Phase 3 各 task 已独立 commit，diff 累计所有 task 变更）。使用 `review` 技能的 reviewer system prompt（`~/.ai/skills/review/reviewer.md`）
3. **Review agent 写** `.pge/review-phase{N}.md` — 包含发现的问题（P0-P3）
4. **Orchestrator 读 review report**：
   - **无 P1**: 可以 commit
   - **有 P1**: 写修复任务 → spawn Generator 修复 → spawn Evaluator 验证 → 回到 Review
      - **P2/P3**: 记录在 state.md 的 Known Issues 中，不阻塞 commit
5. **Phase 合入** — 前提：所有 eval report PASS + review 无 P1。
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

`progress.md` 是所有 agent（Orchestrator、Generator、Evaluator）按时间顺序追加的操作日志，作为流程执行的见证。它与 `state.md` 职责分离：

| 文件 | 职责 | 维护者 | 更新方式 |
|------|------|--------|----------|
| `state.md` | 全局状态快照（当前进度、决策记录） | Orchestrator 仅 | `edit` 覆盖/追加 |
| `progress.md` | 操作日志（谁做了什么、结果如何） | 所有 agent | `>>` 追加 |

**格式：** 每行一条记录：`[时间戳] 角色 | 事件`

示例：
```
[2025-07-07 10:00:00] ORCHESTRATOR | Phase 1 spec.md 已写，等待用户确认
[2025-07-07 10:05:00] ORCHESTRATOR | Phase 2 拆解完成: task-auth, task-db, task-api
[2025-07-07 10:06:00] ORCHESTRATOR | Spawn gen-auth (coder)
[2025-07-07 10:15:00] GENERATOR    | gen-auth DONE. Write: pkg/auth/login.go, pkg/auth/register.go
[2025-07-07 10:16:00] ORCHESTRATOR | Kitchen Sink 检查通过，未超出范围
[2025-07-07 10:16:00] ORCHESTRATOR | Spawn eval-auth (validator)
[2025-07-07 10:22:00] EVALUATOR    | eval-auth PASS. 3/3 criteria met
[2025-07-07 10:23:00] ORCHESTRATOR | task-auth ✅ state.md 已更新
[2025-07-07 10:24:00] ORCHESTRATOR | task-auth ✅ commit: abc1234
```

**写入时机：**
| agent | 时机 | 内容 |
|-------|------|------|
| Orchestrator | spawn/kill 子 agent、Kitchen Sink 通过/回滚、更新 state.md、commit 时 | 操作名称 + 结果 |
| Generator | DONE/BLOCKED 时 | 状态 + 产出文件列表 |
| Evaluator | 写完 eval report 后 | PASS/FAIL + 摘要 |

所有 agent 使用 `bash -c "mkdir -p .pge && echo \"[$(date '+%Y-%m-%d %H:%M:%S')] ...\" >> .pge/progress.md"` 追加。

**目的：** 执行历史由 eval report + git log 覆盖功能视角，`progress.md` 提供 **流程视角**——验证每个 agent 是否按 PGE 技能要求的顺序和职责执行。会话结束后可据此审计流程合规性。

## State Tracking

`state.md` 是 PGE 的**全局状态文件**——保存当前进度、已完成 task、决策记录。`progress.md` 是其操作日志伴侣。两者共同用于 compaction 后恢复上下文。每个 task PASS 后必须更新 state.md。（模板见 [`references/state-template.md`](references/state-template.md)）

**更新规则（每个 task PASS 后，按需更新以下字段）：**
1. **必做** `edit` Task Status 表格：标记 task 为 ✅ PASS + eval 文件名
2. **必做** `edit` Next Task：改为下一个 task 名
3. **条件触发** 如果有被放弃的方案 → `edit` Attempt Log 追加一行（路径 → 原因，≤20 字）
4. **条件触发**（仅在 phase 结束时）追加 Phase Log 一行：commit hash + review 结果

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
7. **每个 task PASS 后更新 state.md** — 按需更新 Task Status + Next Task（必做）+ Attempt Log（条件）+ Phase Log（条件）。见 State Tracking 章节；compaction 后恢复上下文唯一依据
8. **Task 级 commit 可在 eval PASS 后执行** — 每个 task 通过 Evaluator 验证后即可独立 commit。Phase end 的 review 检查跨 task 代码质量，review 无 P1 后执行 Phase 合入
9. **Generator MUST read existing API before using it** — no hallucinated function calls
10. **Build MUST pass before DONE**
11. **Kitchen Sink 检查** — Generator DONE 后、spawn Evaluator 前，Orchestrator 跑 `git status --porcelain --untracked-files=all` 对比 task Write 范围，超范围则回滚
12. **只 kill 自己 spawn 的 agent** — 严禁批量 kill，遵循 `subagent` 安全规则

## Prompt Templates

角色映射（Generator→`coder`, Evaluator→`validator`, Review→`coder`）和 prompt 模板见 [`references/prompt-templates.md`](references/prompt-templates.md)。

## End-to-End Example

完整的单 task 生命周期示例见 [`references/end-to-end-example.md`](references/end-to-end-example.md)。
