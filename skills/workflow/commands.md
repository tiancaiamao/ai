# Workflow Commands

User-friendly frontend for multi-phase agent execution via `ag`.

## Architecture

```
User Command
    ↓
Workflow Skill (Frontend)
  - Parse user input
  - Friendly commands
  - Template selection
  - Prompt composition
    ↓
ag CLI (Backend)
  - Agent spawning (ag spawn)
  - Phase execution (ag wait)
  - Output capture (ag output)
  - Cleanup (ag rm)
```

## Commands

### `/workflow start <template> [description]`

Start a new workflow.

```bash
# Examples
workflow.sh start feature "add user authentication"
workflow.sh start bugfix "fix login timeout"
workflow.sh start refactor "extract validation module"
workflow.sh start spike "evaluate caching strategies"
```

Templates available: `feature`, `bugfix`, `refactor`, `spike`, `hotfix`, `security`

### `/workflow auto [--no-review] [--phase <name>]`

Execute the current workflow automatically.

Each phase spawns an isolated `ag` agent that:
1. Receives phase-specific instructions
2. Gets context from previous phase outputs
3. Produces deliverables in the artifact directory
4. Optionally gets reviewed by a second agent

```bash
# Run all phases with review
workflow.sh auto

# Skip the reviewer
workflow.sh auto --no-review

# Run a specific phase
workflow.sh auto --phase implement
```

### `/workflow status`

Display workflow progress with phase status markers:
- ✓ completed
- ▶ active (current)
- ○ pending
- ✗ failed

### `/workflow next`

Show the next phase instructions without executing.

### `/workflow commit`

Git commit current phase with conventional commit message:
`feat(<template>): complete <phase> phase`

### `/workflow pause` / `/workflow resume`

Pause a running workflow. Resume picks up where you left off.

### `/workflow stop`

Stop and remove the workflow (prompts for confirmation).

### `/workflow templates [info <name>]`

List all available templates, or show details for a specific one.

## Custom Templates

Add to `templates/registry.json`:

```json
{
  "my-template": {
    "name": "My Custom Workflow",
    "description": "Custom workflow for my team",
    "phases": ["step1", "step2", "step3"],
    "category": "custom",
    "complexity": "medium",
    "aliases": ["custom"]
  }
}
```

Then create `templates/my-template.md` with phase sections:

```markdown
---
id: my-template
name: My Custom Workflow
phases: [step1, step2, step3]
---

## Phase 1: step1
### Goals
...

## Phase 2: step2
### Goals
...
```