---
name: workflow
description: 对话式开发流程编排。通过自然语言驱动 brainstorm → spec → plan → implement，无需用户手工执行命令。
---

# Workflow — Conversation Interface

Workflow 是一个**对话接口**，不是命令行教程。

用户只需要表达意图，agent 负责在后台协调 skills、状态文件和执行脚本。

## Dependencies

Workflow requires these skills to be available:

- `brainstorm` — dependency inversion interview
- `spec` — structured specification writing
- `plan` — task breakdown with planner+reviewer loop
- `implement` — subagent-driven implementation
- `explore` — codebase exploration and analysis
- `ag` — agent orchestration runtime (required by `implement` skill for parallel task execution)

## User Contract (对用户)

用户应通过自然语言触发流程，例如：

- "开始一个 feature workflow：实现用户注册"
- "继续下一个阶段"
- "先给我看当前进度和阻塞点"
- "暂停 / 恢复这个 workflow"
- "回到上一步重新想"

**不要要求用户手动执行 shell 命令。**

## Agent Contract (对 agent)

agent 必须：

1. 负责 workflow 状态管理（开始、推进、回退、暂停、恢复、完成）。
2. 根据当前阶段自动调用对应 skill。
3. 在每个 gate 阶段完成后，向用户汇报产物并等待确认。
4. 用户确认后先 `approve` 再 `advance`。
5. 用户拒绝则 `reject`，附带反馈，留在当前阶段重新迭代。
6. 遇到方向性错误时用 `back` 回退到之前阶段。
7. 长时间阶段用 `note` 记录进度，便于跨 session 恢复。

## CLI Tool

`workflow-ctl` is the state management backend. The agent calls it, not the user.

### Commands

```bash
# Start a new workflow
workflow-ctl start <template> <description>

# Show available templates
workflow-ctl templates [name]

# Show current state (human-readable or JSON)
workflow-ctl status [--json]

# Gate: approve current phase (requires --user-message with user's confirmation words)
workflow-ctl approve --user-message "用户说了什么"

# Gate: reject current phase (stays active, feedback appended to notes)
workflow-ctl reject [feedback text]

# Skip current phase entirely and advance
workflow-ctl skip [reason]

# Advance to next phase (requires approve first for gate phases)
# Validates output file exists unless --force
workflow-ctl advance [--output <artifact-path>] [--force]

# Roll back to previous phase (resets subsequent phases)
workflow-ctl back [steps]   # default: 1

# Append progress note to current phase
workflow-ctl note <text>

# Mark current phase as failed
workflow-ctl fail [reason]

# Retry the failed phase
workflow-ctl retry

# Pause / resume
workflow-ctl pause
workflow-ctl resume

# Plan tools
workflow-ctl plan-lint <plan.yml> [--json]
workflow-ctl plan-render <plan.yml> [output.md]
```

### Status --json output

```json
{
  "id": "wf-feature-1234567890",
  "template": "feature",
  "templateName": "Feature Development",
  "description": "user registration",
  "phases": [
    {
      "name": "brainstorm",
      "skill": "brainstorm",
      "gate": true,
      "status": "completed",
      "output": "design.md",
      "gateApproved": true,
      "approvedAt": "2026-04-18T10:00:00Z",
      "notes": ""
    },
    {
      "name": "spec",
      "skill": "spec",
      "gate": true,
      "status": "active",
      "gateApproved": false,
      "notes": "started writing user stories"
    },
    {
      "name": "plan",
      "skill": "plan",
      "gate": true,
      "status": "pending"
    },
    {
      "name": "implement",
      "skill": "implement",
      "gate": false,
      "status": "pending"
    }
  ],
  "currentPhase": 1,
  "status": "in_progress",
  "startedAt": "2026-04-18T09:30:00Z",
  "updatedAt": "2026-04-18T10:05:00Z",
  "artifactDir": ".workflow/artifacts/feature"
}
```

Note: fields with `omitempty` (output, gateApproved, approvedAt, notes) are omitted when empty on pending phases.
```

## ⚠️ Gate Phase Hard Rule (MANDATORY)

Gate phases (brainstorm, spec, plan) enforce approval with **two mechanical guards**:

1. **`--user-message` required**: `workflow-ctl approve` rejects calls without `--user-message`.
   This prevents self-approval. Pass the user's actual words:
   ```bash
   workflow-ctl approve --user-message "用户确认了设计方案"
   ```

2. **Required artifacts checked**: Each gate phase declares required files (e.g., `PLAN.yml`).
   `approve` fails if any are missing. Produce them before approving.

Flow:
```
agent produces artifacts → agent presents output to user → user confirms →
  workflow-ctl approve --user-message "用户说了什么" → workflow-ctl advance --output <path>
```

**Agent self-approval = critical violation.** Always show output to user first.

## Gate Phase Required Artifacts

| Phase | Required Artifacts |
|-------|-------------------|
| brainstorm | `design.md` |
| spec | `SPEC.md` |
| plan | `PLAN.yml` + `PLAN.md` |

## Templates

| Template | Flow | Gates |
|----------|------|-------|
| `feature` | brainstorm → spec → plan → implement | brainstorm ✓, spec ✓, plan ✓, implement ✗ |
| `bugfix` | triage → plan → implement | triage ✓, plan ✓, implement ✗ |
| `refactor` | assess → plan → implement → verify | assess ✓, plan ✓, implement ✗, verify ✓ |
| `spike` | brainstorm → document | brainstorm ✓, document ✓ |
| `hotfix` | implement | (no gates) |
| `security` | assess → plan → implement → verify | assess ✓, plan ✓, implement ✗, verify ✓ |

Gate phases require `approve` before `advance`. Non-gate phases proceed directly.

## Phase Behavior

### Brainstorm
- Skill: `brainstorm`
- Output: `design.md` in artifact dir
- Gate: ✓ — present design, user approves or requests changes.

### Spec
- Skill: `spec`
- Output: `SPEC.md` in artifact dir
- Gate: ✓ — present spec, user approves.

### Plan
- Skill: `plan`
- Output: `PLAN.yml` + `PLAN.md` in artifact dir
- Gate: ✓ — present task breakdown, user approves.
- Run `workflow-ctl plan-lint <PLAN.yml>` to validate before presenting.

### Implement
- Skill: `implement`
- Output: code changes + tests + `impl-report.md`
- Gate: ✗ — execute and report progress.
- For medium/large scope, use `implement` skill's team mode for parallel execution.
- Use `workflow-ctl note` to track progress during long implementations.

## Agent Workflow Loop

```
1. workflow-ctl start <template> "<description>"
2. loop:
   a. workflow-ctl status --json → get current phase, skill, gate, notes
   b. If notes exist → agent knows where it left off (cross-session recovery)
      c. ⛔ READ the corresponding skill's full SKILL.md (MANDATORY — do not rely on memory)
   d. Execute the skill, produce output in artifact dir
      - For implement phase specifically: follow the implement/SKILL.md flow
        (Pre-Flight → select execution mode → implement tasks → Per-Task Ritual)
      - ≥3 tasks = MUST use subagent mode (ag task + ag agent)
      - <3 tasks = direct execution is OK
   e. If phase has gate:
      - Present output to user
            - If user approves: workflow-ctl approve --user-message "用户原话" && workflow-ctl advance --output <path>
      - If user rejects: workflow-ctl reject "feedback" → iterate
   f. If phase has no gate:
      - Execute to completion, use note to track progress
      - workflow-ctl advance --output <path>
   g. If direction is wrong:
      - workflow-ctl back [steps] → re-do from earlier phase
   h. If workflow completed → report final results
3. On error:
   - workflow-ctl fail "reason"
   - workflow-ctl retry (after fixing)
```

## Cross-Session Recovery

When resuming a workflow in a new session:

1. `workflow-ctl status --json` → get full state
2. Check `notes` on the active phase — this tells you where you left off
3. Check `output` on completed phases — read these for context
4. Continue from where the notes indicate

Agent should write notes at meaningful checkpoints during long phases:

```bash
workflow-ctl note "completed group 1/3, tests passing"
workflow-ctl note "group 2/3 in progress, auth module done"
```

## Conversation Templates

### Starting a workflow

Present to the user:

```
## Workflow Started: {templateName}

**Description:** {description}
**Phases:** {phases}
**Artifacts:** {artifactDir}

Starting Phase 1: {name} (skill: {skill})
```

### Gate phase complete

```
## Phase Complete: {name}

**Output:** {artifactPath}

{summary}

Please review. Say "approve" to continue, or provide feedback for revision.
```

### User approves → `workflow-ctl approve --user-message "用户原话" && workflow-ctl advance --output <path>`

### User rejects → `workflow-ctl reject "user's feedback"` → iterate on the phase

### Phase failed

```
## Phase Failed: {name}

**Error:** {reason}

Options:
- "retry" — retry the current phase
- "back" — go back to a previous phase
- provide guidance — tell me what to do differently
```

## Artifacts

All artifacts go in the artifact directory (shown by `workflow-ctl status`):

```
.workflow/
├── STATE.json
├── AUDIT.jsonl
└── artifacts/
    └── <template>/
        ├── design.md
        ├── SPEC.md
        ├── PLAN.yml
        ├── PLAN.md
        └── impl-report.md
```

## Conversation-First Rules

1. 任何"开始/继续/暂停/恢复/状态查询/回退"都应可通过自然语言完成。
2. `workflow-ctl` is an internal tool — the agent calls it, never the user.
3. 当后台命令失败，向用户解释失败原因和下一步建议。
4. Gate 阶段必须向用户展示产物并获取明确确认后才 `approve` + `advance`。
5. 长时间阶段用 `note` 记录进度，便于恢复。
6. `back` 是安全的——后续阶段会被重置为 pending，已有产物引用保留在 `previousOutput` 字段。
7. `skip` 命令可以跳过不需要的阶段（如用户已有明确 spec），比连续 `advance` 更语义化。
8. 所有状态变更都会记录到 `AUDIT.jsonl`（append-only），便于调试和审计。

## ⛔ MANDATORY — Phase Entry Rules

**每次进入一个新 phase 时，必须执行以下步骤：**

- [ ] **重读技能文件** — 读取对应 skill 的完整 `SKILL.md`，不能凭记忆
- [ ] **检查前置产物** — 确认上一阶段的 output 文件存在且可读
- [ ] **确认理解** — 对用户简述即将做什么（一句话即可）

**这适用于所有 phase，包括 implement 和 verify（无 gate 的阶段更容易跳过这一步）。**

## ⛔ MANDATORY — Progress Tracking in Non-Gate Phases

implement 和 verify 等非 gate 阶段没有 approve 检查点，但**进度追踪不可省略**：

- 每完成一个有意义的工作单元（如一个 task、一组改动），必须调 `workflow-ctl note`
- note 内容应包含：任务编号/名称、完成状态、测试结果
- 向用户汇报当前进度（不要一口气做完才汇报）

```bash
# 正确 ✅
workflow-ctl note "Task 1/6 done: pkg/command/registry.go created, tests passing"
# 向用户: ✅ 1/6 done — registry.go. Next: server.go rewrite

# 错误 ❌ — 连续做完 6 个任务，最后一次 commit
```

## ⛔ MANDATORY — Self-Check Assertions

**在 workflow 执行过程中的任何时刻，如果以下任一条件为真，必须立即停下：**

| 断言 | 触发条件 | 修正动作 |
|------|----------|----------|
| 未读技能文件 | 进入新 phase 但没读对应 SKILL.md | 停下，读取完整 SKILL.md，再继续 |
| 进度未记录 | 非 gate 阶段完成了一个 task 但没调 `workflow-ctl note` | 补上 note |
| 跳过 gate | gate 阶段未经用户确认就 advance | 回退，向用户展示产物并等待确认 |
| 自审自批 | `approve` 的 `--user-message` 不是用户原话 | 使用用户实际说的话 |

**这些是对 agent 的硬约束，不是建议。**