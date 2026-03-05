---
name: wf-tick
description: "Cron-friendly scheduler tick: scan workflow registry, reconcile status with GitHub, and process state transitions by delegating to wf-worker, wf-push, wf-pr-review, and wf-closeout."
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
- `no_worker` (default `false`; when `true`, do not invoke `wf-worker` or start subagents)
- `auto_review` (default `true`; when `false`, skip automated code review)

## Required Files

- `~/.aiclaw/workflows/registry.json`
- `<worktree>/.aiclaw/status.json` for each item

## Locking

Only one tick may run at a time.

```bash
mkdir ~/.aiclaw/workflows/.tick.lock
# If lock exists, exit quickly with WF_TICK_SKIPPED_LOCKED
# Always release lock at the end: rm -rf ~/.aiclaw/workflows/.tick.lock
```

## Reconciliation Order

For each registry item:

1. Load status file. If missing, mark `blocked` with error.
2. Reconcile issue/PR truth from GitHub.
3. Apply state transition rules.
4. Write status file.
5. Mirror summary state back to registry item.

## Transition Rules

### `todo`

- Normal mode (`no_worker=false`):
  - Action: invoke `wf-worker` start behavior.
  - Target: `running`.
- No-worker mode (`no_worker=true`):
  - Do not invoke `wf-worker`.
  - Update status:
    - `state=running`
    - `step=queued_no_worker`
    - refresh `heartbeat_at` and `updated_at`

### `running`

**Normal mode (`no_worker=false`):**

- Check heartbeat:
  - If `heartbeat_at` older than `stale_minutes`:
    - increment `retry_count`
    - restart worker if `retry_count <= max_retries`
    - else `state=failed`

- Check for implementation completion:
  - If `.aiclaw/result.json` exists and `ok=true`:
    - If result indicates `phase=done` or implementation is complete:
      - **Call wf-push to push branch and create PR**
      - Set `state=pr_open`, `step=awaiting_review`
    - If result indicates `partial=true`:
      - Resume from `next_phase` (invoke wf-worker again)

- Check PR status:
  - If PR exists and open: `state=pr_open`, `step=awaiting_review`
  - If PR merged: `state=done`

**No-worker mode (`no_worker=true`):**

- Do not restart worker and do not increment retry due to missing heartbeat.
- Do not invoke wf-push.
- Keep state as-is unless PR truth requires transition (`pr_open` / `done`).

### `pr_open`

- If `auto_review=true` and `step=awaiting_review`:
  - **Call wf-pr-review** (wait for CI, add LGTM, flag for human merge)
  - Review result determines next state:
    - If CI passing + LGTM added: keep `state=pr_open`, `step=ready_to_merge`
    - If CI not passing: keep `state=pr_open`, `step=waiting_ci`
    - If review comment requested changes: `state=reviewing`, `step=review_fix`
- If `auto_review=false`:
  - Keep `state=pr_open`, `step=awaiting_human_review`
  - Wait for human review decision

- Reconcile with GitHub PR state:
  - If merged: `state=done`
  - If review requested changes: `state=reviewing`

### `reviewing`

- Action: invoke `wf-worker` for fix pass (task: address review comments).
- If fixes pushed and no blocking feedback: `state=pr_open`
- If retries exceeded: `state=failed`

### `ready_to_merge`

- Keep `state=pr_open`, `step=ready_to_merge`
- Wait for human to merge the PR
- Reconcile with GitHub PR state:
  - If merged: `state=done`

### `done`

- Action: invoke `wf-closeout` (cleanup worktree, close issue).
- Remove from active registry when closeout succeeds.

### `failed`

- Keep terminal unless explicit retry command is present.

### `blocked`

- Keep terminal until manual unblocking.

## Key Workflow Pattern

**Fully automated task flow:**

```
todo
  → wf-tick detects todo
  → invoke wf-worker (subagent)
  → state=running

running
  → wf-worker completes implementation (ok=true)
  → wf-tick detects completion
  → invoke wf-push (push branch, create PR)
  → state=pr_open

pr_open
  → wf-tick invokes wf-pr-review (wait CI + LGTM + human merge)
  ↓
  ├─ CI passing + LGTM added → state=pr_open, step=ready_to_merge
  ├─ CI not passing → state=pr_open, step=waiting_ci
  └─ human review changes requested → state=reviewing

reviewing
  → invoke wf-worker fix pass
  → fixes pushed
  → state=pr_open → re-review
```

**Manual review flow (auto_review=false):**

```
pr_open
  → state=pr_open, step=awaiting_human_review
  → wait for human to post review
  → wf-tick reconciles with GitHub
  ↓
  ├─ approved + LGTM → state=pr_open, step=ready_to_merge (wait for human merge)
  └─ changes_requested → state=reviewing → wf-worker fix
```

## Idempotency Rules

- Multiple ticks must produce the same result if no external state changed.
- Never append duplicate registry entries.
- Never create multiple PRs for the same branch.
- Always reconcile with GitHub before changing `pr_open/reviewing/done/approved`.
- `wf-push` should check if PR already exists (idempotent).
- `wf-pr-review` should check if review already posted (idempotent).

## Cron Prompt Template

Use this exact message for `/cron add` payload:

```text
Run wf-tick now. Reconcile ~/.aiclaw/workflows/registry.json, process all items idempotently, and output only a short machine-readable summary.
```

Expected summary format:

```text
WF_TICK_RESULT
mode: normal|no_worker
auto_review: true|false
scanned: <n>
updated: <n>
running: <n>
pr_open: <n>
reviewing: <n>
ready_to_merge: <n>
done: <n>
failed: <n>
blocked: <n>
```

## Guardrails

- Never run destructive cleanup outside `done`.
- Never close issues from `wf-tick` directly; use closeout behavior.
- Never keep stale lock on normal exit; release lock in all branches.
- In `no_worker=true` mode, never invoke `wf-worker` and never start subagents.
- Always verify `wf-push` only runs when implementation is complete (ok=true).
- Never invoke `wf-push` on partial results (partial=true).
- In `auto_review=false` mode, allow human review workflow.
- Never auto-merge - always wait for human to merge PRs.