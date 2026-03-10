---
name: wf-push
description: "Finalize manual implementation: push branch, ensure PR exists, and persist workflow status/result so wf-tick can continue automatically."
allowed-tools: [bash, read, write, edit, grep]
---

# wf-push

Use this skill after manual implementation (main agent or human). It is a small, composable finalize action.

## Use When

- Work is implemented in an existing workflow worktree.
- You want a single step to push + create/reuse PR + persist `.aiclaw` state.
- You want `wf-tick` to continue from `pr_open` automatically.

## Required Inputs

At least one of:

- `worktree` (absolute path), or
- run in a directory that already contains `.aiclaw/status.json`.

Optional inputs (auto-detected from status/git when missing):

- `workflow_id`
- `repo` (`owner/repo`)
- `repo_path`
- `issue_number`
- `branch`
- `summary` (default: `manual implementation finalized and pushed`)
- `ensure_push` (default: `true`)
- `ensure_pr` (default: `true`)
- `update_status` (default: `true`)
- `update_registry` (default: `true`)

If required runtime fields cannot be resolved, return `WF_PUSH_FAILED`.

## Shared State Contract

Use existing workflow state files:

- `<worktree>/.aiclaw/status.json`
- `<worktree>/.aiclaw/result.json`
- `~/.aiclaw/workflows/registry.json`

Allowed next states set by this skill:

- `pr_open` (success path)
- `failed` (terminal failure when requested steps cannot complete)
- `blocked` (missing prerequisites)

## Procedure

1. Resolve working paths and metadata.

- Resolve `worktree`.
- Resolve status path: `<worktree>/.aiclaw/status.json`.
- Load metadata from status first; fallback to git/gh commands.

Commands:

```bash
cd "<worktree>" && pwd
cd "<worktree>" && git rev-parse --show-toplevel
cd "<worktree>" && git rev-parse --abbrev-ref HEAD
```

2. Preconditions.

- Ensure worktree exists.
- Ensure git branch is not detached.
- Ensure `gh` auth works before PR operations.

```bash
test -d "<worktree>"
cd "<worktree>" && git rev-parse --abbrev-ref HEAD
cd "<worktree>" && gh auth status
```

3. Push branch (`ensure_push=true`).

- If upstream exists: `git push`.
- If no upstream: `git push -u origin <branch>`.

```bash
cd "<worktree>" && git push
# fallback:
cd "<worktree>" && git push -u origin "<branch>"
```

4. Ensure PR (`ensure_pr=true`).

- First try from status (`pr_number`/`pr_url`) if present.
- Else query by head branch.
- If none exists, create one linked to issue.

```bash
cd "<worktree>" && gh pr list --head "<branch>" --repo "<owner/repo>" --json number,url,state --jq '.[0]'
cd "<worktree>" && gh pr create --repo "<owner/repo>" --head "<branch>" --fill
```

When issue number is known, include in PR body/title context (`Closes #<issue_number>` or equivalent).

5. Write `.aiclaw/result.json`.

Schema:

```json
{
  "ok": true,
  "summary": "manual implementation finalized and pushed",
  "pr_number": 56,
  "pr_url": "https://github.com/owner/repo/pull/56",
  "next_state": "pr_open",
  "last_error": ""
}
```

6. Update `.aiclaw/status.json` (`update_status=true`).

- Preserve existing fields when possible.
- Set:
  - `state = pr_open`
  - `step = awaiting_review`
  - `pr_number`/`pr_url`
  - `updated_at` and `heartbeat_at` to now (UTC ISO-8601)
  - `last_error = ""`
  - `subagent.pid = 0` (manual finalize path)
  - `last_push_time = now` (for multi-round review loop)
  - `last_addressed_commit = $(git rev-parse HEAD)` (track which commit addressed comments)

```bash
NOW=$(date -u +%Y-%m-%dT%H:%M:%SZ)
CURRENT_COMMIT=$(git rev-parse HEAD)

jq --arg now "$NOW" --arg commit "$CURRENT_COMMIT" \
   '.state = "pr_open" | .step = "awaiting_review" | 
    .updated_at = $now | .heartbeat_at = $now |
    .last_push_time = $now | .last_addressed_commit = $commit |
    .last_error = "" | .subagent.pid = 0' \
   .aiclaw/status.json > /tmp/status.json && mv /tmp/status.json .aiclaw/status.json
```

7. Upsert global registry (`update_registry=true`).

- Upsert by `workflow_id` (or by `issue_number` when `workflow_id` missing).
- Mirror summary fields: `repo`, `repo_path`, `issue_number`, `branch`, `worktree`, `state`, `updated_at`.
- Never append duplicates.

8. Final verification (required).

Before success response, verify:

- Branch is pushed to remote (or was already up to date).
- PR exists and is reachable.
- `<worktree>/.aiclaw/result.json` exists with `ok=true`.
- `<worktree>/.aiclaw/status.json` exists with `state=pr_open`.
- Registry contains updated item when `update_registry=true`.

9. Return summary.

Success:

```text
WF_PUSHED
workflow_id: ...
issue: #...
branch: ...
pr: #...
pr_url: ...
state: pr_open
```

Failure:

```text
WF_PUSH_FAILED
workflow_id: <or empty>
worktree: ...
failed_step: ...
error: ...
next_action: ...
```

## Guardrails

- Do not create a second PR for the same branch.
- Do not mark `done` in this skill.
- Do not remove worktree/branch in this skill.
- Always write `last_error` when failing after status is loaded.
- Keep this skill composable: if `ensure_push=false`, skip push cleanly; if `ensure_pr=false`, do not call gh PR create.
