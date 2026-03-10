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
- `~/.aiclaw/workflows/running.json` (for concurrency control)
- `<worktree>/.aiclaw/status.json` for each item

## Locking

Only one tick may run at a time.

```bash
mkdir ~/.aiclaw/workflows/.tick.lock
# If lock exists, exit quickly with WF_TICK_SKIPPED_LOCKED
# Always release lock at the end: rm -rf ~/.aiclaw/workflows/.tick.lock
```

## Concurrency Control

Check running slots before invoking wf-worker:

```bash
CONFIG_PATH="${HOME}/.aiclaw/workflows/config.json"
RUNNING_PATH="${HOME}/.aiclaw/workflows/running.json"

# Read config values
MAX_CONCURRENT=$(jq -r '.concurrency.max_concurrent // 3' "$CONFIG_PATH")
STALE_MINUTES=$(jq -r '.health.stale_minutes // 10' "$CONFIG_PATH")

# Read current running count
RUNNING_COUNT=$(jq '.running_items | length' "$RUNNING_PATH")

if [ "$RUNNING_COUNT" -ge "$MAX_CONCURRENT" ]; then
  echo "Max concurrent ($RUNNING_COUNT/$MAX_CONCURRENT) reached, skipping new tasks"
  # Skip invoking wf-worker for todo items
fi

# Function to acquire slot
acquire_slot() {
  local workflow_id="$1"
  local worktree="$2"
  
  RUNNING_COUNT=$(jq '.running_items | length' "$RUNNING_PATH")
  if [ "$RUNNING_COUNT" -ge "$MAX_CONCURRENT" ]; then
    return 1
  fi
  
  # Add to running_items
  jq --arg id "$workflow_id" --arg wt "$worktree" --arg now "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    '.running_items += [{"workflow_id": $id, "worktree": $wt, "started_at": $now}]' \
    "$RUNNING_PATH" > /tmp/running.json && mv /tmp/running.json "$RUNNING_PATH"
  return 0
}

# Function to release slot
release_slot() {
  local workflow_id="$1"
  jq --arg id "$workflow_id" \
    '(.running_items |= map(select(.workflow_id != $id)))' \
    "$RUNNING_PATH" > /tmp/running.json && mv /tmp/running.json "$RUNNING_PATH"
}

# Health check functions
check_process_alive() {
  local pid_file="$1"
  
  if [ ! -f "$pid_file" ]; then
    return 1
  fi
  
  local pid=$(cat "$pid_file")
  if ps -p "$pid" > /dev/null 2>&1; then
    return 0
  else
    return 1
  fi
}

check_stale() {
  local status_file="$1"
  local stale_minutes="$2"
  
  local heartbeat=$(jq -r '.heartbeat_at // empty' "$status_file")
  
  if [ -z "$heartbeat" ] || [ "$heartbeat" = "null" ]; then
    return 2  # No heartbeat = stale
  fi
  
  local heartbeat_epoch=$(date -d "$heartbeat" +%s 2>/dev/null || echo 0)
  local now_epoch=$(date -u +%s)
  local diff_minutes=$(( (now_epoch - heartbeat_epoch) / 60 ))
  
  if [ $diff_minutes -gt $stale_minutes ]; then
    return 1  # Stale
  fi
  
  return 0  # Healthy
}

# Process health check and recovery
check_worker_health() {
  local workflow_id="$1"
  local worktree="$2"
  local pid_file="$worktree/.aiclaw/worker.pid"
  local status_file="$worktree/.aiclaw/status.json"
  
  # 1. Check if process is alive
  if ! check_process_alive "$pid_file"; then
    echo "Worker for $workflow_id: process not running"
    
    # 2. Check if result exists
    if [ -f "$worktree/.aiclaw/result.json" ]; then
      echo "Worker for $workflow_id: result exists, will process"
      return 0  # Let completion handling deal with it
    else
      echo "Worker for $workflow_id: crashed without result"
      return 2  # Needs retry
    fi
  fi
  
  # 3. Check if stale (no heartbeat)
  if ! check_stale "$status_file" "$STALE_MINUTES"; then
    echo "Worker for $workflow_id: stale (no heartbeat for $STALE_MINUTES min)"
    return 3  # Stale but alive
  fi
  
  echo "Worker for $workflow_id: healthy"
  return 0  # Healthy
}
```

## Reconciliation Order

For each registry item:

1. **Validate worktree exists:**
   ```bash
   if [ ! -d "$worktree" ]; then
     echo "Worktree $worktree not found, removing from registry"
     # Remove from registry and skip
     continue
   fi
   ```

2. Load status file. If missing, mark `blocked` with error.
   ```bash
   if [ ! -f "$worktree/.aiclaw/status.json" ]; then
     echo "Status file missing for $workflow_id, marking blocked"
     # Mark as blocked, write status, continue
     continue
   fi
   ```

3. Validate status.json is valid JSON:
   ```bash
   if ! jq empty "$worktree/.aiclaw/status.json" 2>/dev/null; then
     echo "Corrupted status.json for $workflow_id, marking blocked"
     # Mark as blocked with error, continue
     continue
   fi
   ```

4. Reconcile issue/PR truth from GitHub.
5. Apply state transition rules.
6. Write status file.
7. Mirror summary state back to registry item.

## Subagent Invocation Commands

**重要**: wf-tick 通过调用其他技能来推进任务。以下是具体的调用命令。

### Invoke wf-worker (启动实现/修复)

```bash
# 1. 读取任务信息
ISSUE_NUMBER=$(jq -r '.issue_number // 0' "$worktree/.aiclaw/status.json")
ISSUE_URL=$(jq -r '.issue_url // ""' "$worktree/.aiclaw/status.json")
BRANCH=$(jq -r '.branch // ""' "$worktree/.aiclaw/status.json")

# 2. 获取 issue 内容作为任务描述
if [ -n "$ISSUE_URL" ]; then
  ISSUE_BODY=$(gh issue view "$ISSUE_NUMBER" --json title,body --jq '.title + "\n\n" + .body')
else
  ISSUE_BODY="Implement the task"
fi

# 3. 确定 task_type
# - implement: 首次实现
# - fix_review: 修复 review 问题
TASK_TYPE="implement"  # 或 "fix_review"

# 4. 后台启动 subagent (参考 /skill:subagent 最佳实践)
nohup ai --mode headless --subagent \
  --subagent-timeout 30m \
  --system-prompt "You are a focused implementer. Complete the task efficiently.
Work in directory: $worktree
Branch: $BRANCH

When done:
1. Run tests to verify
2. Commit changes
3. Write result to $worktree/.aiclaw/result.json with format:
{\"ok\": true, \"summary\": \"...\", \"next_state\": \"pr_open\"}" \
  "Task: $ISSUE_BODY

Working directory: $worktree
Branch: $BRANCH

Complete the implementation and write result.json when done." \
  > "$worktree/.aiclaw/worker.log" 2>&1 &

WORKER_PID=$!
echo $WORKER_PID > "$worktree/.aiclaw/worker.pid"

# 5. 更新状态
NOW=$(date -u +%Y-%m-%dT%H:%M:%SZ)
jq --arg pid "$WORKER_PID" \
   --arg now "$NOW" \
   --arg task_type "$TASK_TYPE" \
   '.state = "running" | .step = "implement" | .task_type = $task_type | .started_at = $now | .heartbeat_at = $now | .subagent.pid = ($pid | tonumber)' \
   "$worktree/.aiclaw/status.json" > /tmp/status.json && mv /tmp/status.json "$worktree/.aiclaw/status.json"

# 6. 占用 slot
acquire_slot "$workflow_id" "$worktree"

echo "Started worker for $workflow_id with PID $WORKER_PID"
```

### Invoke wf-push (推送 PR)

```bash
# 前置条件: result.json 存在且 ok=true
if [ -f "$worktree/.aiclaw/result.json" ] && [ "$(jq -r '.ok' "$worktree/.aiclaw/result.json")" = "true" ]; then
  
  BRANCH=$(jq -r '.branch // ""' "$worktree/.aiclaw/status.json")
  ISSUE_NUMBER=$(jq -r '.issue_number // 0' "$worktree/.aiclaw/status.json")
  REPO=$(jq -r '.repo // ""' "$worktree/.aiclaw/status.json")
  
  # 检查 PR 是否已存在
  EXISTING_PR=$(gh pr list --repo "$REPO" --head "$BRANCH" --json number --jq '.[0].number // empty')
  
  if [ -n "$EXISTING_PR" ]; then
    echo "PR #$EXISTING_PR already exists for branch $BRANCH"
    PR_NUMBER=$EXISTING_PR
  else
    # 推送分支
    cd "$worktree"
    git push -u origin "$BRANCH"
    
    # 创建 PR
    PR_URL=$(gh pr create --repo "$REPO" --head "$BRANCH" --base main \
      --title "[$(basename $BRANCH)] Implementation" \
      --body "Closes #$ISSUE_NUMBER")
    PR_NUMBER=$(echo "$PR_URL" | grep -oE '[0-9]+$')
    
    echo "Created PR #$PR_NUMBER"
  fi
  
  # 更新状态
  jq --arg pr "$PR_NUMBER" --arg url "https://github.com/$REPO/pull/$PR_NUMBER" \
    '.state = "pr_open" | .step = "created" | .pr_number = ($pr | tonumber) | .pr_url = $url' \
    "$worktree/.aiclaw/status.json" > /tmp/status.json && mv /tmp/status.json "$worktree/.aiclaw/status.json"
fi
```

### Invoke wf-pr-review (自动 Review)

```bash
# 参考 /skill:subagent 的最佳实践
PR_NUMBER=$(jq -r '.pr_number // 0' "$worktree/.aiclaw/status.json")
REPO=$(jq -r '.repo // ""' "$worktree/.aiclaw/status.json")

if [ "$auto_review" = "true" ] && [ "$PR_NUMBER" -gt 0 ]; then
  # 获取 PR diff
  gh pr diff "$PR_NUMBER" --repo "$REPO" > /tmp/pr_$PR_NUMBER.diff
  
  # 后台启动 review subagent
  (ai --mode headless --subagent \
    --subagent-timeout 15m \
    --system-prompt @/Users/genius/.ai/skills/review/reviewer.md \
    "Review PR #$PR_NUMBER: $(cat /tmp/pr_$PR_NUMBER.diff)" \
    > "$worktree/.aiclaw/review_result.txt" 2>&1) &
  
  REVIEW_PID=$!
  echo "Started review for PR #$PR_NUMBER with PID $REVIEW_PID"
  
  # 等待完成后处理结果...
fi
```

### Invoke wf-closeout (清理)

```bash
# 仅在 state=done 时调用
ISSUE_NUMBER=$(jq -r '.issue_number // 0' "$worktree/.aiclaw/status.json")
REPO=$(jq -r '.repo // ""' "$worktree/.aiclaw/status.json")

# 1. 关闭 issue (如果还没关闭)
ISSUE_STATE=$(gh issue view "$ISSUE_NUMBER" --repo "$REPO" --json state --jq '.state')
if [ "$ISSUE_STATE" != "CLOSED" ]; then
  gh issue close "$ISSUE_NUMBER" --repo "$REPO" --comment "Completed via workflow"
fi

# 2. 移除 worktree (可选，保留历史)
# cd "$repo_path" && git worktree remove "$worktree" --force

# 3. 从 registry 移除
jq --arg id "$workflow_id" \
  '(.items |= map(select(.workflow_id != $id)))' \
  ~/.aiclaw/workflows/registry.json > /tmp/registry.json && \
  mv /tmp/registry.json ~/.aiclaw/workflows/registry.json

# 4. 释放 slot
release_slot "$workflow_id"

echo "Closeout complete for $workflow_id"
```

## Transition Rules

### `todo`

- Normal mode (`no_worker=false`):
  - **Check concurrency first:**
    ```bash
    RUNNING_COUNT=$(jq '.running_items | length' "$RUNNING_PATH")
    if [ "$RUNNING_COUNT" -ge "$MAX_CONCURRENT" ]; then
      echo "Max concurrent ($RUNNING_COUNT/$MAX_CONCURRENT) reached, skipping"
      # Skip invoking wf-worker
    else
      # Acquire slot before invoking worker
      acquire_slot "$workflow_id" "$worktree"
      # Then invoke wf-worker
    fi
    ```
  - Action: invoke `wf-worker` start behavior.
  - Target: `state=running`.
- No-worker mode (`no_worker=true`):
  - Do not invoke `wf-worker`.
  - Update status:
    - `state=running`
    - `step=queued_no_worker`
    - refresh `heartbeat_at` and `updated_at`

### `running`

**Normal mode (`no_worker=false`):**

- **First, check worker health:**
  ```bash
  HEALTH_STATUS=$(check_worker_health "$workflow_id" "$worktree")
  case $HEALTH_STATUS in
    0)  # Healthy - process running normally
       echo "Worker $workflow_id: healthy"
       ;;
    1)  # Process dead but has result - will be handled below
       echo "Worker $workflow_id: process dead, result exists"
       ;;
    2)  # Process crashed without result - needs retry
       echo "Worker $workflow_id: crashed without result"
       retry_count=$(jq -r '.retry_count // 0' "$worktree/.aiclaw/status.json")
       if [ "$retry_count" -lt "$max_retries" ]; then
         # Increment retry_count and restart worker
         jq --argjson count $((retry_count + 1)) \
            '.retry_count = $count | .last_error = "worker crashed, restarting"' \
            "$worktree/.aiclaw/status.json" > /tmp/status.json && \
            mv /tmp/status.json "$worktree/.aiclaw/status.json"
         # Invoke wf-worker to restart
       else
         # Exhausted retries, mark as failed
         jq '.state = "failed" | .last_error = "exhausted retries after crash"' \
            "$worktree/.aiclaw/status.json" > /tmp/status.json && \
            mv /tmp/status.json "$worktree/.aiclaw/status.json"
       fi
       ;;
    3)  # Stale - process alive but no heartbeat
       echo "Worker $workflow_id: stale, no heartbeat"
       retry_count=$(jq -r '.retry_count // 0' "$worktree/.aiclaw/status.json")
       if [ "$retry_count" -lt "$max_retries" ]; then
         # Increment and restart
         jq --argjson count $((retry_count + 1)) \
            '.retry_count = $count | .last_error = "heartbeat stale, restarting"' \
            "$worktree/.aiclaw/status.json" > /tmp/status.json && \
            mv /tmp/status.json "$worktree/.aiclaw/status.json"
       else
         # Exhausted retries, mark as failed
         jq '.state = "failed" | .last_error = "exhausted retries due to stale"' \
            "$worktree/.aiclaw/status.json" > /tmp/status.json && \
            mv /tmp/status.json "$worktree/.aiclaw/status.json"
       fi
       ;;
  esac
  ```

- Check heartbeat:
  - If `heartbeat_at` older than `stale_minutes`:
    - increment `retry_count`
    - restart worker if `retry_count <= max_retries`
    - else `state=failed`

- Check for implementation completion:
  - If `.aiclaw/result.json` exists and `ok=true`:
    - **Release concurrency slot:**
      ```bash
      release_slot "$workflow_id"
      ```
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
  - **Call wf-pr-review** (wait for CI, self-review code, add LGTM or comment issues)
  - Review result determines next state:
    - If CI passing + AI added LGTM: `state=pr_open`, `step=ready_to_merge`
    - If CI not passing: `state=pr_open`, `step=waiting_ci`
    - If AI found issues and commented: `state=reviewing`, `step=review_fix_needed`
- If `auto_review=false`:
  - Keep `state=pr_open`, `step=awaiting_human_review`
  - Wait for human review decision

- Reconcile with GitHub PR state:
  ```bash
  # Check PR status using gh CLI
  PR_STATE=$(gh pr view "$pr_number" --repo "$repo" --json state -q '.state')
  
  # Also check if merged (state might be MERGED even if not showing as merged)
  IS_MERGED=$(gh pr view "$pr_number" --repo "$repo" --json merged -q '.merged')
  
  if [ "$PR_STATE" = "MERGED" ] || [ "$IS_MERGED" = "true" ]; then
    echo "PR #$pr_number has been merged"
    # Update status to done
    jq ".state = \"done\" | .step = \"merged\"" "$worktree/.aiclaw/status.json" > /tmp/status.json && \
      mv /tmp/status.json "$worktree/.aiclaw/status.json"
    # Will trigger closeout on next tick
  elif [ "$PR_STATE" = "CLOSED" ]; then
    echo "PR #$pr_number was closed without merging"
    jq ".state = \"failed\" | .last_error = \"PR closed without merging\"" "$worktree/.aiclaw/status.json" > /tmp/status.json && \
      mv /tmp/status.json "$worktree/.aiclaw/status.json"
  fi
  ```
  - If merged: `state=done`
  - If review requested changes: `state=reviewing`

- **Check for unaddressed review comments** (COMMENTED type counts as changes requested):
  ```bash
  # Get last addressed commit and timestamp from status.json
  LAST_ADDRESSED_COMMIT=$(jq -r '.last_addressed_commit // ""' "$worktree/.aiclaw/status.json")
  LAST_PUSH_TIME=$(jq -r '.last_push_time // ""' "$worktree/.aiclaw/status.json")
  
  # Fetch review comments (line-level comments)
  COMMENTS=$(gh api "repos/$repo/pulls/$pr_number/comments" --jq '.[]')
  
  # Filter to only new comments since last push
  NEW_COMMENTS=$(echo "$COMMENTS" | jq --arg last_time "$LAST_PUSH_TIME" \
    'select(.created_at > $last_time)')
  
  if [ -n "$NEW_COMMENTS" ]; then
    echo "Found new review comments since last push"
    jq ".state = \"reviewing\" | .step = \"review_fix_needed\" | .last_error = \"\"" \
      "$worktree/.aiclaw/status.json" > /tmp/status.json && mv /tmp/status.json "$worktree/.aiclaw/status.json"
  fi
  
  # Also check review-level comments
  REVIEWS=$(gh pr view "$pr_number" --repo "$repo" --json reviews --jq '.reviews[]')
  
  for review in $REVIEWS; do
    REVIEW_STATE=$(echo "$review" | jq -r '.state')
    REVIEW_SUBMITTED=$(echo "$review" | jq -r '.submitted_at // ""')
    AUTHOR=$(echo "$review" | jq -r '.author.login')
    
    # Skip if submitted before last push
    if [ -n "$LAST_PUSH_TIME" ] && [ "$REVIEW_SUBMITTED" \< "$LAST_PUSH_TIME" ]; then
      continue
    fi
    
    # CHANGES_REQUESTED from anyone means needs attention
    if [ "$REVIEW_STATE" = "CHANGES_REQUESTED" ]; then
      echo "Found CHANGES_REQUESTED from $AUTHOR"
      jq ".state = \"reviewing\" | .step = \"review_fix_needed\" | .last_error = \"\"" \
        "$worktree/.aiclaw/status.json" > /tmp/status.json && mv /tmp/status.json "$worktree/.aiclaw/status.json"
      break
    fi
    
    # COMMENTED type also triggers reviewing (for single-account workflow)
    if [ "$REVIEW_STATE" = "COMMENTED" ]; then
      REVIEW_BODY=$(echo "$review" | jq -r '.body // ""')
      if [ -n "$REVIEW_BODY" ] && ! echo "$REVIEW_BODY" | grep -qiE "^(lgtm|approved|looks good|👍|✓)"; then
        echo "Found COMMENTED review with actionable feedback from $AUTHOR"
        jq ".state = \"reviewing\" | .step = \"review_fix_needed\" | .last_error = \"\"" \
          "$worktree/.aiclaw/status.json" > /tmp/status.json && mv /tmp/status.json "$worktree/.aiclaw/status.json"
        break
      fi
    fi
  done
  ```
  - If merged: `state=done`
  - If review comments found: `state=reviewing`
- If no Worker ran (no_worker=true), still do the reconciliation above.

### `reviewing`

- Action: invoke `wf-worker` for fix pass (task: address review comments).
- If fixes pushed and no blocking feedback: `state=pr_open`
- If retries exceeded: `state=failed`

### `ready_to_merge`

- Keep `state=pr_open`, `step=ready_to_merge`
- Wait for human to merge the PR
- Reconcile with GitHub PR state:
  ```bash
  # Check PR status using gh CLI
  PR_STATE=$(gh pr view "$pr_number" --repo "$repo" --json state -q '.state')
  IS_MERGED=$(gh pr view "$pr_number" --repo "$repo" --json merged -q '.merged')
  
  if [ "$PR_STATE" = "MERGED" ] || [ "$IS_MERGED" = "true" ]; then
    echo "PR #$pr_number has been merged"
    jq ".state = \"done\" | .step = \"merged\"" "$worktree/.aiclaw/status.json" > /tmp/status.json && \
      mv /tmp/status.json "$worktree/.aiclaw/status.json"
  fi
  ```
  - If merged: `state=done`

### `done`

- **Invoke wf-closeout for cleanup:**
  ```bash
  # Call wf-closeout to cleanup worktree and close issue
  wf-closeout workflow_id="$workflow_id" worktree="$worktree" issue_number="$issue_number"
  
  # After closeout succeeds, remove from active registry
  if [ $? -eq 0 ]; then
    # Remove item from registry
    jq --arg id "$workflow_id" \
      '(.items |= map(select(.workflow_id != $id)))' \
      ~/.aiclaw/workflows/registry.json > /tmp/registry.json && \
      mv /tmp/registry.json ~/.aiclaw/workflows/registry.json
  fi
  ```
- Remove from active registry when closeout succeeds.

### `failed`

- Keep terminal unless explicit retry command is present.
- **Enable manual retry:**
  ```bash
  # Check if explicit retry flag is present in input or env
  if [ "$FORCE_RETRY" = "true" ] || [ -n "$RETRY_WORKFLOW_ID" ]; then
    if [ "$RETRY_WORKFLOW_ID" = "$workflow_id" ]; then
      echo "Manual retry requested for $workflow_id"
      # Reset retry_count and set state back to todo
      jq '.state = "todo" | .retry_count = 0 | .last_error = ""' \
         "$worktree/.aiclaw/status.json" > /tmp/status.json && \
         mv /tmp/status.json "$worktree/.aiclaw/status.json"
    fi
  fi
  ```

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