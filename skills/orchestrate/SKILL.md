---
name: orchestrate
description: Coordinate the full EDD workflow - explore → speckit → worker → review. Manage workflow state and progress tracking.
allowed-tools: [bash, read, write, edit, grep]
---

# Orchestrate Skill

Coordinate the Explore-Driven Development (EDD) workflow across multiple phases.

## Workflow Phases

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│   EXPLORE   │ ──▶ │   SPECKIT   │ ──▶ │   WORKER    │ ──▶ │   REVIEW    │
│  Discover   │     │   Plan      │     │   Implement │     │   Verify    │
└─────────────┘     └─────────────┘     └─────────────┘     └─────────────┘
```

## Orchestration Flow

### 1. Explore Phase
```bash
# Delegate to explore skill
~/.ai/skills/worker/bin/chain.sh \
    -p @explorer.md \
    -t 5m \
    "Explore codebase: find patterns" \
    "Summarize findings"
```

### 2. Speckit Phase
```bash
# Use speckit skill to create spec + plan
ai --skill speckit "feature: add user authentication"
```

### 3. Worker Phase
```bash
# Execute tasks in parallel
~/.ai/skills/worker/bin/parallel.sh \
    -n 2 \
    -p @worker.md \
    -t 15m \
    "Implement Task 1" \
    "Implement Task 2"
```

### 4. Review Phase
```bash
# Review all changes
~/.ai/skills/worker/bin/chain.sh \
    -p @reviewer.md \
    -t 10m \
    "Review code quality" \
    "Run integration tests"
```

## Workflow State File

Track progress in `.workflow/state.json`:

```json
{
  "phase": "worker",
  "tasks": {
    "total": 5,
    "done": 2,
    "failed": 0
  },
  "current_task": "Implement auth middleware",
  "started_at": "2024-01-15T10:00:00Z"
}
```

## Commands

### Start Workflow
```bash
orchestrate.sh --start "Add user authentication"
```

### Check Status
```bash
orchestrate.sh --status
```

### Resume Workflow
```bash
orchestrate.sh --resume
```

### Abort Workflow
```bash
orchestrate.sh --abort
```

## Error Handling

| Phase | On Failure |
|-------|-----------|
| Explore | Log findings, continue with known info |
| Speckit | Report spec issues, retry once |
| Worker | Mark task failed, continue next |
| Review | Report issues, request fixes |

## Progress Reporting

```
=== EDD Workflow ===
Phase: WORKER [████████░░] 80%

Tasks:
  [✓] Task 1: Setup project structure
  [✓] Task 2: Add database models  
  [▶] Task 3: Implement auth (in progress)
  [ ] Task 4: Add tests
  [ ] Task 5: Update docs

Estimated: 5 min remaining
```
