# Workflow Skills for Daily Development

This document describes the workflow skill set designed for task orchestration with AiClaw cron jobs.

## Skill Set

- `wf-intake`
- `wf-tick`
- `wf-worker`
- `wf-pr-review`
- `wf-closeout`

## Shared State Files

Global registry:

- `~/.aiclaw/workflows/registry.json`

Per-worktree runtime state:

- `<worktree>/.aiclaw/status.json`

Tick lock:

- `~/.aiclaw/workflows/.tick.lock`

## Canonical States

- `todo`
- `running`
- `pr_open`
- `reviewing`
- `done`
- `blocked`
- `failed`

## State Machine

- `todo -> running` by `wf-worker`
- `running -> pr_open` when PR exists and is open
- `running -> failed` on retry exhaustion
- `pr_open -> reviewing` when changes are requested
- `pr_open -> done` when PR is merged
- `reviewing -> pr_open` after fix push with no blocking feedback
- `done -> closeout` by `wf-closeout`

## Recommended Cron Setup

Add one periodic tick job:

```bash
./bin/aiclaw cron add \
  -n "workflow-tick" \
  -m "Run wf-tick now. Reconcile ~/.aiclaw/workflows/registry.json, process all items idempotently, and output only a short machine-readable summary." \
  -e 120
```

This runs every 120 seconds.

## Typical Usage

1. Send a task to AiClaw and explicitly ask for `wf-intake` with required inputs:
   - `repo_path=/absolute/path/to/repo`
   - `repo=owner/repo`
   - `task=...`
   - `acceptance_criteria=...`
2. Verify issue/worktree/status creation output.
3. Let cron repeatedly run `wf-tick`.
4. Monitor transitions (`running`, `pr_open`, `reviewing`, `done`).
5. Let `wf-closeout` finish cleanup after merge.

Example intake message:

```text
Use wf-intake.
repo_path=/Users/genius/project/ai
repo=tiancaiamao/ai
task=Implement ...
acceptance_criteria=All tests pass and PR opened
```

`wf-intake` should return `WF_CREATED` only if worktree creation and verification succeeded.
If issue was created but worktree failed, it must return `WF_INTAKE_FAILED`.

## Operational Notes

- Keep transitions idempotent.
- Reconcile GitHub truth before state changes.
- Do not run destructive cleanup outside `done` state.
- Use absolute paths in JSON state files.
