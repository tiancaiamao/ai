---
name: implement
description: Execute an implementation plan using subagent-driven development. Fresh subagent per task, two-stage review (spec compliance + code quality), commit after each group.
---

# Implement

Execute a PLAN.md by dispatching sub-agents for each task, with automatic
two-stage review after each.

**Core principle:** Fresh subagent per task + two-stage review = high quality.

## When to Use

- After plan is approved (from the `plan` skill)
- When user says "implement this plan" or "start coding"
- As Phase 4 of a feature workflow

## Input

- `PLAN.md` (required) — the implementation plan
- `PLAN.yml` (required) — machine-parseable plan (for task extraction)

## The Process

### Step 1: Load Plan

Read PLAN.md and PLAN.yml. Review critically:
- Any concerns about the plan? Raise them before starting.
- No concerns? Proceed to execution.

### Step 2: Select Execution Strategy

Based on plan size:

| Scope | Tasks | Strategy |
|-------|-------|----------|
| Small | 1-2 | Execute directly in this session |
| Medium | 3-6 | Group by group, sub-agent per task |
| Large | 7+ | Fan-out parallel within groups, sub-agent per task |

### Step 3: Execute Groups

For each group in `group_order`:

#### 3a. Spawn Implementer(s)

For each task in the group:

```bash
AG_BIN=~/.ai/skills/ag/ag

$AG_BIN spawn \
  --id "impl-T001" \
  --system "$(cat ~/.ai/skills/implement/prompts/implementer.md)" \
  --input "TASK: [full task text from plan]

CONTEXT: [where this fits, dependencies, architectural notes]

SPEC.md location: [path]
Working directory: [cwd]" \
  --timeout 15m
```

**Parallelism:** Tasks with no dependencies and different target files → spawn
all at once. Tasks that depend on each other or touch the same files → serial.

#### 3b. Wait for Implementers

```bash
$AG_BIN wait "impl-T001" --timeout 900
OUTPUT=$($AG_BIN output "impl-T001")
```

#### 3c. Stage 1 Review: Spec Compliance

After implementer reports, dispatch spec reviewer:

```bash
$AG_BIN spawn \
  --id "spec-review-T001" \
  --system "$(cat ~/.ai/skills/implement/prompts/spec-reviewer.md)" \
  --input "## What Was Requested
[full task requirements]

## What Implementer Claims
[implementer report from output]

Verify by reading the actual code at: [cwd]" \
  --timeout 10m

$AG_BIN wait "spec-review-T001" --timeout 600
SPEC_VERDICT=$($AG_BIN output "spec-review-T001")
```

**If CHANGES_REQUESTED →** Feed feedback back to implementer (same sub-agent
or new one), re-review. Max 2 rounds.

**If APPROVED →** Proceed to Stage 2.

#### 3d. Stage 2 Review: Code Quality

Only after spec compliance passes:

```bash
$AG_BIN spawn \
  --id "quality-review-T001" \
  --system "$(cat ~/.ai/skills/implement/prompts/quality-reviewer.md)" \
  --input "## What Was Implemented
[task description + implementer report]

Review the actual code at: [cwd]" \
  --timeout 10m

$AG_BIN wait "quality-review-T001" --timeout 600
QUALITY_VERDICT=$($AG_BIN output "quality-review-T001")
```

**If CHANGES_REQUESTED with medium+ severity →** Fix and re-review. Max 2 rounds.
**If APPROVED →** Proceed to commit.

#### 3e. Commit the Group

After both reviews pass:

```bash
git add -A
git commit -m "[group commit message from PLAN.yml]"
```

### Step 4: Run Tests

After all groups are implemented:

```bash
# Run full test suite
go test ./... -v
```

If tests fail, enter fix loop:
- Spawn fix agent with specific failure context
- Re-run tests
- Max 2 fix cycles; if still failing, report to user

### Step 5: Report

Summarize:
- Groups completed
- Tasks completed vs total
- Test results
- Any issues or concerns

## Error Handling

| Error | Recovery |
|-------|----------|
| Implementer times out | Retry once with increased timeout; report if still fails |
| Spec review fails 2 rounds | Report to user with diagnosis; don't auto-proceed |
| Quality review fails 2 rounds | Report to user; medium+ must be fixed, low can proceed |
| Tests fail after 2 fix cycles | Report to user; don't silently proceed |
| Sub-agent crashes | Retry once; report if persistent |

**Never silently skip a failure.** Always report to the user.

## Task Dispatch Template

When spawning implementer, fill in the prompt template:

```
TASK: [paste FULL task text from PLAN — don't make sub-agent read file]

CONTEXT:
- This is task [T001] in group "[group-name]"
- Dependencies: [list completed deps, or "none"]
- Related files: [from task's "file" field]
- Project: [brief description]
- Working directory: [absolute path]

SPEC.md: [path if needed for reference]
```

## Commit Conventions

```
feat(scope): add [feature]
fix(scope): resolve [issue]
refactor(scope): restructure [area]
test(scope): add tests for [feature]
docs: update [documentation]
```

## Anti-Patterns

- ❌ Don't implement directly — delegate to sub-agents
- ❌ Don't skip spec compliance review — "it looks fine" is not a review
- ❌ Don't skip quality review — spec compliance ≠ code quality
- ❌ Don't batch-review multiple tasks — review each task independently
- ❌ Don't trust implementer reports — verify by reading code
- ❌ Don't proceed on failure — report to user

## Skill Composition

```
plan → implement (this skill) → [commit → PR]
                                  or
                     direct plan → implement → [commit → PR]
```

## Integration with Other Skills

- **explore** — may be used before implementation if plan has gaps
- **review** — used for PR-level review after all tasks complete
- **test-driven-development** — implementer sub-agents should follow TDD
- **finishing-a-development-branch** — after implementation is complete