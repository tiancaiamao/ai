---
name: orchestrate
description: Multi-agent orchestration methodology. Use chain/parallel/loop patterns to coordinate multiple subagents for complex tasks. Current: phase-level execution with review loops.
allowed-tools: [bash, read, write, edit, grep]
---

# Orchestrate - Phase-Level Execution

**Orchestrate** executes speckit phases with review loops and automatic commits.

## Core Workflow

```
for each phase (SPECIFY → PLAN → TASKS → IMPLEMENT):
    while not approved (max 3 cycles):
        1. phase-worker: execute entire phase
        2. phase-reviewer: review phase output
        3. if APPROVED: commit, move to next phase
        4. if CHANGES_REQUESTED: loop back with feedback
```

## Key Difference from Task-Level

| Old (Task-Level) | New (Phase-Level) |
|------------------|-------------------|
| Review after EACH task | Review after ENTIRE phase |
| No commits during execution | Commit after each phase |
| Many review cycles | 4 review cycles max (one per phase) |

## Commands

```bash
# Run full workflow
~/.ai/skills/orchestrate/bin/orchestrate.sh

# Show status
~/.ai/skills/orchestrate/bin/orchestrate.sh status

# Execute next phase only
~/.ai/skills/orchestrate/bin/orchestrate.sh next

# Initialize workflow
~/.ai/skills/orchestrate/bin/orchestrate.sh init
```

## Phases

| Phase | Output | Worker Does | Reviewer Checks |
|-------|--------|-------------|-----------------|
| **SPECIFY** | spec.md | Gather requirements, write spec | Complete? Testable? |
| **PLAN** | plan.md | Read spec, explore, write plan | Addresses spec? Feasible? |
| **TASKS** | tasks.md | Break plan into tasks | Actionable? Complete? |
| **IMPLEMENT** | code | Execute all tasks | Tests pass? Criteria met? |

## Review Loop

Each phase goes through:

```
Cycle 1: worker executes → reviewer checks
         ↓ if CHANGES_REQUESTED
Cycle 2: worker fixes → reviewer checks
         ↓ if CHANGES_REQUESTED  
Cycle 3: worker fixes → reviewer checks
         ↓ if still not approved
Manual intervention needed
```

## Internal Personas

| Persona | Role | File |
|---------|------|------|
| **phase-worker** | Execute entire phase | `references/phase-worker.md` |
| **phase-reviewer** | Review phase completion | `references/phase-reviewer.md` |

## Output Format

### Phase-Worker Output

```
## Phase Complete: [PHASE_NAME]

**Output**: [files created/modified]
**Summary**: [what was done]

### Key Changes
- [change 1]
- [change 2]

### Verification
- [how verified]
```

### Phase-Reviewer Output

```json
PHASE_REVIEW_RESULT: {
  "status": "APPROVED" | "CHANGES_REQUESTED" | "FAILED",
  "phase": "SPECIFY" | "PLAN" | "TASKS" | "IMPLEMENT",
  "completed_well": ["..."],
  "blocking_issues": ["..."],
  "next_steps": "specific fixes needed"
}
```

## Commits

After each approved phase:

```bash
git add -A
git commit -m "feat(specify): complete specify phase"
git commit -m "feat(plan): complete plan phase"
git commit -m "feat(tasks): complete tasks phase"
git commit -m "feat(implement): complete implement phase"
```

## State Tracking

Workflow state in `.workflow/state.json`:

```json
{
  "phase": "PLAN",
  "status": "in_progress",
  "cycle": 1,
  "updated_at": "2024-01-15T10:30:00Z"
}
```

## Typical Workflow

```
1. /skill:explore      → context (optional)
2. /skill:brainstorming → decisions (optional)
3. /skill:speckit      → creates spec.md, plan.md, tasks.md
4. orchestrate.sh      → executes all phases with review loops
```

## Parallel Execution (Advanced)

For independent tasks in IMPLEMENT phase, you can parallelize manually:

```bash
# Start parallel workers for independent tasks
~/.ai/skills/subagent/bin/start_subagent_tmux.sh /tmp/task1.txt 10m @worker.md "Task 1"
~/.ai/skills/subagent/bin/start_subagent_tmux.sh /tmp/task2.txt 10m @worker.md "Task 2"

# Wait and collect
~/.ai/skills/tmux/bin/tmux_wait.sh session1 /tmp/task1.txt 600
~/.ai/skills/tmux/bin/tmux_wait.sh session2 /tmp/task2.txt 600
```

## Troubleshooting

### Phase stuck in loop
- Check `/tmp/orchestrate-review-*.txt` for feedback
- Max 3 cycles - then stops for manual intervention
- Attach to tmux session to see full context

### Commit fails
- Check if git is initialized
- Check for merge conflicts
- Commit is best-effort (won't fail the phase)

### Reviewer not parsing
- Ensure phase-reviewer outputs `PHASE_REVIEW_RESULT` JSON
- Check `/tmp/orchestrate-review-*.txt` for raw output

## vs Other Skills

| Skill | Focus | Commits? | Review Level |
|-------|-------|----------|--------------|
| **speckit** | Create spec/plan/tasks | No | None |
| **orchestrate** | Execute with review loops | Yes | Phase-level |
| **review** | Detailed code review | No | Code-level |

## Key Benefits

- ✅ **Phase-level review** - Not micro-managing each task
- ✅ **Automatic commits** - Clean history after each phase
- ✅ **Feedback loops** - Fix issues before moving on
- ✅ **Clean context** - Each subagent is isolated
- ✅ **Max 3 cycles** - Won't loop forever