---
name: wf-worker
description: "Meta-skill for orchestrating subagent execution for workflow tasks. Handles implementation, fix passes, and other workflow actions with phased execution, heartbeat tracking, and rate limit recovery."
allowed-tools: [bash, read, write, edit, grep]
---

# wf-worker

Meta-skill for orchestrating subagent execution for workflow tasks. Delegates to a subagent to perform specific actions like implementation, review fixes, or other workflow operations.

## Use When

- A workflow item is in `todo` and should start implementation.
- A workflow item is in `running` and needs heartbeat/recovery or resume from previous phase.
- A workflow item is in `reviewing` and needs a fix pass to address review comments.
- Any workflow step requires agent execution (implementation, fix, analysis, etc.).

## Required Inputs

- `workflow_id`
- `repo_path`
- `worktree`
- `task_type` (one of: `implement`, `fix_review`, `test`, `analysis`, etc.)
- `task_prompt` (specific prompt for the task)

## Optional Inputs

(None - always uses async execution with no turn limit)

## State Rules

- `todo -> running`: when worker starts for implementation.
- `running -> pr_open`: when branch is pushed and PR exists (via wf-push).
- `running -> failed`: process exits non-zero after retries exhausted.
- `running -> blocked`: missing prerequisites or repeated failure.
- `reviewing -> pr_open`: when fixes pushed and no blocking feedback.
- `reviewing -> failed`: repeated fix attempts fail.
- `running -> done`: when explicitly marked by workflow manager.

## Worker Files

Inside worktree:

- `.aiclaw/status.json` - Current status and phase tracking
- `.aiclaw/result.json` - Final result (or partial progress)
- `.aiclaw/worker.log` - Subagent execution log
- `.aiclaw/worker.pid` - Process ID (for async mode)

Status schema:

```json
{
  "workflow_id": "wf-001",
  "state": "running",
  "step": "implement",
  "phase": "analysis",
  "task_type": "implement",
  "started_at": "2026-03-05T19:00:00Z",
  "heartbeat_at": "2026-03-05T19:17:00Z",
  "updated_at": "2026-03-05T19:17:00Z",
  "retry_count": 0,
  "last_error": "",
  "metrics": {
    "input_tokens": 15000,
    "output_tokens": 5000,
    "total_tokens": 20000,
    "seconds_running": 300,
    "turn_count": 25,
    "tool_calls": 45
  }
}
```

Result schema (full success):

```json
{
  "ok": true,
  "partial": false,
  "summary": "implemented feature successfully",
  "phase": "done",
  "task_type": "implement",
  "next_state": "pr_open",
  "last_error": ""
}
```

Result schema (partial progress, e.g., rate limit hit):

```json
{
  "ok": false,
  "partial": true,
  "summary": "completed analysis and code changes, rate limit hit before PR creation",
  "phase": "push_or_open_pr",
  "task_type": "implement",
  "next_phase": "push_or_open_pr",
  "next_prompt": "Commit changes and create PR from branch feature/issue-16",
  "next_state": "running",
  "last_error": "API rate limit (429) exceeded at turn 27"
}
```

Result schema (fix pass success):

```json
{
  "ok": true,
  "partial": false,
  "summary": "Addressed all review comments and pushed fixes",
  "phase": "done",
  "task_type": "fix_review",
  "next_state": "pr_open",
  "last_error": ""
}
```

## Phased Execution Model

For complex tasks, break into independent phases. For simple tasks (like fix_review), single-phase execution is sufficient.

### Standard Implementation Phases

1. `analysis` - Read and analyze code
2. `implement` - Make code changes
3. `test` - Run tests and verify
4. `commit` - Commit changes
5. `push_or_open_pr` - Push and create PR (or just finalize)

### Fix Review Phases (simplified)

1. `parse_review` - Parse review comments
2. `implement_fixes` - Make required changes
3. `test_fixes` - Verify fixes
4. `push_fixes` - Push and update PR

### Single Phase Execution

For simple tasks like `fix_review`, single-phase execution is preferred:

```
task_type=fix_review
prompt="Address the following review comments: ..."
→ Single subagent call with sufficient turns
→ Complete result.json with ok=true
```

## Procedure

### 0. Load Configuration

Load global config for hooks and retry settings:

```bash
CONFIG_PATH="${HOME}/.aiclaw/workflows/config.json"

# Read hook commands
HOOK_BEFORE_RUN=$(jq -r '.hooks.before_run // ""' "$CONFIG_PATH")
HOOK_AFTER_RUN=$(jq -r '.hooks.after_run // ""' "$CONFIG_PATH")

# Read retry settings
MAX_RETRIES=$(jq -r '.retry.max_retries // 3' "$CONFIG_PATH")
RETRY_BASE_DELAY=$(jq -r '.retry.base_delay_ms // 10000' "$CONFIG_PATH")
RETRY_BACKOFF=$(jq -r '.retry.backoff_multiplier // 2' "$CONFIG_PATH")
RETRY_MAX_DELAY=$(jq -r '.retry.max_delay_ms // 120000' "$CONFIG_PATH")
```

### 0.5. Hook: before_run

Execute before_run hook if defined:

```bash
run_hook() {
  local hook_name="$1"
  local workspace="$2"
  local timeout_ms="${3:-60000}"
  
  if [ -z "$hook_name" ]; then
    return 0
  fi
  
  cd "$workspace"
  timeout "$timeout_ms" sh -lc "$hook_name" 2>/dev/null || true
}

# Execute before_run hook
if [ -n "$HOOK_BEFORE_RUN" ]; then
  run_hook "$HOOK_BEFORE_RUN" "<worktree>" 60000
fi
```

### 1. Read current status

```bash
cat "<worktree>/.aiclaw/status.json"
```

- If `state` is `done`, exit without changes.
- If `state` is `blocked` and no explicit `retry` flag, exit.
- If `phase` is set and `partial=true` in result, resume from that phase.

### 2. Update status to running

Update `.aiclaw/status.json`:

```json
{
  "workflow_id": "<workflow_id>",
  "state": "running",
  "step": "<task_type>",
  "phase": "<current_phase>",
  "task_type": "<task_type>",
  "started_at": "<previous_or_now>",
  "heartbeat_at": "<now>",
  "updated_at": "<now>",
  "retry_count": <incremented>,
  "last_error": ""
}
```

### 3. Determine execution mode

**Async mode** (recommended):
```bash
cd "<worktree>" && \
nohup ai --mode headless --no-session --subagent "<phase_prompt>" \
  > .aiclaw/worker.log 2>&1 & echo $! > .aiclaw/worker.pid
```

**Sync mode** (for simple/quick tasks):
```bash
cd "<worktree>" && \
ai --mode headless --no-session --subagent "<phase_prompt>" \
```

### 4. Generate task-specific prompts

#### Task Type: implement (new feature)

**Phase 1: analysis**
```
You are implementing issue #<issue_number> (Phase 1: Analysis).

Task: <task_prompt>

Your job:
1. Read and analyze relevant source code.
2. Identify files to modify.
3. Understand expected behavior.

Output:
- Create .aiclaw/analysis.json
- Update .aiclaw/status.json heartbeat_at

When complete, write .aiclaw/result.json:
{
  "ok": true,
  "partial": true,
  "phase": "analysis",
  "next_phase": "implement"
}
```

**Phase 2: implement**
```
You are implementing issue #<issue_number> (Phase 2: Implementation).

Task: Read .aiclaw/analysis.json and implement changes.

Your job:
1. Read analysis.json
2. Make code modifications
3. Run 'go build ./...' to verify

Output:
- Update .aiclaw/status.json heartbeat_at

When complete, write .aiclaw/result.json:
{
  "ok": true,
  "partial": true,
  "phase": "implement",
  "next_phase": "test"
}
```

#### Task Type: fix_review (address review comments)

**Single phase:**
```
You are fixing review comments for PR #<pr_number> (Issue #<issue_number>).

Task: <task_prompt>

Review comments to address:
<list of comments from review_result.json or gh pr view>

Your job:
1. Read the review comments carefully.
2. Make the necessary code changes.
3. Test the changes (build + run tests).
4. Commit the fixes.
5. Push the fixes.

Output:
- Update .aiclaw/status.json heartbeat_at after each major step

When complete, write .aiclaw/result.json:
{
  "ok": true,
  "partial": false,
  "summary": "Addressed all review comments and pushed fixes",
  "phase": "done",
  "task_type": "fix_review",
  "next_state": "pr_open"
}

Rate limit handling:
- If you encounter API rate limit, immediately write .aiclaw/result.json with partial=true.
- Include next_prompt for resumption.
```

### 5. Heartbeat and supervision

**For async mode:**

Start the subagent in background:
```bash
cd "<worktree>" && \
nohup ai --mode headless --no-session --subagent "<phase_prompt>" \
  > .aiclaw/worker.log 2>&1 & echo $! > .aiclaw/worker.pid
```

Monitor the log file:
```bash
tail -f "<worktree>/.aiclaw/worker.log"
```

Check if process is still running:
```bash
if ps -p $(cat "<worktree>/.aiclaw/worker.pid") > /dev/null 2>&1; then
  echo "Worker is running"
else
  echo "Worker has exited"
fi
```

**Heartbeat refresh:**
- If process is running, refresh `heartbeat_at` at least once per tick.
- If no heartbeat for more than 10 minutes, treat as stale:
  - Check if process is still alive
  - If yes, continue waiting
  - If no, check `.aiclaw/result.json` for partial result
  - Restart from appropriate phase if partial result exists
  - Otherwise, mark as `failed` if retries exceeded

### 6. Completion handling

When `.aiclaw/result.json` exists and `ok=true`:

- **Collect token metrics:**
  ```bash
  # Read metrics from result.json (if available)
  INPUT_TOKENS=$(jq -r '.metrics.input_tokens // 0' "<worktree>/.aiclaw/result.json")
  OUTPUT_TOKENS=$(jq -r '.metrics.output_tokens // 0' "<worktree>/.aiclaw/result.json")
  TOTAL_TOKENS=$(jq -r '.metrics.total_tokens // 0' "<worktree>/.aiclaw/result.json")
  TURN_COUNT=$(jq -r '.metrics.turn_count // 0' "<worktree>/.aiclaw/result.json")
  TOOL_CALLS=$(jq -r '.metrics.tool_calls // 0' "<worktree>/.aiclaw/result.json")
  
  # Calculate seconds running
  STARTED_AT=$(jq -r '.started_at // empty' "<worktree>/.aiclaw/status.json")
  if [ -n "$STARTED_AT" ]; then
    START_EPOCH=$(date -d "$STARTED_AT" +%s 2>/dev/null || echo 0)
    NOW_EPOCH=$(date -u +%s)
    SECONDS_RUNNING=$((NOW_EPOCH - START_EPOCH))
  else
    SECONDS_RUNNING=0
  fi
  ```
- Update status with metrics:
  ```bash
  jq --argjson input "$INPUT_TOKENS" \
     --argjson output "$OUTPUT_TOKENS" \
     --argjson total "$TOTAL_TOKENS" \
     --argjson turns "$TURN_COUNT" \
     --argjson calls "$TOOL_CALLS" \
     --argjson secs "$SECONDS_RUNNING" \
     '.metrics = {"input_tokens": $input, "output_tokens": $output, "total_tokens": $total, "turn_count": $turns, "tool_calls": $calls, "seconds_running": $secs}' \
     "<worktree>/.aiclaw/status.json" > /tmp/status.json && mv /tmp/status.json "<worktree>/.aiclaw/status.json"
  ```
- If `partial=false` (fully complete):
  - Copy `next_state` into status.
  - For `implement` task: wf-tick will invoke wf-push
  - For `fix_review` task: set `state=pr_open`
- If `partial=true` (phased progress):
  - Update status with current `phase`.
  - wf-tick will schedule next phase execution.

When `.aiclaw/result.json` exists and `ok=false`:

- Increment `retry_count`.
- If `retry_count >= MAX_RETRIES`:
  - Set `state="failed"`.
  - Copy `last_error` to status.
- Otherwise:
  - Calculate exponential backoff delay:
    ```bash
    # Exponential backoff: base * (multiplier ^ retry_count)
    retry_count=$(jq -r '.retry_count // 0' "<worktree>/.aiclaw/status.json")
    backoff_delay=$((RETRY_BASE_DELAY * (RETRY_BACKOFF ** retry_count) / 1000)))
    if [ $backoff_delay -gt $((RETRY_MAX_DELAY / 1000)) ]; then
      backoff_delay=$((RETRY_MAX_DELAY / 1000))
    fi
    echo "Retrying in ${backoff_delay}s (attempt $((retry_count + 1))/${MAX_RETRIES})"
    sleep $backoff_delay
    ```
  - Keep `state="running"`.
  - Retry from current or next phase.

### 6.5. Hook: after_run

Execute after_run hook after completion:

```bash
# Execute after_run hook (even on failure)
if [ -n "$HOOK_AFTER_RUN" ]; then
  run_hook "$HOOK_AFTER_RUN" "<worktree>" 60000
fi
```

## Integration with wf-tick

This skill is called by wf-tick in the following scenarios:

1. **todo → running**: Start new implementation
2. **running (stale)**: Resume or restart implementation
3. **running (partial)**: Continue from next_phase
4. **reviewing → pr_open**: Fix review comments
5. **Other task types**: Any workflow action requiring agent execution

wf-tick orchestrates the calls:
- Invokes wf-worker with appropriate `task_type` and `task_prompt`
- Waits for completion (async or sync)
- Reads result.json to determine next action
- For `implement` task complete: calls wf-push
- For `fix_review` task complete: sets `state=pr_open`

## Example Usage

### New implementation (multi-phase)
```bash
# wf-tick calls wf-worker
task_type=implement
task_prompt="Add /session command to show startup and working directory"

# Phase 1: analysis (30 turns)
nohup ai --mode headless --no-session --subagent \
  "Phase 1: analysis prompt..." \
  > .aiclaw/worker.log 2>&1 &

# After phase 1 complete, phase 2: implement
nohup ai --mode headless --no-session --subagent \
  "Phase 2: implement prompt..." \
  > .aiclaw/worker.log 2>&1 &

# ... continue phases 3, 4, 5

# After phase 5 done, wf-tick invokes wf-push
```

### Fix review comments (single phase)
```bash
# wf-tick calls wf-worker
task_type=fix_review
task_prompt="Address review comments from PR #17"

# Single-phase execution (40 turns for more complexity)
nohup ai --mode headless --no-session --subagent \
  "You are fixing review comments for PR #17.

Review comments:
- Line 42: Missing input validation
- Line 89: N+1 query pattern

Your job:
1. Fix the issues
2. Test with 'go build ./...'
3. Commit and push

Output result.json when done." \
  > .aiclaw/worker.log 2>&1 &

# After completion, state=pr_open
```

### Resume from rate limit
```bash
# Read partial result
cat .aiclaw/result.json

# Resume with adjusted prompt
nohup ai --mode headless --no-session --subagent \
  "Resume: branch already pushed, create PR for issue #16" \
  > .aiclaw/worker.log 2>&1 &
```

## Guardrails

- Do not modify global registry directly unless caller requested full reconcile.
- Always write `last_error` on failure.
- Never mark `done` from this skill (workflow manager does that).
- Do not remove worktree in this skill (use `wf-closeout` for that).
- Always handle rate limit gracefully with partial results.
- Max 3 retries per phase before marking as failed.
- Keep phase prompts independent and resumable.
- For `fix_review` tasks, prefer single-phase with higher turn limit.
- Always set `task_type` in result.json for workflow tracking.