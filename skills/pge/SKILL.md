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

- **`subagent`** — 子 agent 完整生命周期（spawn → watch → cleanup）。PGE 中的所有子 agent 操作遵循 `subagent` 技能定义的生命周期模型，此处不重复。
- **`worker-judge`** — Generator-Evaluator 循环的通用框架

**⚠️ MUST：在执行任何子 agent 操作前，确认 `subagent` 技能已加载到当前上下文。如果未加载，先调用 `find_skill` 工具（参数 `name="subagent"`, `load=true`）加载它。未加载时不要凭猜测操作子 agent。**

本技能聚焦于 PGE 的编排逻辑。子 agent 的 spawn/watch/kill 操作详见 `subagent` 技能。

## Execution Flow

### Phase 1: Spec Alignment

1. **Understand** — 和用户讨论需求
2. **Write spec** — 写入 `.pge/spec.md`（使用下方 Spec Template）
3. **Spec Quality Gate** — 每个 acceptance criterion 必须通过 Spec Checklist（见下方）
4. **Get user confirmation** — 展示 spec，等用户说 ok

#### Spec Template

```markdown
# Spec: <title>

## Goal
<one sentence>

## Acceptance Criteria

### L1 — Structural (must pass before L2)
- [ ] <criterion> — Verify: `<executable command>`
- [ ] <criterion> — Verify: `<executable command>`

### L2 — Behavioral (validates correctness, not just existence)
- [ ] <criterion> — Verify: `<test command or manual check>`
- [ ] <criterion> — Verify: `<test command or manual check>`

## Constraints
- <technical constraints>

## Out of Scope
- <explicitly excluded>
```

**L1 vs L2 distinction:**
- **L1 (Structural)**: `go build ./...` passes, files exist, correct number of files, correct function signatures, imports resolve. Does NOT prove correctness.
- **L2 (Behavioral)**: Unit tests pass, golden file matches, smoke test produces expected output, API returns expected status codes. Proves correctness.

#### Spec Checklist (Quality Gate for Each Criterion)

Before starting Phase 2, Orchestrator must verify **every** acceptance criterion passes this checklist:

```
For each criterion in spec.md:
  □ Is it specific? (not "the system should work well")
  □ Is it falsifiable? (there exists a scenario where it clearly fails)
  □ Does it have an executable verification command?
    - L1: e.g., `go build ./...`, `grep -r "func HandleLogin" pkg/`,
            `test -f pkg/auth/jwt.go`
    - L2: e.g., `go test ./pkg/auth/... -run TestJWTExpiry`,
            `curl -s localhost:8080/api/login | jq .status`
  □ Can a new agent (with no prior context) execute the verification?

If ANY criterion fails the checklist → rewrite that criterion before proceeding.
```

**Rule: Unverifiable criterion =不合格 criterion.** If you cannot write a verification command, the criterion is too vague. Tighten it or split it into verifiable sub-criteria.

### Phase 2: Task Decomposition

分析 spec，拆解成可执行的 task。写入 `.pge/tasks/NNN-<name>.md`。

#### Task Template

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

✅ Good: `"Implement JWT auth middleware. The handler should validate the token from the Authorization header and set user context. See spec.md acceptance criteria L1.1 and L2.1."`

❌ Bad: `"Add some auth stuff"` — too vague

#### Phase Validation Gate

**At the end of each phase (or group of related tasks), the Orchestrator MUST:**

1. Run all L1 verification commands for completed tasks
2. If any L1 fails → create a **backfill task** to fix it before proceeding
3. Run all relevant L2 verification commands (spawn Evaluator if needed)
4. Record gate result in `progress.md`

**Backfill Task Mechanism:**
When a bug is discovered in a previously completed phase:
- Create `.pge/tasks/NNN-fix-<name>.md` with `Dependencies: none` (urgency override)
- Tag it as `type: backfill` in the task file
- The backfill task MUST pass the same validation gate before the next phase continues
- Backfill tasks take priority over new feature tasks

```markdown
# Task: Fix <bug description> (BACKFILL)

## Type
backfill

## Original Task
<reference to the task that introduced the bug>

## Bug Description
<what's wrong and how to reproduce>

## Fix Verification
<specific command to confirm the fix>

## Acceptance
<must restore all previously passing L1/L2 criteria>
```

### Phase 3: Generate, Validate, Iterate

For each task in the decomposition:

1. **Spawn Generator** (via tmux) with the task file + spec.md + state.md
   - 遵循 `subagent` 技能的 Spawn 阶段
2. **Watch Generator** — poll output, wait for DONE/BLOCKED
   - 遵循 `subagent` 技能的 Watch 阶段
3. **Collect Result** — 检查:
   - Has the Generator output `DONE: <file list>`?
   - Do the listed files exist?
   - Does `go build ./...` (or equivalent) pass?
   - If yes to all → proceed to cleanup + validation
   - If Generator is still working → continue watching
4. **Cleanup Generator** — **拿到结果后立即 kill**
   - `ai kill --id <generator-id>`
   - `tmux kill-session -t <generator-session>`
   - 详见 `subagent` 技能 Cleanup 阶段
5. **Spawn Validator** (independent Evaluator agent) — **MANDATORY, not optional**
   - 同样遵循 `subagent` 生命周期
6. **Collect Validator feedback** — update progress.md with VALIDATED or issues
7. **Cleanup Validator** — **立即 kill**
   - `ai kill --id <validator-id>`
   - `tmux kill-session -t <validator-session>`
8. **Loop if needed** — create fix tasks for any failed criteria (max 3 rounds per task)
   - 每轮 spawn 新 Generator/Evaluator，每轮完成后都立即 cleanup

#### Validation Gate Per Task

Each task must reach one of these terminal states in `progress.md`:

| Status | Meaning | Can Commit? |
|--------|---------|-------------|
| `VALIDATED` | Evaluator confirmed all criteria pass | ✅ Yes |
| `SELF-CHECKED` | Generator confirmed via build + DONE output, but Evaluator not yet run | ⚠️ Only if Evaluator is queued next |
| `FAILED` | Build fails or Evaluator found issues | ❌ No |

**Rule: No task may be committed in `FAILED` status.** If Generator times out but files exist and build passes, the task goes to `SELF-CHECKED` and a Validator MUST be spawned before moving to the next task.

## Generator Rules & Completion Conditions

### Mandatory Clauses for Every Generator Task

When the Orchestrator spawns a Generator, the task instructions MUST include these clauses:

```
GENERATOR RULES (mandatory):

1. READ BEFORE WRITE — Before using any external API, type, function, or package:
   - Run: grep -r "func <name>" . or grep -r "type <name>" .
   - If it doesn't exist, DO NOT use it. Find the real API or ask.
   - If unsure about a package's API, read its source first.

2. BUILD MUST PASS — After implementation:
   - Run: go build ./... (or project-equivalent build command)
   - If build fails → fix it immediately. Build failure = task not done.
   - Do NOT output DONE until build passes.

3. OUTPUT DONE MARKER — When genuinely complete:
   - Output exactly: DONE: <comma-separated file list>
   - Example: DONE: pkg/auth/jwt.go, pkg/middleware/auth.go
   - If you cannot output DONE (build still failing), output:
     BLOCKED: <reason>
```

### Orchestrator Polling Protocol

After spawning a Generator, the Orchestrator watches:

```
Watch loop:
1. Check Generator output for "DONE:" or "BLOCKED:"
2. If DONE:
   a. Verify listed files exist (ls <each file>)
   b. Run build (go build ./...)
   c. Cleanup Generator: ai kill + tmux kill-session
   d. Mark task SELF-CHECKED
   e. Spawn Validator → mark VALIDATED when passed
   f. Cleanup Validator: ai kill + tmux kill-session
3. If BLOCKED:
   a. Read reason from Generator output
   b. Cleanup Generator: ai kill + tmux kill-session
   c. If reason is API confusion → provide guidance, respawn
   d. If reason is spec ambiguity → clarify spec, respawn
4. If timeout (600s) reached:
   a. Cleanup Generator: ai kill + tmux kill-session
   b. Check if any files were created
   c. If files exist + build passes → mark SELF-CHECKED, spawn Validator
   d. If no files or build fails → mark FAILED, report to user
```

**每个分支都必须 cleanup Generator，无一例外。** 即使超时、失败、BLOCKED，也要先 kill 再做后续处理。

### Test Policy

- **Do NOT write tests-for-testing-sake** — no empty test scaffolding that proves nothing
- **DO write behavioral verification tests** when they validate real correctness:
  - Tests that catch real bugs (edge cases, error paths)
  - Golden file tests that pin expected output
  - Integration smoke tests that verify end-to-end flow
- If the project already has test patterns, follow them
- If L2 acceptance criteria require running `go test`, the Generator MUST write those tests

## State Handoff

After each Generator completes a task, the Orchestrator writes `.pge/state.md`:

```markdown
# State

## Completed Tasks
- T001: <title> — VALIDATED
  Files: <file list>
  Key changes: <summary>

## Key Decisions
- <decision 1>
- <decision 2>

## Known Issues
- <issue> (deferred to task NNN)

## What's Next
- T00N: <next task title>
```

下一个 Generator 从 `state.md` + `spec.md` + 当前 task description 开始，获得干净的完整上下文。

**为什么不用 compaction：** Anthropic 发现模型在上下文接近限制时会产生 "context anxiety" — 提前收摊、跳过边缘情况。结构化 handoff 让每个 Generator 从满上下文窗口开始。

## File Structure

```
.pge/
  spec.md              # Requirements + acceptance criteria (L1 + L2)
  state.md             # Current state — updated after each generator
  tasks/
    001-add-auth.md
    002-add-rbac.md
    003-fix-token-refresh.md   # backfill task example
  progress.md          # Append-only execution log with VALIDATED/SELF-CHECKED status
```

## Progress Tracking

`.pge/progress.md`（append-only）：

```markdown
## 14:30 — Started
- Spec: implement dark mode
- Acceptance criteria: L1: 3, L2: 4

## 14:35 — Task 001: Create theme tokens
- Generator: a1b2c3 (tmux: gen-001)
- Status: DONE: src/theme.ts, src/tokens.css
- Build: PASS
- Validator: spawned (c4d5e6, tmux: val-001)
- Validation: VALIDATED — all L1 + L2 criteria pass

## 14:42 — Task 002: Toggle component
- Generator: e7f8g9 (tmux: gen-002)
- Status: DONE: src/components/Toggle.tsx
- Build: PASS
- Validator: spawned (h0i1j2, tmux: val-002)
- Validation: FAILED — L2.2 (toggle persistence) broken
- Fix task created: 003-fix-toggle-persist.md

## 14:50 — Task 003 (BACKFILL): Fix toggle persistence
- Generator: k3l4m5 (tmux: gen-003)
- Status: DONE: src/components/Toggle.tsx, src/hooks/usePersist.ts
- Build: PASS
- Validator: spawned (n6o7p8, tmux: val-003)
- Validation: VALIDATED — L2.2 restored

## 15:00 — All criteria VALIDATED
```

**Every task entry MUST include a Status line with one of: VALIDATED, SELF-CHECKED, FAILED.**

## Error Handling

| Scenario | Detection | Action |
|----------|-----------|--------|
| Generator timeout | `timeout` exits 124 or 600s reached | Kill → check files + build → if pass: SELF-CHECKED + spawn Validator; if fail: FAILED |
| Generator outputs BLOCKED | Parse output for "BLOCKED:" | Kill → address reason → respawn once |
| Agent crash | `ai ls` shows `failed` or `killed` | Check rpc.log → retry with modified instructions |
| Agent off-track | Parse output, see wrong direction | `/steer` correction, or kill + respawn |
| Same task fails 2× | Two consecutive failures | **Stop. Report to user.** |
| Evaluator says not done | Criteria not all ✅ | Create specific fix tasks (backfill if in later phase) |
| Validator not spawned | Task marked SELF-CHECKED but no Validator | **MUST spawn Validator before committing** |
| Build fails after Generator | `go build` returns non-zero | Mark FAILED → fix task or respawn Generator |
| tmux session died | `tmux has-session` fails | Check `ai ls` for status, may need cleanup |

## Key Constraints

1. **Orchestrator never writes implementation code** — delegates to generators
2. **Each generator gets one clear task** — not a laundry list
3. **Validate against spec, not against tasks** — tasks are means, spec is the end
4. **Generator and Evaluator are separate agents** — self-evaluation is unreliable
5. **Stop on repeated failure** — don't burn tokens retrying forever
6. **Commit after each VALIDATED task** — not SELF-CHECKED, only VALIDATED
7. **Always use tmux to spawn** — `ai serve` is blocking, direct call freezes orchestrator
8. **Structured handoff between generators** — state.md, not compaction
9. **Every acceptance criterion must have an executable verification command** — unverifiable = invalid
10. **Phase validation gates are mandatory** — no building on broken foundations
11. **Generator MUST read existing API before using it** — no hallucinated function calls
12. **Build MUST pass before DONE** — build failure = task incomplete
13. **每个子 agent 完成后必须立即 cleanup** — `ai kill` + `tmux kill-session`，遵循 `subagent` 技能生命周期。不得累积已完成的 agent
14. **PGE 开始前只观察不操作** — `ai ls` 查看环境状态，但**绝对不 kill 不是自己 spawn 的 agent**。如需清理孤儿报告给用户
15. **异常退出也要 cleanup** — BLOCKED、超时、失败等所有路径都必须 kill 子 agent
16. **只 kill 自己 spawn 的 agent** — 维护 `SPAWNED_IDS` 列表，cleanup 只针对列表中的 ID。`ai ls` 中看到的其他 agent 可能是你自己（orchestrator）、用户手动启动的、或其他流程的，严禁批量 kill

## ⛔ Mandatory Self-Check

| Assertion | Trigger | Fix |
|-----------|---------|-----|
| Direct `ai serve` call | Using `ai serve` without tmux | Wrap in tmux |
| No spec written | Starting execution without .pge/spec.md | Write spec first |
| No user confirmation | Executing without user approval | Show spec, wait for ok |
| Generator task too vague | Task description < 2 sentences | Add more context |
| Skipped evaluation | Task done but criteria not checked | Spawn independent Evaluator |
| Self-evaluation | Generator reviews its own code | Must spawn separate Evaluator agent |
| Silent failure | Generator failed but didn't report | Always check exit status |
| No state.md update | Generator completed but state.md not updated | Write state.md before spawning next agent |
| Parallel file overlap | Two parallel tasks list same file | Make sequential or re-scope |
| Task too large | Estimated >500 lines | Split into smaller tasks |
| Task too small | Estimated <80 lines | Merge with adjacent task |
| >5 eval rounds | Same task keeps failing validation | Stop. Report to user — spec needs revision |
| Criterion lacks verification command | Acceptance criterion has no executable verify step | Rewrite criterion or add verify command |
| Committing SELF-CHECKED task | Attempting to commit without VALIDATED status | Spawn Validator first |
| Generator used hallucinated API | `grep` shows function doesn't exist | Kill Generator, provide correct API info |
| No phase validation gate | Completed phase without L1/L2 check | Run validation gate before next phase |
| Generator output has no DONE marker | Generator completed without DONE/BLOCKED output | Check output manually, add to error handling |
| 子 agent 未 cleanup | `ai ls` 显示已完成的 agent 仍在 running | 每个子 agent 完成后立即 `ai kill` + `tmux kill-session`（只 kill 自己 spawn 的） |
| PGE 结束但有 agent 存活 | PGE 流程结束但未清理所有子 agent | 最后一步：检查 `SPAWNED_IDS` 列表，逐个 cleanup |
| kill 了非自己 spawn 的 agent | `ai kill` 了 `ai ls` 中的非本流程 agent | ⛔ **严禁**。只 kill `SPAWNED_IDS` 中的 ID |

## Reference Prompts

PGE 编写 Generator/Evaluator 的 system prompt 时，参考 `brainstorm` 技能产出的 design.md 结构来确保 handoff 信息完整。
