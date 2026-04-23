# Workflow Skill

Conversation-driven development workflow orchestration.

## Architecture

```
User (natural language) → Agent → workflow-ctl (state + audit) + skills (execution)
```

- **workflow-ctl** — Go CLI for state management with transition guards and audit logging
- **skills** — brainstorm, spec, plan, implement, explore — called by the agent per phase
- **implement skill** — Team mode for parallel subagent execution during implement phase (3+ tasks)

## Commands

```bash
workflow-ctl start <template> <description>   # Start workflow
workflow-ctl status [--json]                   # Show state
workflow-ctl approve                           # Approve gate (records timestamp)
workflow-ctl reject [feedback]                 # Reject gate (feedback appended to notes)
workflow-ctl advance [--output file] [--force] # Move to next phase (validates output file)
workflow-ctl skip [reason]                     # Skip current phase entirely
workflow-ctl back [steps]                      # Roll back (preserves previousOutput)
workflow-ctl note <text>                       # Append progress note
workflow-ctl fail [reason]                     # Mark phase failed
workflow-ctl retry                             # Retry failed phase
workflow-ctl pause / resume                    # Pause/resume
workflow-ctl templates [name]                  # List templates
workflow-ctl plan-lint <plan.yml> [--json]     # Validate plan (includes cycle detection)
workflow-ctl plan-render <plan.yml> [out.md]   # Render plan to markdown
```

## Gate Mechanism

Phases marked with `gate: true` in `registry.json` require:
1. Agent presents output to user
2. User approves → agent runs `approve` then `advance`
3. User rejects → agent runs `reject "feedback"` and iterates

`advance` will fail if:
- The current gate phase hasn't been approved (unless `--force`)
- The declared output file doesn't exist (unless `--force`)

`approve` records a timestamp in `approvedAt` for audit purposes.
`reject` appends feedback to the phase's notes (never overwrites).

## State Transition Guards

Phase status transitions are validated:
- `pending → active` (start, retry, back)
- `active → completed` (advance)
- `active → failed` (fail)
- `active → skipped` (skip)
- `failed → active` (retry)

Invalid transitions return an error immediately.

## Skip Command

`skip [reason]` marks the current phase as skipped and advances to the next.
Use this instead of double-advancing when a phase isn't needed (e.g., user
already has a spec). The reason is recorded in the phase notes.

## Back/Rollback

`back` rolls the workflow to a previous phase. All phases after the target
are reset to pending. Previous output references are preserved in the
`previousOutput` field so the agent can reference prior artifacts.

## Audit Log

All state changes are appended to `.workflow/AUDIT.jsonl` in JSON Lines format:
```jsonl
{"ts":"2025-01-25T10:00:00Z","event":"start","detail":"template=feature description=..."}
{"ts":"2025-01-25T10:05:00Z","event":"approve","phase":"brainstorm"}
{"ts":"2025-01-25T10:06:00Z","event":"reject","phase":"spec","detail":"too broad"}
{"ts":"2025-01-25T10:07:00Z","event":"advance","phase":"spec","detail":"output=SPEC.md"}
```

## Templates

| ID | Name | Phases |
|----|------|--------|
| feature | Feature Development | brainstorm → spec → plan → implement |
| bugfix | Bug Fix | triage → plan → implement |
| refactor | Refactor | assess → plan → implement → verify |
| spike | Spike | brainstorm → document |
| hotfix | Hotfix | implement |
| security | Security Audit | assess → plan → implement → verify |

## File Structure

```
workflow/internal/
├── main.go          # Cobra command registration
├── types.go         # State, Phase, Plan, LintIssue, AuditEvent types
├── config.go        # File I/O, registry loading, template resolution
├── state.go         # Transition guards, output validation
├── commands.go      # All command handlers (start, advance, approve, etc.)
├── plan.go          # Plan lint (cycle detection, deps, groups) + render
└── state_test.go    # Unit tests
```

## Building

```bash
cd ~/.ai/skills/workflow
go build -o $(go env GOPATH)/bin/workflow-ctl ./internal/
```

## Testing

```bash
go test ./internal/ -v
```

## Configuration

The skills path (where templates are loaded from) is resolved in order:
1. `WORKFLOW_SKILLS_PATH` environment variable
2. Binary-relative: walk up from the executable to find `templates/registry.json`
3. Fallback: `~/.ai/skills/workflow/`