# Workflow Commands

Agent-driven workflow execution with state persistence.

## Architecture

```
/workflow start feature "X"
    ↓
workflow-ctl start → write STATE.json
    ↓
Agent reads STATE.json → current phase
    ↓
Agent reads templates/feature.md → phase instructions
    ↓
Agent executes phase autonomously
    ↓
Agent calls workflow-ctl advance → update STATE.json
    ↓
Next phase...
```

**workflow-ctl Tool:**
- **Location:** `skills/workflow/bin/workflow-ctl`
- **Build:** `cd skills/workflow && go build -o bin/workflow-ctl workflow-ctl.go`
- **Purpose:** Deterministic state updates, agent-driven execution

**ag CLI:**
- **Location:** `skills/ag/` (separate skill)
- **Used for:** Agent spawn, task queue (for dynamic parallel tasks)

> **Note:** Agent controls execution flow, workflow-ctl manages state persistence.

## Commands

### `/workflow start <template> [description]`

Start a new workflow.

```bash
# Examples
/workflow start bugfix "Fix login timeout"
/workflow start feature "Add user authentication"
/workflow start hotfix "Emergency: prod crash"
```

**Implementation:**
```bash
workflow-ctl start bugfix "Fix login timeout"
```

### `/workflow status`

Show current workflow state.

```bash
workflow-ctl status
```

### `/workflow advance [--phase <name>]`

Advance to next phase (used by agent).

```bash
# Move to next phase (agent calls this after completing current phase)
workflow-ctl advance

# Jump to specific phase (rare, for recovery)
workflow-ctl advance --phase implement
```

### `/workflow pause` / `/workflow resume`

Pause or resume workflow.

```bash
workflow-ctl pause
workflow-ctl resume
```

## Agent Usage

Agent drives phase execution:

```bash
# 1. Start workflow
workflow-ctl start feature "add auth"

# 2. Agent reads STATE.json → current phase = spec
# 3. Agent executes Spec phase (reads templates/feature.md)
# 4. Agent calls:
workflow-ctl advance

# 5. Agent reads STATE.json → current phase = plan
# 6. Agent executes Plan phase (creates PLAN.md)
# 7. Agent calls:
workflow-ctl advance

# 8. Agent reads STATE.json → current phase = implement
# 9. Agent generates tasks.md
# 10. Agent creates tasks:
ag task create "implement API"
ag task create "write tests"
# 11. Agent spawns workers (fan-out pattern)
# 12. Agent calls:
workflow-ctl advance

# ... continue until done
```

## Dynamic Task Execution

For phases with parallel subtasks, agent uses `ag task`:

```bash
# Agent generates tasks.md

# Create tasks
ag task create "task-1 description"
ag task create "task-2 description"

# Spawn worker pool (fan-out.sh)
# Workers claim tasks: ag task claim <id>
# Workers complete: ag task done <id> --output <file>

# After all tasks done:
workflow-ctl advance
```

## State Management

All state is managed by `workflow-ctl` in `.workflow/`:

```
.workflow/
├── STATE.json                 # Current workflow state
└── artifacts/                 # Phase outputs
    ├── features/
    │   └── feature/
    │       ├── SPEC.md
    │       ├── PLAN.md
    │       ├── tasks.md
    │       └── implement-output.md
    └── bugfixes/
        └── bugfix/
            └── triage-output.md
```

## Custom Templates

To add custom templates:

```bash
# 1. Add to templates/registry.json
{
  "templates": {
    "my-template": {
      "name": "My Template",
      "description": "Custom workflow",
      "phases": ["step1", "step2", "step3"],
      "category": "custom"
    }
  }
}

# 2. Create template file
~/.ai/skills/workflow/templates/my-template.md

# 3. Use it
workflow-ctl start my-template "My custom workflow"
```

Template file format:
```markdown
---
id: my-template
name: My Template
phases: [step1, step2, step3]
---

## Phase 1: step1

### Goals
- [goal 1]
- [goal 2]

### Actions
1. [action 1]
2. [action 2]

### Output
Create `step1-output.md` with findings.

### Review Criteria
- [ ] criterion 1
- [ ] criterion 2

---

## Phase 2: step2
...
```