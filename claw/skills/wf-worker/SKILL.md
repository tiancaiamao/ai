---
name: wf-worker
description: "Start and supervise a subagent for one workflow worktree, maintain heartbeat, and update status transitions from todo/running to pr_open/failed/blocked."
allowed-tools: [bash, read, write, edit, grep]
---

# wf-worker

Run implementation work for one workflow item.

## Use When

- A workflow item is in `todo` and should start execution.
- A workflow item is in `running` and needs heartbeat/recovery.
- A workflow item is in `reviewing` and needs a fix pass.

## Required Inputs

- `workflow_id`
- `repo_path`
- `worktree`
- `issue_number`
- `branch`
- `task_prompt` (implementation or review-fix prompt)

## State Rules

- `todo -> running`: when worker starts.
- `running -> pr_open`: when branch is pushed and PR exists.
- `running -> failed`: process exits non-zero.
- `running -> blocked`: missing prerequisites or repeated failure.

## Worker Files

Inside worktree:

- `.aiclaw/status.json`
- `.aiclaw/result.json`
- `.aiclaw/worker.log`
- `.aiclaw/worker.pid`

Result schema:

```json
{
  "ok": true,
  "summary": "implemented feature and opened PR",
  "pr_number": 56,
  "pr_url": "https://github.com/owner/repo/pull/56",
  "next_state": "pr_open",
  "last_error": ""
}
```

## Procedure

1. Read current status.

- If state is `done`, exit without changes.
- If state is `blocked` and no explicit retry instruction, exit.

2. Update status to `running`.

- Set `step` to `implement` or `review_fix`.
- Set `started_at` if empty.
- Set `heartbeat_at` and `updated_at` to now.

3. Start subagent in worktree.

Use a single command shape (adjust prompt only):

```bash
cd "<worktree>" && \
ai --mode headless --no-session --subagent --max-turns 20 \
"You are implementing issue #<n>. <task_prompt>. Requirements: write progress heartbeat to .aiclaw/status.json before and after each major step; when finished, write .aiclaw/result.json with ok, summary, pr_number, pr_url, next_state, last_error."
```

If async supervision is needed, run in background and persist pid:

```bash
cd "<worktree>" && nohup ai --mode headless --no-session --subagent --max-turns 20 "..." > .aiclaw/worker.log 2>&1 & echo $! > .aiclaw/worker.pid
```

4. Heartbeat behavior.

- While process is running, refresh `heartbeat_at` at least once per tick.
- If no heartbeat for more than 10 minutes, treat as stale:
  - retry once by restarting subagent
  - otherwise set `failed`

5. Completion handling.

- If `.aiclaw/result.json` exists and `ok=true`:
  - copy `pr_number` and `pr_url` into status
  - set state to `pr_open` when PR exists
  - if no PR exists, keep `running` with `step=push_or_open_pr`
- If result `ok=false` or process exit != 0:
  - increment `retry_count`
  - set `failed` if retries exceeded; else keep `running` and retry

## Guardrails

- Do not modify global registry directly unless caller requested full reconcile.
- Always write `last_error` on failure.
- Never mark `done` from this skill.
- Do not remove worktree in this skill.
