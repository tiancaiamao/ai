---
name: wf-closeout
description: "Finalize completed workflow items: close issue, cleanup worktree/branches, and archive or remove registry entries."
allowed-tools: [bash, read, write, edit, grep]
---

# wf-closeout

Finalize one completed workflow item safely.

## Use When

- Workflow state is `done`.
- PR is merged and all required checks are complete.

## Required Inputs

- `repo` (owner/repo)
- `repo_path`
- `worktree`
- `issue_number`
- `branch`
- `workflow_id`

## Procedure

### 0. Load Configuration

Load global config for hooks:

```bash
CONFIG_PATH="${HOME}/.aiclaw/workflows/config.json"
HOOK_BEFORE_REMOVE=$(jq -r '.hooks.before_remove // ""' "$CONFIG_PATH")

run_hook() {
  local hook_name="$1"
  local workspace="$2"
  local timeout_ms="${3:-60000}"
  
  if [ -z "$hook_name" ]; then
    return 0
  fi
  
  # Worktree may not exist, run from parent directory
  timeout "$timeout_ms" sh -lc "$hook_name" 2>/dev/null || true
}
```

1. Verify merge state from GitHub.

- If PR is not merged, do not close out.
- If merge status cannot be confirmed, set `blocked` with error.

2. Close issue if still open.

```bash
gh issue view <issue> --repo <owner/repo> --json state
gh issue close <issue> --repo <owner/repo> --comment "Closed automatically after merge."
```

3. Cleanup git resources.

Execute before_remove hook before cleanup:

```bash
# Execute before_remove hook before worktree removal
if [ -n "$HOOK_BEFORE_REMOVE" ]; then
  run_hook "$HOOK_BEFORE_REMOVE" "<worktree>" 60000
fi
```

Then cleanup:

```bash
cd "<repo_path>"
git worktree remove "<worktree>"
git branch -d "<branch>" || true
```

Optional remote branch delete:

```bash
git push origin --delete "<branch>" || true
```

4. Update status file.

- Keep final snapshot in `<worktree>/.aiclaw/status.json` only if worktree still exists.
- Otherwise write final record to registry history before removing entry.

5. Update registry.

- Remove item from active `items`.
- Append to `history` (if field exists) with `closed_at` timestamp.

6. Return summary block.

```text
WF_CLOSED
workflow_id: ...
issue: #...
branch_removed: true|false
worktree_removed: true|false
```

## Guardrails

- Do not run if state is not `done`.
- Never delete default branch.
- Never fail closeout only because optional remote branch deletion failed.
