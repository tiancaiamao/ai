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
6. **2-3 轮收敛** — 正常情况下 2-3 轮 Generator-Evaluator 循环即可收敛。超过 3 轮说明 spec 有问题，应暂停报告用户（MindStudio）

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

## Activation

PGE is a skill loaded into the current agent. **You ARE the Orchestrator.** No need to spawn yourself.

Trigger: user says "用 PGE 模式" / "pge" / 描述了一个复杂实现任务需要编排。

## Prerequisite Skills

- **`subagent`** — 子 agent 完整生命周期（spawn → watch → cleanup）。PGE 中的所有子 agent 操作遵循 `subagent` 技能定义的生命周期模型，此处不重复。

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
- **L1 (Structural)**: `make` 或项目等效构建命令 passes, files exist, correct number of files, correct function signatures, imports resolve. Does NOT prove correctness.
- **L2 (Behavioral)**: Unit tests pass, golden file matches, smoke test produces expected output, API returns expected status codes. Proves correctness.

#### Spec Checklist (Quality Gate for Each Criterion)

Before starting Phase 2, Orchestrator must verify **every** acceptance criterion passes this checklist:

```
For each criterion in spec.md:
  □ Is it specific? (not "the system should work well")
  □ Is it falsifiable? (there exists a scenario where it clearly fails)
  □ Does it have an executable verification command?
    - L1: e.g., `make` 或项目等效构建命令, `grep -r "func HandleLogin" pkg/`,
            `test -f src/auth.c`
    - L2: e.g., `./run_test auth_jwt`,
            `curl -s localhost:8080/api/login | jq .status`
  □ Can a new agent (with no prior context) execute the verification?

If ANY criterion fails the checklist → rewrite that criterion before proceeding.
```

**Rule: Unverifiable criterion =不合格 criterion.** If you cannot write a verification command, the criterion is too vague. Tighten it or split it into verifiable sub-criteria.

### Phase 2: Task Decomposition

分析 spec，拆解成可执行的 task。

```bash
mkdir -p .pge/tasks    # 首次使用时创建目录
```

写入 `.pge/tasks/task-{name}.md`。

#### Task Template

```markdown
# Task: <short description>

## Goal
<what this task accomplishes>

## Files (scope)
<expected files to modify/create — MUST be explicit>

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

### Phase 3: Generate, Evaluate, Iterate (Worker-Judge Loop)

For each task in the decomposition:

```
┌──────────────────────────────────────────────────────────────────┐
│                    Per-Task Worker-Judge Loop                     │
│                                                                  │
│  1. Write task → .pge/tasks/task-{name}.md                       │
│  2. Spawn Generator (子 agent, ai serve)                         │
│  3. Watch Generator → 等待 DONE/BLOCKED                          │
│  4. Generator 完成 → 不要 kill，保持活着                          │
│  5. Spawn Evaluator (独立 agent, ai serve)                        │
│  6. Evaluator 对照 spec 逐条验证                                   │
│  7. Evaluator 写结果 → .pge/eval-{task}.md                        │
│                                                                  │
│     ┌── PASS ──→ Kill Generator + Evaluator → 下一个 task         │
│     │                                                            │
│     └── FAIL ──→ ai send eval feedback 给同一个 Generator         │
│                   Generator 修复 → 回到步骤 5                     │
│                   ↑                                              │
│                   └── 最多 3 轮，仍 FAIL → 停下来报告用户          │
└──────────────────────────────────────────────────────────────────┘
```

**详细步骤：**

1. **Write task description** → `.pge/tasks/task-{name}.md`
2. **Spawn Generator** — 通过 `ai serve`（tmux 后台），给清晰的任务范围、文件列表、验收标准
3. **Watch Generator** — 等待 DONE/BLOCKED
4. **Generator 完成后** — **不要 kill Generator**，保持它活着（后续可能需要它修复问题）
5. **Spawn Evaluator** — 独立 agent，对照 spec 逐条验证。**Evaluator 必须写结果到 `.pge/eval-{task}.md`**（见下方 Eval Report 格式）
6. **读 eval report** — Orchestrator 读 `.pge/eval-{task}.md`（Evaluator 超时 300s，超时则 kill Evaluator，spawn 新的）：

   - **PASS**: `ai kill` Generator + Evaluator，进入下一个 task
   - **FAIL**: 把 eval report 中的失败项作为反馈，通过 `ai send` 发给**同一个 Generator**（它有完整上下文）。然后 `ai kill` Evaluator，spawn 新 Evaluator，回到步骤 5
7. **循环上限**: 同一 task 失败 **3 次** → 停下来，报告用户。可能 spec 有问题。
8. **One task at a time.** 不要在 Task 1 通过前启动 Task 2。

**为什么 FAIL 后 `ai send` 给同一个 Generator？**
- 同一 agent 有完整上下文（已经读了所有相关源文件）
- 只需要处理 Evaluator 发现的具体问题
- 不需要重新构建上下文，节省 token 和时间
- 只有 task 真正 PASS 了才 kill Generator

#### Eval Report 格式

Evaluator **必须**将验证结果写入 `.pge/eval-{task}.md`。这是 task 完成的门禁文件——没有这个文件，task 就不算完成。

```markdown
# Eval Report: {task-name}

**Evaluator Agent**: {agent-name} ({agent-id})
**Timestamp**: {iso timestamp}

## Result: PASS / FAIL

## Criteria Verification

### L1 — Structural
- [✅/❌] <criterion> — Evidence: <actual output or observation>
- [✅/❌] <criterion> — Evidence: <actual output or observation>

### L2 — Behavioral
- [✅/❌] <criterion> — Evidence: <actual output or observation>
- [✅/❌] <criterion> — Evidence: <actual output or observation>

## Issues Found (if any)
- <description of each failure, with enough detail for Generator to fix>
```

**门禁规则：**
- **Orchestrator 不得创建 eval report 文件** — 只有 Evaluator agent 可以写
- Orchestrator 必须读 eval report 才能判断 task 状态
- 文件不存在 = task 未完成 = 不能进入下一个 task
- PASS 后才能 `ai kill` Generator（同一 task 循环内保持 Generator 存活）

### Phase 4: Phase Review

所有 task 完成后（所有 eval report 都是 PASS）：

1. **Record start commit** — `git rev-parse HEAD > .pge/phase-start-commit`（Review 需要 diff 范围）
2. **Spawn Review agent** — 审查整个 phase 的代码变更质量（`git diff <start_commit>..HEAD` + 读源文件）
3. **Review agent 写** `.pge/review-{phase}.md` — 包含发现的问题（P0/P1/P2/P3）
4. **Orchestrator 读 review report**：
   - **无 P1**: 可以 commit
   - **有 P1**: 写修复任务 → spawn Generator 修复 → spawn Evaluator 验证 → 回到 Phase Review
   - **P2/P3**: 记录在 progress.md，不阻塞 commit

### Phase 5: Commit & Cleanup

1. **Final commit** — 前提：所有 eval report PASS + review 无 P1
2. **Cleanup all subagents** — `ai kill` 每个已 spawn 的 agent（检查 subagent 文件列表）
3. **Report to user** — 完成了什么、通过了什么、review 发现了什么

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
   - Run: make (or project-equivalent build command)
   - If build fails → fix it immediately. Build failure = task not done.
   - Do NOT output DONE until build passes.

3. OUTPUT DONE MARKER — When genuinely complete:
   - Output exactly: DONE: <comma-separated file list>
   - Example: DONE: src/auth.c, src/middleware.c
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
   b. Run build (make)
   c. Do NOT kill Generator — keep alive for potential fix rounds
   d. Spawn Evaluator → wait for eval report
   e. Read .pge/eval-{task}.md:
      - PASS → kill Generator + Evaluator, next task
      - FAIL → ai send feedback to Generator, kill Evaluator, spawn new Evaluator
3. If BLOCKED:
   a. Read reason from Generator output
   b. Kill Generator
   c. If reason is API confusion → provide guidance, respawn
   d. If reason is spec ambiguity → clarify spec, respawn
4. **If timeout (600s) reached**（例外：超时时 Generator 已被强制 kill，不再适用 keep-alive 规则）：
   a. Kill Generator
   b. Check if any files were created
   c. If files exist + build passes → spawn Evaluator to validate
   d. If eval FAIL → spawn **new** Generator with fix task (original is dead)
   e. If no files or build fails → mark FAILED, report to user
```

### Test Policy

- **Do NOT write tests-for-testing-sake** — no empty test scaffolding that proves nothing
- **DO write behavioral verification tests** when they validate real correctness:
  - Tests that catch real bugs (edge cases, error paths)
  - Golden file tests that pin expected output
  - Integration smoke tests that verify end-to-end flow
- If the project already has test patterns, follow them
- If L2 acceptance criteria require running `tests`, then tests are mandatory

## Progress Tracking

Maintain `.pge/progress.md`:

```markdown
## Progress

## Phase: <name>
- [ ] Task 1: <name> — IN PROGRESS
- [x] Task 2: <name> — VALIDATED
- [ ] Task 3: <name> — NOT STARTED

## Validation Log

### Task 1: <name>
- Generator: gen-001 (alive)
- Evaluator: val-001 (killed)
- Eval: .pge/eval-task-1.md — FAIL (round 1)
- Fix: ai send feedback to gen-001
- Eval: .pge/eval-task-1.md — PASS (round 2)
- Status: VALIDATED ✅

### Task 2: <name>
- Generator: gen-002 (killed)
- Evaluator: val-002 (killed)
- Eval: .pge/eval-task-2.md — PASS
- Status: VALIDATED ✅

## Phase Review
- Review: review-001 (killed)
- Report: .pge/review-phase-1.md — 0 P1 issues
- Commit: abc123
```

**Every task entry MUST include eval report path and verdict.**

## Error Handling

| Scenario | Detection | Action |
|----------|-----------|--------|
| Generator timeout | `timeout` exits 124 or 600s reached | Kill → check files + build → if pass: spawn Evaluator; if fail: report to user |
| Generator outputs BLOCKED | Parse output for "BLOCKED:" | Kill → address reason → respawn once |
| Agent crash | `ai ls` shows `failed` or `killed` | Check rpc.log → retry with modified instructions |
| Agent off-track | Parse output, see wrong direction | `ai send` correction, or kill + respawn |
| Same task fails 3× | Three consecutive eval FAILs | **Stop. Report to user.** |
| Evaluator says not done | Eval report says FAIL | `ai send` feedback to same Generator, spawn new Evaluator |
| Build fails after Generator | build returns non-zero | Report to Generator via `ai send`, let it fix |
| Evaluator timeout | 300s reached, no eval report | Kill Evaluator → spawn new one |
| Evaluator crash | `ai ls` shows `failed` | Check output, spawn new Evaluator |
| Malformed eval report | File exists but no PASS/FAIL verdict | Kill Evaluator → spawn new one, clarify format in prompt |
| Spec changed mid-execution | User modifies spec during phase | Re-evaluate completed tasks? Report to user for guidance |
| Background process died | `ai ls` shows agent gone | 遵循 `subagent` 技能错误处理 |

## Key Constraints

1. **Orchestrator 永不写实现代码** — 所有对源文件的 edit/write 操作都必须交给 Generator 子 agent。Orchestrator 只负责读代码（理解上下文写任务描述）和管理流程。
2. **Each generator gets one clear task** — not a laundry list
3. **Validate against spec, not against tasks** — tasks are means, spec is the end
4. **Generator and Evaluator are separate agents** — self-evaluation is unreliable
5. **Stop on repeated failure** — 3 次失败后停下来报告用户
6. **Commit 只在 eval report PASS + review 无 P1 之后** — eval report 文件是硬性门禁
7. **所有子 agent 操作遵循 `subagent` 技能** — spawn、watch、cleanup 的具体代码见 `subagent` 技能，本技能只定义参数
8. **Structured handoff between agents** — eval report + progress.md, not compaction
9. **Every acceptance criterion must have an executable verification command** — unverifiable = invalid
10. **Eval report 是 task 完成的门禁** — `.pge/eval-{task}.md` 文件必须存在且内容为 PASS，才能进入下一个 task
11. **Generator MUST read existing API before using it** — no hallucinated function calls
12. **Build MUST pass before DONE** — build failure = task incomplete
13. **FAIL 后 `ai send` 给同一个 Generator** — 不 spawn 新的，保持上下文连续性
14. **PASS 后才 kill Generator** — task 循环内保持 Generator 存活，只有 PASS 才 cleanup
15. **PGE 开始前只观察不操作** — `ai ls` 查看环境状态，但**绝对不 kill 不是自己 spawn 的 agent**。如需清理孤儿报告给用户
16. **只 kill 自己 spawn 的 agent** — 遵循 `subagent` 技能安全规则，维护 spawn 列表，严禁批量 kill

## ⛔ Mandatory Self-Check

| Assertion | Trigger | Fix |
|-----------|---------|-----|
| 跳过 subagent 技能直接操作子 agent | 直接写 tmux/ai serve/ai kill 命令 | 先加载 `subagent` 技能，按其流程操作 |
| No spec written | Starting execution without .pge/spec.md | Write spec first |
| No user confirmation | Executing without user approval | Show spec, wait for ok |
| Generator task too vague | Task description < 2 sentences | Add more context |
| Skipped evaluation | Task done but no eval report file | Spawn Evaluator → wait for `.pge/eval-{task}.md` |
| Self-evaluation | 自己跑测试判定通过 | Must spawn separate Evaluator agent |
| Silent failure | Generator failed but didn't report | Always check exit status |
| Tasks share files | Two tasks modify same file | Make sequential |
| Task too large | Estimated >500 lines | Split into smaller tasks |
| Task too small | Estimated <80 lines | Merge with adjacent task |
| >3 eval rounds | Same task keeps failing validation | Stop. Report to user — spec needs revision |
| Criterion lacks verification command | Acceptance criterion has no executable verify step | Rewrite criterion or add verify command |
| Committing without eval report | Attempting commit without `.pge/eval-{task}.md` PASS | Read eval report first, must exist + PASS |
| Generator used hallucinated API | `grep` shows function doesn't exist | `ai send` correction to Generator |
| No phase review | Completed phase without Review | Spawn Review agent before commit |
| 自己动手改源码 | `edit`/`write` src/ 中的文件 | 停下来。写任务描述交给 Generator |
| 自测并判定通过 | 自己跑测试后宣布 PASS | Orchestrator 可以运行构建和测试命令来收集信息，但**判定 PASS/FAIL 必须由 Evaluator 做** |
| Orchestrator 创建 eval report | `write .pge/eval-*.md` | 只有 Evaluator 可以写 eval report |
| PGE 结束但有 agent 存活 | PGE 流程结束但未清理所有子 agent | 最后一步：检查 spawn 列表，逐个 cleanup |
| kill 了非自己 spawn 的 agent | `ai kill` 了 `ai ls` 中的非本流程 agent | ⛔ **严禁**。遵循 `subagent` 技能安全规则 |

## Reference Prompts

PGE spawn 子 agent 时，**遵循 `subagent` 技能定义的 spawn/watch/kill 流程和参数格式**。本节只定义 PGE 特有的内容：角色选择、prompt 模板、文件路径。

### Role Mapping

| PGE Role | `--role` 参数 | 说明 |
|----------|--------------|------|
| Generator | `coder` | 实现代码 |
| Evaluator | `validator` | 独立验证 |
| Review | `coder` | 代码审查 |

具体的 `ai serve` 参数（`--name`, `--input-file`, `--id-file`, `--timeout` 等）参见 `subagent` 技能，本技能不重复定义。

### Generator Prompt 模板

写入 `/tmp/task-{name}.md`，作为 `--input-file` 传入：

```markdown
## Task: {title}

## Context
{简要项目背景，帮助 Generator 理解代码库}

## What to Implement
{具体的实现要求，给 WHAT 不给 HOW}

## Files to Modify/Create
{明确的文件列表}

## Verification
{构建命令 + 测试命令}

## Rules
1. READ BEFORE WRITE — grep 确认 API 存在再使用
2. BUILD MUST PASS — 实现后必须构建成功
3. Output DONE: <file list> when complete
```

### Evaluator Prompt 模板

写入 `/tmp/eval-{task}.md`，作为 `--input-file` 传入：

```markdown
## Task: Evaluate {task-name}

You are an INDEPENDENT evaluator. You did NOT write this code.
Critically and objectively verify each acceptance criterion.

## Spec Acceptance Criteria
{从 spec.md 复制相关 criteria}

## Instructions
1. cd {project_dir}
2. For each criterion, run the verification command YOURSELF
3. For code quality, READ the actual source files
4. Output a structured report with ✅ or ❌ for EVERY criterion, with EVIDENCE
5. For any ❌, explain what failed and what the actual behavior was
6. At the end, give overall PASS/FAIL verdict
7. Write your report to .pge/eval-{task}.md

## Eval Report Format
Write to .pge/eval-{task}.md:
- Result: PASS / FAIL
- Each criterion: ✅/❌ + evidence
- Issues found (if any): enough detail for Generator to fix
```

### Review Agent Prompt 模板

写入 `/tmp/review-{phase}.md`，作为 `--input-file` 传入：

```markdown
## Task: Review Phase {N} Code

Review all code changes in this phase:
cd {project_dir} && git diff {start_commit}..HEAD -- '*.c' '*.h' (adapt extensions)

Look for: memory safety, GC correctness, error handling, type safety, dead code.
Write findings to .pge/review-phase{N}.md with priority levels (P0-P3).
```

### Orchestrator

Orchestrator 通常就是你自己（当前 agent），不需要 spawn。