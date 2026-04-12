---
id: feature
name: Feature Development
description: Develop a new feature from spec to ship
phases: [spec, plan, implement, test, ship]
complexity: medium
estimated_tasks: 4-8
---

# Feature Development Workflow

## Overview

Feature workflow uses ag agents to execute each phase. Each phase spawns
specialized workers (agent) and optional reviewers (pair pattern).

## Phase 1: Spec

### Goals
- Clarify what we're building
- Define success criteria
- Identify constraints

### Execution: Spawn Worker Agent

Use `ag spawn` to create a spec worker agent:

```bash
ag spawn \
  --id "feature-spec-worker" \
  --system "You are writing a feature specification. Focus on clarity, user value, and testable requirements." \
  --input <(echo "Feature: $FEATURE_DESC") \
  --cwd "$PWD" \
  --timeout 10m
```

Wait for completion:
```bash
ag wait "feature-spec-worker" --timeout 600
```

Capture output:
```bash
ag output "feature-spec-worker" > .workflow/artifacts/features/SPEC.md
```

Cleanup:
```bash
ag rm "feature-spec-worker"
```

### Output

Create `SPEC.md` with content from worker agent output.

### Review: Use Pair Pattern

After spec is complete, use pair.sh for worker-reviewer loop:

```bash
# Worker: improve spec
# Reviewer: check spec quality
./ag/patterns/pair.sh \
  "You are reviewing a spec for clarity and completeness." \
  "You are checking if the spec meets requirements." \
  .workflow/artifacts/features/SPEC.md \
  3  # max rounds
```

If reviewer outputs `APPROVED`, advance to next phase.

---

## Phase 2: Plan

### Goals
- Break down into actionable tasks
- Estimate effort
- Identify dependencies

### Execution: Spawn Worker Agent

```bash
ag spawn \
  --id "feature-plan-worker" \
  --system "You are creating an implementation plan. Break down the feature into concrete tasks." \
  --input <(cat .workflow/artifacts/features/SPEC.md) \
  --cwd "$PWD" \
  --timeout 15m

ag wait "feature-plan-worker" --timeout 900
ag output "feature-plan-worker" > .workflow/artifacts/features/PLAN.md
ag rm "feature-plan-worker"
```

### Output

`PLAN.md` should contain task list for dynamic execution.

### Review: Use Pair Pattern

```bash
./ag/patterns/pair.sh \
  "You are reviewing an implementation plan for feasibility and completeness." \
  "You are checking if the plan addresses all spec requirements." \
  .workflow/artifacts/features/PLAN.md \
  3
```

---

## Phase 3: Implement

### Goals
- Execute tasks from PLAN.md
- Use parallel workers for efficiency

### Execution: Dynamic Task Queue

This phase uses `ag task` for parallel subtask execution.

#### Step 1: Create Tasks from PLAN.md

Read PLAN.md and create tasks:

```bash
# Example: Extract tasks and create them
ag task create "Implement API endpoint: POST /api/users"
ag task create "Write unit tests for user creation"
ag task create "Update database schema"
ag task create "Write API documentation"
```

#### Step 2: Spawn Worker Pool (Fan-Out Pattern)

Use fan-out.sh to spawn workers that claim tasks:

```bash
# Fan-out spawns N workers, each claiming pending tasks
# Each task gets its own agent (via spawn inside worker loop)

WORKER_PROMPT="You are implementing a task. Follow the task description precisely."
REVIEWER_PROMPT="You are reviewing code changes. Check for correctness, style, and edge cases."

./ag/patterns/fan-out.sh \
  .workflow/artifacts/features/PLAN.md \
  3 \  # number of parallel workers
  "$WORKER_PROMPT" \
  "$REVIEWER_PROMPT"
```

**How fan-out works internally:**
1. Reads tasks from PLAN.md (or pre-created via `ag task create`)
2. Spawns N worker agents
3. Each worker loops:
   - `ag task claim <id>` — claim a pending task
   - `ag spawn --id task-<id>` — spawn sub-agent to execute task
   - `ag wait` — wait for sub-agent
   - `ag task done <id> --output <file>` — mark task complete
4. When all tasks done, a merger agent collects results

#### Step 3: Or Use Pair Pattern for Sequential Tasks

If tasks depend on each other (not parallelizable), use pair.sh:

```bash
for task_desc in "implement API" "write tests" "update docs"; do
  echo "$task_desc" > task-input.md

  # Worker: implement task
  # Reviewer: review implementation
  ./ag/patterns/pair.sh \
    "You are implementing: $(cat task-input.md)" \
    "You are reviewing the implementation." \
    task-input.md \
    3

  # Check reviewer output for APPROVED
  if grep -q "APPROVED" reviewer-output.md; then
    # Task approved, move to next
  else
    # Fix and retry
  fi
done
```

### Output

All task outputs collected in `.workflow/artifacts/features/`.

### Review

After all tasks complete, spawn a final reviewer:

```bash
ag spawn \
  --id "feature-implement-reviewer" \
  --system "You are reviewing the complete feature implementation. Check against SPEC.md." \
  --input <(cat .workflow/artifacts/features/SPEC.md .workflow/artifacts/features/PLAN.md) \
  --cwd "$PWD" \
  --timeout 10m

ag wait "feature-implement-reviewer" --timeout 600
ag output "feature-implement-reviewer" > .workflow/artifacts/features/implement-review.md
ag rm "feature-implement-reviewer"
```

---

## Phase 4: Test

### Goals
- Run test suite
- Verify all requirements met

### Execution: Spawn Test Agent

```bash
ag spawn \
  --id "feature-test-worker" \
  --system "You are running tests and verifying the feature. Execute go test ./... and report results." \
  --input <(cat .workflow/artifacts/features/SPEC.md) \
  --cwd "$PWD" \
  --timeout 15m

ag wait "feature-test-worker" --timeout 900
ag output "feature-test-worker" > .workflow/artifacts/features/test-results.md
ag rm "feature-test-worker"
```

### Output

Test results and coverage report.

### Review: Use Pair Pattern

```bash
./ag/patterns/pair.sh \
  "You are reviewing test results. Check if all tests pass and coverage is adequate." \
  "You are verifying the feature works correctly based on SPEC.md." \
  .workflow/artifacts/features/test-results.md \
  2
```

---

## Phase 5: Ship

### Goals
- Clean commit history
- Create PR (if applicable)
- Update documentation

### Execution: Spawn Ship Agent

```bash
ag spawn \
  --id "feature-ship-worker" \
  --system "You are preparing to ship the feature. Squash commits, write PR description, update docs." \
  --input <(echo "Feature: $FEATURE_DESC") \
  --cwd "$PWD" \
  --timeout 10m

ag wait "feature-ship-worker" --timeout 600
ag output "feature-ship-worker" > .workflow/artifacts/features/ship-summary.md
ag rm "feature-ship-worker"
```

### Output

PR description or commit summary.

### Review

Final review before merging:

```bash
./ag/patterns/pair.sh \
  "You are reviewing the final shipping package. Check PR description and commit history." \
  "You are verifying the feature is ready to merge." \
  .workflow/artifacts/features/ship-summary.md \
  2
```

---

## Summary: ag Patterns Used

| Pattern | Usage | Phase |
|---------|--------|-------|
| `ag spawn` | Spawn phase worker | All phases |
| `ag wait` | Wait for agent completion | All phases |
| `ag output` | Capture agent output | All phases |
| `ag rm` | Cleanup agent | All phases |
| `ag task create` | Create subtasks | Implement |
| `ag task claim` | Worker claims task | Implement (fan-out) |
| `ag task done` | Mark task complete | Implement (fan-out) |
| `pair.sh` | Worker-reviewer loop | All phases (optional) |
| `fan-out.sh` | Parallel task execution | Implement |

## Key Principle

**Main agent is coordinator.** It:
1. Reads STATE.json → current phase
2. Reads template phase instructions
3. Spawns worker agents via `ag spawn`
4. Spawns reviewer agents via pair.sh
5. Spawns parallel workers via fan-out.sh + ag task
6. Advances phase via `workflow-ctl advance`

**Main agent does NOT** write code directly. It orchestrates sub-agents.