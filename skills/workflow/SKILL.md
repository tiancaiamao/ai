---
name: workflow
description: |
  Multi-phase workflow execution using ag agents. Start workflows with /workflow start <template>,
  execute with /workflow auto. Each phase runs in an isolated ag agent, with optional review.
---

# Workflow — Multi-Agent Phase Execution

## Core Philosophy

> **State on disk, agents via ag, progress visible, commits after each phase.**

Workflow orchestrates multi-phase development tasks by spawning isolated `ag` agents for each phase.
Each agent gets the phase instructions + context from previous phases. An optional reviewer agent
validates output before advancing.

## Architecture

```
User Command
    ↓
workflow.sh (Frontend)
  - Parse commands, manage state in .workflow/STATE.json
  - Build prompts from templates + references
    ↓
ag CLI (Backend)
  - spawn: create isolated agent per phase
  - wait: block until phase completes
  - output: capture phase results
  - rm: cleanup agent state
```

## Setup

```bash
# ag binary must be available
export AG_BIN=~/.ai/skills/ag/ag   # or let workflow.sh auto-detect
```

## Commands

### `/workflow start <template> [description]`

Start a new workflow from a template.

```bash
workflow.sh start feature "add user authentication"
workflow.sh start bugfix "login timeout after 30s"
workflow.sh start refactor "extract validation logic"
workflow.sh start spike "evaluate GraphQL vs REST"
```

### `/workflow auto [--no-review] [--phase <name>]`

Execute phases automatically using ag agents.

- Spawns a **worker agent** for each phase with phase-specific instructions
- Optionally spawns a **reviewer agent** (pair pattern) to validate output
- Advances to next phase on success, stops on failure

```bash
workflow.sh auto                # Run all phases with review
workflow.sh auto --no-review    # Skip reviewer
workflow.sh auto --phase plan   # Run a specific phase only
```

### `/workflow status`

Show current workflow state with phase progress.

### `/workflow next`

Show instructions for the next phase.

### `/workflow commit`

Commit current phase changes with a conventional commit message.

### `/workflow pause` / `/workflow resume`

Pause or resume the workflow.

### `/workflow stop`

Stop and remove the workflow.

### `/workflow templates [info <name>]`

List or inspect available templates.

## Templates

| Template | Phases | Use When |
|----------|--------|----------|
| feature | spec → plan → implement → test → ship | New feature development |
| bugfix | triage → fix → verify → ship | Bug fixes with root-cause analysis |
| refactor | assess → plan → execute → verify → ship | Code restructuring |
| spike | research → document → present | Research & exploration |
| hotfix | identify → fix → deploy | Emergency production fix |
| security | scan → analyze → remediate → verify → document | Security audit |

## How It Works

### Phase Execution Flow

```
1. Read .workflow/STATE.json → current phase
2. Build phase prompt from:
   - references/phase-worker.md (base instructions)
   - templates/<template>.md (phase-specific instructions)
   - Previous phase outputs (context)
3. ag spawn worker → ag wait → ag output
4. If review enabled:
   ag spawn reviewer → ag wait → check APPROVED/CHANGES_REQUESTED
5. On success: save output, advance state
6. On failure: stop, report, let user fix
7. ag rm cleanup
```

### Prompt Composition

Each phase worker receives:
1. **Base instructions** from `references/phase-worker.md` — how to work, quality standards
2. **Phase instructions** extracted from the template markdown — specific tasks and outputs
3. **Context** — description, previous phase outputs
4. **Working directory** — artifact directory for outputs

The reviewer agent receives `references/phase-reviewer.md` as its system prompt.

## Creating Custom Templates

Add to `templates/registry.json`:

```json
{
  "templates": {
    "my-template": {
      "name": "My Template",
      "description": "What it does",
      "phases": ["step1", "step2", "step3"],
      "category": "custom",
      "complexity": "medium",
      "aliases": ["alt-name"]
    }
  }
}
```

Create `templates/my-template.md` with `## Phase N: <name>` sections.

## Files

```
workflow/
├── SKILL.md              — This file
├── commands.md           — Command reference
├── bin/workflow.sh       — Main CLI (calls ag)
├── templates/
│   ├── registry.json     — Template definitions
│   └── *.md              — Template phase instructions
├── references/
│   ├── phase-worker.md   — Worker agent system prompt
│   └── phase-reviewer.md — Reviewer agent system prompt
└── prompts/
    └── workflow-start.md — Start prompt template
```

## Anti-Patterns

❌ **Don't start without template** — Use `workflow.sh start feature`
❌ **Don't skip phases** — Each phase builds on the previous
❌ **Don't ignore failures** — Fix issues before re-running auto
❌ **Don't forget to commit** — `workflow.sh commit` after each phase
❌ **Don't run without ag** — Ensure `ag` binary is built and accessible