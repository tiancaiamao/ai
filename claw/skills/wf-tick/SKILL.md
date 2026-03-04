---
name: wf-tick
description: "Cron-friendly scheduler tick: scan workflow registry, reconcile status with GitHub and process state transitions by delegating to wf-worker, wf-pr-review, and wf-closeout actions."
allowed-tools: [bash, read, write, edit, grep]
---

# wf-tick

Single reconciliation pass for all workflow items. Designed for `/cron` execution.

## Use When

- Called by cron every 1-2 minutes.
- User asks to manually run one scheduler cycle.

## Inputs

Optional runtime parameters (use defaults if missing):

- `stale_minutes` (default `10`)
- `max_retries` (default `2`)
- `target_workflow_id` (optional; reconcile only one item)

## Required Files

- `~/.aiclaw/workflows/registry.json`
- `<worktree>/.aiclaw/status.json` for each item

## Locking

Only one tick may run at a time.

- Acquire lock with atomic directory create:

```bash
mkdir ~/.aiclaw/workflows/.tick.lock
```

- If lock exists, exit quickly with `WF_TICK_SKIPPED_LOCKED`.
- Always release lock at the end.

## Reconciliation Order

For each registry item:

1. Load status file. If missing, mark `blocked` with error.
2. Reconcile issue/PR truth from GitHub.
3. Apply state transition rules.
4. Write status file.
5. Mirror summary state back to registry item.

## Transition Rules

### `todo`

- Action: invoke `wf-worker` start behavior.
- Target: `running`.

### `running`

- If `heartbeat_at` older than `stale_minutes`:
  - increment retry
  - restart worker if `retry_count <= max_retries`
  - else `failed`
- If PR exists and open: `pr_open`
- If PR merged: `done`

### `pr_open`

- Action: invoke `wf-pr-review` reconcile behavior.
- If merged: `done`
- If changes requested: `reviewing`

### `reviewing`

- Action: invoke `wf-pr-review` fix cycle.
- If fixes pushed and no blocking feedback: `pr_open`
- If retries exceeded: `failed`

### `done`

- Action: invoke `wf-closeout`.
- Remove from active registry when closeout succeeds.

### `failed`

- Keep terminal unless explicit retry command is present.

### `blocked`

- Keep terminal until manual unblocking.

## Idempotency Rules

- Multiple ticks must produce the same result if no external state changed.
- Never append duplicate registry entries.
- Never create multiple PRs for the same branch.
- Always reconcile with GitHub before changing `pr_open/reviewing/done`.

## Cron Prompt Template

Use this exact message for `/cron add` payload:

```text
Run wf-tick now. Reconcile ~/.aiclaw/workflows/registry.json, process all items idempotently, and output only a short machine-readable summary.
```

Expected summary format:

```text
WF_TICK_RESULT
scanned: <n>
updated: <n>
running: <n>
reviewing: <n>
done: <n>
failed: <n>
blocked: <n>
```

## Guardrails

- Never run destructive cleanup outside `done`.
- Never close issues from `wf-tick` directly; use closeout behavior.
- Never keep stale lock on normal exit; release lock in all branches.
