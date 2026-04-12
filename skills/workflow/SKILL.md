---
name: workflow
description: |
  Multi-phase workflow system. Agent uses /workflow commands to execute structured
  development flows (feature/bugfix/refactor/spike) with spec/plan phases.
  State persisted to disk, phases executed via ag agents.
---

# Workflow - Multi-Agent Orchestration

## Core Philosophy

> **State on disk, progress visible, commits after each phase.**

Workflow is **execution engine** that turns plans into shipped code. It combines:
- **Workflow Templates**: Structured development flows (feature, bugfix, refactor, spike)
- **Auto Mode**: State-machine execution with persistence
- **Phase Review**: Human-in-the-loop checkpoints
- **ag Backend**: Agent orchestration primitives for phase execution

## Architecture

```
/workflow start bugfix "fix login timeout"
    ↓
Workflow Skill (Frontend)
  - /workflow commands (agent interface)
  - Template selection and loading
  - Phase prompt assembly
    ↓
ag CLI (Backend)
  - spawn: create isolated agent per phase
  - wait: block until phase completes
  - output: capture phase results
  - rm: cleanup agent state
```

**ag CLI:**
- **Location:** `skills/ag/` (separate skill with agent orchestration primitives)
- **Binary:** `~/.ai/skills/ag/ag`
- **Build:** `cd skills/ag && go build -o ag .`
- **Auto-build:** Binary is built automatically if missing

> **Note:** ag provides agent/channel/task primitives. workflow uses these to execute phases.

## Commands

| Command | Description |
|---------|-------------|
| `/workflow start [template] [description]` | Start a workflow |
| `/workflow auto [--no-review] [--phase <name>]` | Execute current workflow automatically |
| `/workflow status` | Show workflow state |
| `/workflow templates` | List available templates |
| `/workflow templates info <name>` | Show template details |
| `/workflow pause` | Pause auto mode |
| `/workflow resume` | Resume paused workflow |
| `/workflow stop` | Stop and cleanup |

## Quick Start

```bash
# Setup ag binary
cd skills/ag && go build -o ag .

# Start a bugfix workflow
/workflow start bugfix "fix login timeout"

# List all templates
/workflow templates

# Show template details
/workflow templates info feature

# Execute automatically
/workflow auto

# Check status
/workflow status
```

## Templates

| Template | Complexity | Use Case |
|----------|------------|----------|
| `feature` | Medium | New feature development |
| `bugfix` | Low | Bug fix with root-cause analysis |
| `refactor` | Medium | Code restructuring |
| `spike` | Low | Research/exploration |
| `hotfix` | Minimal | Emergency production fix |
| `security` | Medium | Security audit/fix |

## Feature Development: SPEC + PLAN

For feature work, the workflow includes spec and plan phases:

### Phase 1: SPEC (What)

Create `SPEC.md` defining **what** we're building:

```markdown
# Feature: [Name]

## Summary
[1-2 sentence description]

## Motivation
[Why are we doing this?]

## User Stories
- As a [user], I want [goal] so that [benefit]

## Requirements
- [ ] [requirement 1]
- [ ] [requirement 2]

## Out of Scope
- [ ] [explicitly not doing]

## Success Criteria
- [ ] [measurable criterion 1]
- [ ] [measurable criterion 2]
```

**Review criteria:**
- [ ] Clear user value?
- [ ] Scope bounded?
- [ ] Testable requirements?

### Phase 2: PLAN (How)

Read SPEC.md, explore codebase, create `PLAN.md` defining **how**:

```markdown
# Plan: [Feature]

## Technical Context
[Existing patterns, relevant files]

## Data Model
[Any new types or changes]

## API Design
[If applicable]

## Implementation Steps

### STEP-1: [Name]
**File:** `src/xxx.go`
**What:** [Brief description]
**Test:** [How to verify]

### STEP-2: [Name]
...

## Risks
- [risk] → [mitigation]

## Verification
[How to test the feature works]
```

**Review criteria:**
- [ ] All requirements addressed?
- [ ] Dependencies clear?
- [ ] Testable steps?

---

### Quick Start: Feature Development

```bash
# Start feature workflow
/workflow start feature "用户反馈功能"

# Phase 1: SPEC
# Create SPEC.md in artifact directory

# Phase 2: PLAN  
# Create PLAN.md in artifact directory

# Phase 3-5: Implement, Test, Ship
/workflow auto  # 自动推进
```

### Template Aliases

```bash
/workflow start bug "login timeout"    # → bugfix
/workflow start fix "typo"            # → bugfix
/workflow start feature "oauth"       # → feature
/workflow start research "api"        # → spike
/workflow start hot "production"     # → hotfix
```

## Workflow State

State is persisted to `.workflow/STATE.json`:

```json
{
  "template": "bugfix",
  "templateName": "Bug Fix",
  "description": "fix login timeout",
  "phases": [
    { "name": "triage", "index": 0, "status": "completed" },
    { "name": "fix", "index": 1, "status": "active" },
    { "name": "verify", "index": 2, "status": "pending" },
    { "name": "ship", "index": 3, "status": "pending" }
  ],
  "currentPhase": 1,
  "startedAt": "2025-03-27T10:00:00Z",
  "artifactDir": ".workflow/bugfixes/250327-1-login-timeout"
}
```

## Phase Execution

Each phase follows this pattern:

```
1. Load phase instructions from template
2. Build phase prompt (references/phase-worker.md + template phase section)
3. Spawn ag agent with system prompt + context input
4. Wait for phase completion
5. Optionally spawn reviewer agent (pair.sh pattern)
6. If approved: commit, advance to next phase
7. If failed: stop, report to user for retry
8. Cleanup agent state (ag rm)
```

## Integration with Other Skills

| Skill | Integration Point |
|-------|-------------------|
| `subagent` | Used for parallel phase/task execution |
| `tmux` | Background process management |
| `review` | Phase review after completion |

> **Note:** SPEC and PLAN phases are now built into the feature workflow template. No separate speckit step needed.

## State Files

| File | Purpose |
|------|---------|
| `.workflow/STATE.json` | Current workflow state |
| `.workflow/DECISIONS.md` | Key decisions made |
| `.workflow/notes/` | Phase notes and logs |
| `.workflow/bugfixes/*/` | Bugfix artifacts |
| `.workflow/features/*/` | Feature artifacts |

## Artifact Directory

Artifacts are stored in `.workflow/<category>/<date>-<num>-<slug>/`:

```
.workflow/
├── bugfixes/
│   └── 250327-1-login-timeout/
│       ├── STATE.json
│       ├── triage.md
│       ├── fix-notes.md
│       └── verify-results.md
└── features/
    └── 250326-1-user-auth/
        ├── STATE.json
        └── spec.md
```

## Error Handling

| Error | Recovery |
|-------|----------|
| Phase fails | Retry up to 3 times, then abort |
| User cancels | Save state, cleanup resources |
| Context overflow | Compact, continue |
| Subagent hangs | Timeout, kill, restart |

## Best Practices

1. **Start with templates** - Don't improvise workflows
2. **Use `/workflow auto`** - For fully automated execution
3. **Check `/workflow status`** - Before starting new work
4. **Commit after each phase** - Clean git history
5. **Keep artifacts** - For future reference

## Example: Full Bugfix Flow

```
User: /workflow start bugfix "login timeout after 30s"

Agent:
1. Load bugfix.md template
2. Create .workflow/bugfixes/250327-1-login-timeout/
3. Write STATE.json
4. Send workflow-start prompt with phases

Phase: Triage
→ Investigate issue, reproduce, identify root cause
→ Output: triage.md with findings
→ Review: approved
→ Commit: "fix(bugfix): complete triage"

Phase: Fix
→ Implement fix based on triage findings
→ Output: code changes
→ Review: approved
→ Commit: "fix(bugfix): implement fix"

Phase: Verify
→ Run tests, verify fix works
→ Output: verify-results.md
→ Review: approved
→ Commit: "fix(bugfix): verify fix"

Phase: Ship
→ Create PR, notify user
→ Commit: "fix(bugfix): complete"
→ Output: PR link

User: "Done! Bug is fixed."
```

## Anti-Patterns

❌ **Don't start without template** - Use `/workflow start feature` not ad-hoc
❌ **Don't skip phases** - Each phase has a purpose
❌ **Don't ignore failures** - Retry or abort, don't push broken code
❌ **Don't forget to commit** - Clean history helps future-you
