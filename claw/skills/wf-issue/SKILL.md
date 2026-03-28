---
name: wf-issue
description: "Create a new work item from a user task: open issue, create worktree, initialize status files, and register the workflow item for cron scheduling."
allowed-tools: [bash, read, write, edit, grep]
---

# wf-issue

Create one workflow item that can be advanced by `wf-tick`.

## Required Inputs

- `repo_path` (required, absolute path)
- `repo` (required, `owner/repo`)
- `task` (required)
- `acceptance_criteria` (required)

If any required input is missing, stop and return `WF_ISSUE_FAILED`.

## Use When

- User gives a new development task.
- You need to convert task text into an issue + isolated worktree.

## Shared State Contract

Use these paths exactly:

- Global registry: `~/.aiclaw/workflows/registry.json`
- Global lock dir: `~/.aiclaw/workflows/.tick.lock`
- Per-worktree status: `<worktree>/.aiclaw/status.json`

Allowed state values:

- `todo`
- `running`
- `pr_open`
- `reviewing`
- `done`
- `blocked`
- `failed`

Per-worktree status schema:

```json
{
  "workflow_id": "wf-20260304-001",
  "repo": "owner/repo",
  "repo_path": "/abs/path/to/repo",
  "issue_number": 123,
  "issue_url": "https://github.com/owner/repo/issues/123",
  "branch": "feature/issue-123-short-slug",
  "worktree": "/abs/path/to/repo/.worktrees/issue-123",
  "state": "todo",
  "step": "issue",
  "heartbeat_at": "2026-03-04T10:00:00Z",
  "started_at": "",
  "updated_at": "2026-03-04T10:00:00Z",
  "pr_number": 0,
  "pr_url": "",
  "retry_count": 0,
  "max_retries": 2,
  "last_error": "",
  "subagent": {
    "pid": 0,
    "run_id": "",
    "last_exit_code": 0
  }
}
```

Registry schema:

```json
{
  "version": 1,
  "updated_at": "2026-03-04T10:00:00Z",
  "items": [
    {
      "workflow_id": "wf-20260304-001",
      "repo": "owner/repo",
      "repo_path": "/abs/path/to/repo",
      "issue_number": 123,
      "branch": "feature/issue-123-short-slug",
      "worktree": "/abs/path/to/repo/.worktrees/issue-123",
      "state": "todo",
      "updated_at": "2026-03-04T10:00:00Z"
    }
  ]
}
```

## Procedure

### 0. Load Configuration

Load global config for hooks:

```bash
CONFIG_PATH="${HOME}/.aiclaw/workflows/config.json"
HOOK_AFTER_CREATE=$(jq -r '.hooks.after_create // ""' "$CONFIG_PATH")

run_hook() {
  local hook_name="$1"
  local workspace="$2"
  local timeout_ms="${3:-60000}"
  
  if [ -z "$hook_name" ]; then
    return 0
  fi
  
  # Use subshell to avoid polluting current directory
  (cd "$workspace" && timeout "$timeout_ms" sh -lc "$hook_name") 2>/dev/null || true
}
```

1. Validate and enter repository.

- `repo_path` is mandatory. Do not infer it from current workspace.
- Every bash command in this skill MUST start with:

```bash
cd "<repo_path>" &&
```

- Verify repo path before any GitHub API or git worktree action:

```bash
cd "<repo_path>" && git rev-parse --show-toplevel
cd "<repo_path>" && git remote get-url origin
```

2. Ensure workflow directories exist.

```bash
mkdir -p ~/.aiclaw/workflows
```

3. Create issue from task description.

- Title format: `[WF] <short task title>`
- Body must include:
  - original request
  - acceptance criteria
  - branch naming rule
  - required status file path

Example:

```bash
gh issue create --title "[WF] <title>" --body-file /tmp/wf_issue_body.md
```

4. Extract issue number and build identifiers.

- `workflow_id`: `wf-<YYYYMMDD>-<issue_number>`
- `branch`: `feature/issue-<issue_number>-<slug>`
- `worktree`: `<repo_path>/<worktree_dir>/issue-<issue_number>`

5. Create worktree directory and branch.

Directory priority:

- `<repo>/.worktrees` if exists
- `<repo>/worktrees` if exists
- else create `<repo>/.worktrees`

Then:

```bash
cd "<repo_path>" && git worktree add "<worktree>" -b "<branch>"
```

If branch already exists, use:

```bash
cd "<repo_path>" && git worktree add "<worktree>" "<branch>"
```

6. Mandatory verification (do not skip).

All checks below must pass. If any check fails, return `WF_ISSUE_FAILED`.

```bash
cd "<repo_path>" && git worktree list --porcelain
test -d "<worktree>"
test -d "<worktree>/.git" || test -f "<worktree>/.git"
```

7. Initialize `<worktree>/.aiclaw/status.json` with state `todo`.

Also create runtime directory:

```bash
mkdir -p "<worktree>/.aiclaw"
```

7.5. Hook: after_create

Execute after_create hook after status file initialization:

```bash
# Execute after_create hook (only for newly created worktrees)
if [ -n "$HOOK_AFTER_CREATE" ]; then
  run_hook "$HOOK_AFTER_CREATE" "<worktree>" 60000
fi
```

8. Upsert entry into `~/.aiclaw/workflows/registry.json`.

- If the same `issue_number` already exists, update it instead of appending duplicate rows.

9. Final success verification (required).

Before returning success, verify:

- Issue exists and is reachable.
- Worktree exists on disk.
- `<worktree>/.aiclaw/status.json` exists and has `state=todo`.
- Registry contains this `workflow_id`.

10. Return result.

Success format (only after all verification checks pass):

```text
WF_ISSUE_CREATED
workflow_id: ...
issue: #...
issue_url: ...
branch: ...
worktree: ...
state: todo
```

Failure format (any step failed, including worktree create/verify):

```text
WF_ISSUE_FAILED
workflow_id: <or empty>
issue: #<or empty>
repo_path: ...
failed_step: ...
error: ...
next_action: ...
```

## Guardrails

- Always use absolute paths in JSON.
- Never create duplicate registry entries for the same issue.
- Never set `running` in issue stage; only `wf-worker` may do that.
- Never return `WF_ISSUE_CREATED` if worktree creation or verification failed.
- If issue exists but worktree fails, write a `blocked` status record with `last_error`.
- If issue exists but worktree fails, the response must still be `WF_ISSUE_FAILED`.
