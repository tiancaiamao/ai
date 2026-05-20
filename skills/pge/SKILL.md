---
name: pge
description: Planner-Generator-Evaluator 编排模式。GAN 启发的多 agent 动态编排，通过 ai CLI 控制子 agent 完成复杂任务的拆解-执行-验证闭环。
---

# PGE — Planner-Generator-Evaluator

PGE 模式借鉴 GAN（生成对抗网络）的 Generator-Discriminator 竞争反馈循环，将 AI 编码拆为三个独立角色，实现动态的任务拆解-执行-验证。

## When to Use

- 复杂功能实现（多文件、多模块、有验收标准）
- 用户说 "用 PGE 模式" / "pge" / "编排模式"
- 任务需要验证闭环（实现 → 验证 → 修复循环）

**不要用于：** 简单 bug 修复、单文件改动、快速问答

## Core Theory

来自 Anthropic、OpenAI、MindStudio 的研究发现：

1. **Self-evaluation bias** — Agent 审查自己的代码会自信地夸自己。Generator 和 Evaluator **必须分离为独立 agent**（Anthropic）
2. **Context anxiety** — 上下文接近窗口限制时 Agent 会提前收摊。解法是 hard reset + 结构化 handoff 文件，而非 compaction（Anthropic）
3. **Structured feedback** — Evaluator 应输出结构化反馈（每条标准的 ✅/❌ + 具体证据），而非简单的 pass/fail（MindStudio）
4. **Progressive disclosure** — Agent 从小入口（spec.md）开始，按需深入代码。不在 system prompt 里塞全部信息（OpenAI）
5. **Context firewall** — 每个 subagent 独立上下文窗口，隔离中间噪声。Subagent 不继承主 agent 的对话历史（Martin Fowler）
6. **2-3 轮收敛** — 正常情况下 2-3 轮 Generator-Evaluator 循环即可收敛。超过 5 轮说明 spec 有问题，应暂停报告用户（MindStudio）

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
│  • 实现功能   │───►│  • 读代码    │
│  • 写代码     │    │  • 对照标准  │
│              │◄───│  • 写结构化  │
│  (如需修复)   │    │    反馈      │
└──────────────┘    └──────────────┘
```

**Generator 和 Evaluator 是独立 agent，不共享上下文。** 这是质量保证的关键。

## Activation

```bash
ai run --role orchestrator "implement dark mode for the web app"
# 或
ai serve --role orchestrator --name "my-orchestrator"
```

## Prerequisite Skills

- **`subagent`** — ai serve/send/watch/kill 的 spawn-monitor-control 模式详解
- **`worker-judge`** — Generator-Evaluator 循环的通用框架

本技能聚焦于 PGE 的编排逻辑。子 agent 的具体操作参考 `subagent` 技能。

## ⚠️ Concurrency Limit

主 agent + 子 agent 同时运行数**不得超过 3**（即最多 2 个子 agent 同时运行）。

## Execution Flow

### Phase 1: Spec Alignment

1. **Understand** — 和用户讨论需求
2. **Write spec** — 写入 `.pge/spec.md`

```markdown
# Spec: <title>

## Goal
<one sentence>

## Acceptance Criteria
- [ ] <criterion 1 — must be specific and verifiable>
- [ ] <criterion 2>

## Constraints
- <technical constraints>

## Out of Scope
- <explicitly excluded>
```

3. **Get user confirmation** — 展示 spec，等用户说 ok

### Phase 2: Task Decomposition

分析 spec，拆解成可执行的 task。写入 `.pge/tasks/NNN-<name>.md`。

```markdown
# Task: <short description>

## Goal
<what this task accomplishes>

## Files (scope)
<expected files to modify/create — MUST be explicit for parallel conflict checking>

## Estimated Size
<S(<100) / M(100-300) / L(300-500) / XL(>500, consider splitting)>

## Dependencies
<which tasks must complete first, if any>

## Acceptance
<how to verify this task is done — linked to spec's acceptance criteria>
```

**Delegation Tips — 给 WHAT (outcome)，不给 HOW (实现)。但包含足够上下文让 Generator 独立工作。**

✅ Good: `"Implement JWT auth for /api/login. Use User model in src/models/user.ts. Tokens in http-only cookies."`
❌ Bad: `"Add auth. Look at how auth usually works."`

### Phase 3: Generator-Evaluator Loop

对每个 task，执行 Generator → Evaluator 循环：

```
┌─────────┐    output    ┌───────────┐
│Generator│ ────────────►│ Evaluator │
│(spawn)  │              │ (spawn)   │
│         │◄── feedback ─│           │
└─────────┘              └───────────┘
     │                        │
     │  if all ✅ ──────────► done → next task
     │  if any ❌ ─────────► fix task → loop (max 3 rounds)
     │
     └── max 3 rounds ──► report to user
```

**Generator spawn pattern:**
```bash
SESSION="gen-001"
tmux new-session -d -s "$SESSION" \
  "ai serve --role coder \
   --system-prompt 'You are implementing task 001: Add authentication. Read .pge/spec.md and .pge/tasks/001-add-auth.md for context.' \
   --input 'Implement the task. Write code. Commit when done.' \
   --name 'gen-001-add-auth' \
   --timeout 10m"

sleep 2
GEN_ID=$(tmux capture-pane -t "$SESSION" -p | head -1 | tr -d '[:space:]')
ai watch --id "$GEN_ID" --follow --pretty
```

**Evaluator spawn pattern:**
```bash
SESSION="eval-001"
tmux new-session -d -s "$SESSION" \
  "ai serve --role validator \
   --input 'Generator 完成了 task 001 (Add authentication)。
   请独立验证 .pge/spec.md 中以下验收标准是否被满足：
   <列出相关标准>
   对每条标准：✅ 通过 + 证据 / ❌ 不通过 + 具体原因 / ⚠️ 部分满足 + 缺什么
   最后输出总结：X/Y 条完全通过。' \
   --name 'eval-001-check-auth' \
   --timeout 5m"

sleep 2
EVAL_ID=$(tmux capture-pane -t "$SESSION" -p | head -1 | tr -d '[:space:]')
ai watch --id "$EVAL_ID" --follow --pretty
```

**Round convergence:**
| Rounds | Meaning | Action |
|--------|---------|--------|
| 1 | Excellent | Proceed to next task |
| 2-3 | Normal | Proceed to next task |
| >3 | Warning | Re-examine spec clarity |
| >5 | Problem | **Stop.** Report to user — spec likely needs revision |

### Phase 4: Report

- 更新 `.pge/spec.md` 所有 checkbox 为 `[x]`
- 写最终报告到 `.pge/progress.md`
- 向用户汇报

## Context Management

### State Handoff (NOT compaction)

当 Generator 完成后，**不要**用 compaction 来压缩上下文给下一个 Generator。而是写结构化的 `state.md`：

```markdown
# State after Task 001

## What was implemented
<summary>

## Files changed
- src/auth/jwt.go — new file, JWT generation and validation
- src/api/login.go — added auth middleware

## Key decisions
- Token stored in http-only cookie (not localStorage)

## Known issues
- Token refresh not yet implemented (deferred to task 003)

## What's next
- Task 002: Add role-based access control
```

下一个 Generator 从 `state.md` + `spec.md` + 当前 task description 开始，获得干净的完整上下文。

**为什么不用 compaction：** Anthropic 发现模型在上下文接近限制时会产生 "context anxiety" — 提前收摊、跳过边缘情况。结构化 handoff 让每个 Generator 从满上下文窗口开始。

## File Structure

```
.pge/
  spec.md              # Requirements + acceptance criteria
  state.md             # Current state — updated after each generator
  tasks/
    001-add-auth.md
    002-add-rbac.md
  progress.md          # Append-only execution log
```

## Progress Tracking

`.pge/progress.md`（append-only）：

```markdown
## 14:30 — Started
- Spec: implement dark mode
- Acceptance criteria: 5

## 14:35 — Task 001: Create theme tokens
- Generator: a1b2c3 (tmux: gen-001)
- Status: done
- Files: src/theme.ts, src/tokens.css

## 14:42 — Evaluation round 1
- Criteria passed: 3/5
- Failed: toggle persistence, system preference detection
- Fix tasks created: 003, 004

## 15:00 — All criteria passed
```

## Error Handling

| Scenario | Detection | Action |
|----------|-----------|--------|
| Agent timeout | `timeout` exits 124 | `ai kill` → retry once with simpler task |
| Agent crash | `ai ls` shows `failed` or `killed` | Check rpc.log → retry with modified instructions |
| Agent off-track | Parse output, see wrong direction | `/steer` correction, or kill + respawn |
| Same task fails 2× | Two consecutive failures | **Stop. Report to user.** |
| Evaluator says not done | Criteria not all ✅ | Create specific fix tasks, loop |
| tmux session died | `tmux has-session` fails | Check `ai ls` for status, may need cleanup |

## Key Constraints

1. **Orchestrator never writes implementation code** — delegates to generators
2. **Each generator gets one clear task** — not a laundry list
3. **Validate against spec, not against tasks** — tasks are means, spec is the end
4. **Generator and Evaluator are separate agents** — self-evaluation is unreliable
5. **Stop on repeated failure** — don't burn tokens retrying forever
6. **Commit after each successful generator run** — incremental progress
7. **Always use tmux to spawn** — `ai serve` is blocking, direct call freezes orchestrator
8. **Structured handoff between generators** — state.md, not compaction

## ⛔ Mandatory Self-Check

| Assertion | Trigger | Fix |
|-----------|---------|-----|
| Direct `ai serve` call | Using `ai serve` without tmux | Wrap in tmux |
| No spec written | Starting execution without .pge/spec.md | Write spec first |
| No user confirmation | Executing without user approval | Show spec, wait for ok |
| Generator task too vague | Task description < 2 sentences | Add more context |
| Skipped evaluation | Task done but criteria not checked | Spawn independent Evaluator |
| Generator wrote tests | Output includes `*_test.go` files | Kill, strip tests, spawn Evaluator separately |
| Self-evaluation | Generator reviews its own code | Must spawn separate Evaluator agent |
| Silent failure | Generator failed but didn't report | Always check exit status |
| No state.md update | Generator completed but state.md not updated | Write state.md before spawning next agent |
| Parallel file overlap | Two parallel tasks list same file | Make sequential or re-scope |
| Task too large | Estimated >500 lines | Split into smaller tasks |
| Task too small | Estimated <80 lines | Merge with adjacent task |
| >5 eval rounds | Same task keeps failing validation | Stop. Report to user — spec needs revision |

## Difference from Old Plan/Implement Workflow

| | plan + implement (old) | PGE (new) |
|---|---|---|
| Infrastructure | `ag` CLI + Go binary | `ai` CLI (native) |
| Task scheduling | Static DAG (all tasks predefined) | Dynamic (orchestrator decides on-the-fly) |
| Validation | Per-task review in same agent | Independent Evaluator agent (GAN pattern) |
| Failure recovery | Retry ×3, manual after | Orchestrator adjusts plan dynamically |
| Context management | Compaction | Structured state.md handoff |
| Human involvement | Setup only | Spec approval + error escalation |
| Knowledge passing | Agent reads tasks.md | Generator reads spec.md + state.md + task |

## Reference Prompts

PGE 可以参考以下 prompt 模板（位于旧技能目录，作为参考资源保留）：

| Purpose | File |
|---------|------|
| Task breakdown | `plan/prompts/planner.md` |
| Plan review | `plan/prompts/reviewer.md` |
| Implementation | `implement/prompts/implementer.md` |
| Spec compliance check | `implement/prompts/spec-reviewer.md` |
| Code quality check | `implement/prompts/quality-reviewer.md` |

这些 prompt 不是 PGE 直接使用的模板，而是编写 Generator/Evaluator 的 system prompt 时的参考。根据具体项目需求调整。