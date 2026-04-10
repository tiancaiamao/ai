---
name: subagent
description: Spawn isolated subagent processes for delegated tasks. Use for parallel execution, focused tasks, or breaking down complex problems.
allowed-tools: [bash]
---

# Subagent Skill

Spawn a subagent to handle delegated tasks. The subagent runs in an **isolated tmux session** using headless mode with a **focused system prompt**.

## ⚠️ CRITICAL RULES

```
🔴 RULE 1: NEVER use sleep, wait, or polling loops
   → Use tmux_wait.sh - it detects completion immediately
   → sleep 30 wastes 27s if subagent finishes in 3s

🔴 RULE 2: ALWAYS use tmux_wait.sh to wait for completion
   → Correct: tmux_wait.sh "$SESSION" /tmp/output.txt 600
   → Wrong: sleep 30 && cat output.txt
   → Wrong: while [ ! -f done ]; do sleep 1; done

🔴 RULE 3: ALWAYS use --timeout to prevent runaway subagents
   → Recommended: 5-10 minutes for most tasks

🔴 RULE 4: Use --system-prompt @file for focused persona
   → Load appropriate role from skill references

🔴 RULE 5: NEVER use --max-turns unless explicitly needed
   → Let subagents complete naturally

🔴 RULE 6: Set bash timeout to match subagent timeout
   → tmux_wait.sh respects timeout you pass (e.g., 600s)
   → But bash tool has its own default (120s)
   → For tasks >2min: pass "timeout" parameter to the script call
   → Example: start_subagent_tmux.sh -w /tmp/out.txt 10m ... → timeout=660
   → The extra margin handles startup overhead

🔴 RULE 7: Use cleanup policy intentionally
   → `-w` mode defaults to `--cleanup always` (prevents stale tmux sessions)
   → For debugging, override with `--cleanup never`
   → For background runs (no `-w`), cleanup defaults to `never`
```

## Common Mistakes (Read Before Starting!)

### Mistake 1: Using sleep to wait for subagent

```bash
❌ BAD:     sleep 30 && cat /tmp/output.txt
✅ GOOD:    tmux_wait.sh "$SESSION_NAME" /tmp/output.txt 30

Why: sleep wastes time if subagent finishes early (e.g., 3s). tmux_wait.sh
detects completion immediately via .done marker.
```

### Mistake 2: Wrong tmux_wait.sh parameters (missing output-file)

```bash
❌ BAD:     tmux_wait.sh "$SESSION" 900
✅ GOOD:    tmux_wait.sh "$SESSION" /tmp/output.txt 900

Why: The second parameter is output-file (REQUIRED), not timeout. Passing 900
treats it as a filename, causing confusion. The script now detects this common
mistake and shows an error message.
```

### Mistake 3: Sequential instead of parallel execution

```bash
❌ BAD:     Sequential execution (slow)
  SESSION=$(start_subagent_tmux.sh /tmp/out.txt 10m @persona.md "task")
  SESSION_NAME=$(echo "$SESSION" | cut -d: -f1)
  tmux_wait.sh "$SESSION_NAME" /tmp/out.txt 600
  # Subagent takes 60s to complete
  gh pr view $PR  # This could have run in parallel!

✅ GOOD:    Parallel execution (fast)
  SESSION=$(start_subagent_tmux.sh /tmp/out.txt 10m @persona.md "task")
  SESSION_NAME=$(echo "$SESSION" | cut -d: -f1)
  # Do independent work NOW while subagent runs
  CI_STATUS=$(gh pr view $PR --json statusCheckRollup)
  DIFF_SUMMARY=$(gh pr diff $PR | wc -l)
  tmux_wait.sh "$SESSION_NAME" /tmp/out.txt 600  # Wait here only

Time saved: If subagent takes 60s + other work 5s
- Sequential: 65s total (60s wait + 5s work)
- Parallel: 60s total (both run together)
```

### Mistake 4: Not checking context pressure before starting subagent

```bash
❌ BAD:     Start subagent without checking context
  SESSION=$(start_subagent_tmux.sh ...)

✅ GOOD:    Check context pressure first
  # Before starting, check <agent:runtime_state>
  # If tokens_percent >= 30%, call context_management first
  # This prevents stale outputs accumulation during long subagent runs
```

## Correct Command Template

```bash
# STEP 1: Start subagent in tmux (auto-captures session ID)
# NOTE: For tasks >2min, pass "timeout" parameter to avoid bash timeout
SESSION=$(~/.ai/skills/subagent/bin/start_subagent_tmux.sh \
  /tmp/subagent-output.txt \
  10m \
  /Users/genius/.ai/skills/workflow/orchestrate/references/explorer.md \
  "Your task description here")

# Extract session name (format: "session-name:session-id")
SESSION_NAME=$(echo "$SESSION" | cut -d: -f1)

echo "Started subagent: $SESSION_NAME"

# STEP 2: Wait for completion
# NOTE: For 10m tasks, use timeout=660 to handle bash default limit
~/.ai/skills/tmux/bin/tmux_wait.sh "$SESSION_NAME" /tmp/subagent-output.txt 600

# STEP 3: Collect results
cat /tmp/subagent-output.txt
```

## Parallel Execution Workflow (Best Practice)

When you have a subagent + independent tasks, run them in parallel to save time:

```bash
# STEP 1: Start subagent (it runs in background)
SESSION=$(start_subagent_tmux.sh /tmp/out.txt 10m @persona.md "task")
SESSION_NAME=$(echo "$SESSION" | cut -d: -f1)

# STEP 2: Do independent work NOW (don't wait!)
# These run in parallel with the subagent
CI_STATUS=$(gh pr view $PR --json statusCheckRollup)
DIFF_SUMMARY=$(gh pr diff $PR | wc -l)
ERROR_LOG=$(gh run view $RUN_ID --log-failed)

# STEP 3: Wait for subagent (only wait here)
tmux_wait.sh "$SESSION_NAME" /tmp/out.txt 600

# STEP 4: Process results together
RESULT=$(cat /tmp/review-result.json)
echo "CI Status: $CI_STATUS"
echo "Diff: $DIFF_SUMMARY lines"
echo "Review Result: $RESULT"
```

**Example Scenario: PR Review + CI Check**

```bash
# Start review subagent
cat > /tmp/task.txt << 'EOF'
Review PR #74. Focus on:
- Build errors
- Logic correctness
- Security issues
Write result to /tmp/review-74.json
EOF

SESSION=$(start_subagent_tmux.sh \
  /tmp/subagent-output.txt \
  15m \
  @reviewer.md \
  "Read task from /tmp/task.txt and follow instructions")

SESSION_NAME=$(echo "$SESSION" | cut -d: -f1)

# Parallel: Check CI status while review runs
PR_INFO=$(gh pr view 74 --json statusCheckRollup,title,headRefName)
BUILD_FAILURES=$(gh run view 23499957836 --log-failed | head -50)

# Now wait for review to complete
tmux_wait.sh "$SESSION_NAME" /tmp/subagent-output.txt 900

# Process both results together
REVIEW=$(cat /tmp/review-74.json)
echo "PR: $PR_INFO"
echo "Build Errors: $BUILD_FAILURES"
echo "Review: $REVIEW"
```

**Time Savings**:

| Task | Duration |
|------|----------|
| Subagent review | 60s |
| CI check | 5s |
| Diff analysis | 3s |
| **Total (sequential)** | **68s** |
| **Total (parallel)** | **60s** |

Saved 8s (12%) by running independent work in parallel!

**When to use parallel execution**:

- ✅ Subagent + gh pr view/gh run view (GitHub CLI calls)
- ✅ Subagent + file reading/analysis
- ✅ Subagent + git operations (git diff, git log)
- ✅ Multiple subagents (completely independent tasks)

**When NOT to use parallel execution**:

- ❌ Dependent tasks (need subagent result first)
- ❌ Heavy computation (would compete for resources)
- ❌ Tasks that modify the same files

## Wait for Fastest Subagent (Exploration/Research)

Use `wait_any_subagent.sh` when you have multiple subagents exploring options and want the fastest result:

```bash
# STEP 1: Start multiple subagents in parallel
SESSION1=$(start_subagent_tmux.sh /tmp/opt1.txt 5m @researcher.md "Research option A")
SESSION2=$(start_subagent_tmux.sh /tmp/opt2.txt 5m @researcher.md "Research option B")
SESSION3=$(start_subagent_tmux.sh /tmp/opt3.txt 5m @researcher.md "Research option C")

# STEP 2: Wait for first to complete
FIRST=$(~/.ai/skills/subagent/bin/wait_any_subagent.sh \
  subagent-1 subagent-2 subagent-3)

# STEP 3: Get result from the first
case $FIRST in
  subagent-1) BEST=$(cat /tmp/opt1.txt) ;;
  subagent-2) BEST=$(cat /tmp/opt2.txt) ;;
  subagent-3) BEST=$(cat /tmp/opt3.txt) ;;
esac

# STEP 4: Kill others (optional)
# tmux kill-session -t subagent-1
# tmux kill-session -t subagent-2
# tmux kill-session -t subagent-3

echo "Best option: $BEST"
```

**Use case: Researching multiple approaches**

- Option A: Use PostgreSQL for storage
- Option B: Use MongoDB for storage
- Option C: Use Redis for storage

Start 3 subagents researching each option, wait for first to complete (e.g., option A finishes in 120s), use that result, kill others.

**vs Wait for All:**

| Pattern | When to use | Example |
|---------|-------------|---------|
| **Wait for all** | Need all results | Implement user model + auth middleware + routes |
| **Wait for fastest** | Need best/first result | Research database options, use fastest |

## One-Liner with -w Flag

Use `-w` flag to start and wait in one command:

```bash
# ⚠️ IMPORTANT: Bash tool has 120s default timeout!
# For tasks expected to run >2min, you MUST set bash timeout parameter

# Short task (<2min): default bash timeout is fine
~/.ai/skills/subagent/bin/start_subagent_tmux.sh -w /tmp/out.txt 5m @persona.md "Quick task"

# Long task (>2min): set bash timeout to match subagent timeout + margin
# The "timeout" parameter in your tool call must exceed tmux_wait.sh timeout
~/.ai/skills/subagent/bin/start_subagent_tmux.sh -w /tmp/out.txt 10m @persona.md "Long task"
# ↑ tmux_wait.sh will wait 600s (10min)
# ↑ Bash tool default is 120s - MUST set "timeout": 660 in your tool call

# Keep tmux session for debugging (optional)
~/.ai/skills/subagent/bin/start_subagent_tmux.sh -w --cleanup never /tmp/out.txt 10m @persona.md "Debug task"
```

**Why this matters:**
- `start_subagent_tmux.sh -w` calls `tmux_wait.sh` internally
- `tmux_wait.sh` respects the timeout you pass (e.g., 600s for 10m)
- But bash tool has its own default timeout (120s)
- If bash kills the process before tmux_wait.sh finishes, you lose the result

## Interrupting Subagents

**With tmux, you have better control than interrupt files:**

```bash
# Method 1: Send Ctrl+C (graceful interrupt)
tmux send-keys -t subagent-1234567890 C-c

# Method 2: Kill session (force interrupt)
tmux kill-session -t subagent-1234567890

# Method 3: Check what's running first
tmux attach -t subagent-1234567890
# Then: Ctrl+C to interrupt
```

**Why this is better than interrupt files:**
- ✅ Immediate effect (no polling delay)
- ✅ Standard Unix signals
- ✅ Can attach and inspect before killing
- ✅ No race conditions

## Subagent Management

### List Running Subagents

```bash
# List all subagent tmux sessions
tmux ls | grep subagent

# Check if specific session exists
tmux ls | grep "subagent-123"
```

### View Subagent Output in Real-time

```bash
# Attach to session (Ctrl-b d to detach)
tmux attach -t subagent-1234567890

# Capture current output
tmux capture-pane -t subagent-1234567890 -p

# Capture last N lines
tmux capture-pane -t subagent-1234567890 -p -S -50
```

### Kill a Runaway Subagent

```bash
# Find the session
tmux ls | grep subagent

# Kill it
tmux kill-session -t subagent-1234567890
```

## When to Use

- **Parallel execution** of independent tasks
- **Complex problems** that need focused attention
- **Breaking down** large tasks into sub-tasks
- **Tasks requiring isolation** from main conversation context

## Command-Line Options

| Option | Description | Default |
|--------|-------------|---------|
| `--mode headless` | Run in headless mode (required) | - |
| `--timeout D` | Total execution timeout (e.g., `10m`, `300s`) | 0 (unlimited) |
| `--system-prompt @FILE` | Load persona from file | default headless prompt |
| `--tools T1,T2` | Comma-separated tool whitelist | all tools |
| `--max-turns N` | Maximum turns (avoid, use timeout) | 0 (unlimited) |

## Understanding Sessions

**Two types of sessions are involved:**

### 1. Tmux Session (Container)
- **Name**: `subagent-TIMESTAMP-RANDOM$$` (e.g., `subagent-1773995291-216285`)
- **Purpose**: Process isolation + observability
- **Operations**: `tmux attach/kill/ls`
- **Lifecycle**: Exists while process runs
- **Recovery**: `tmux ls | grep subagent`

### 2. AI Headless Session (Internal State)
- **ID**: UUID format (e.g., `cb76798b-445f-469f-9d02-1bf1464cd0a9`)
- **Purpose**: Conversation tracking + message storage
- **Storage**: `~/.ai/sessions/--<cwd>--/<session-id>/`
- **Lifecycle**: Persistent (survives restart)
- **Recovery**: `ls ~/.ai/sessions/--<cwd>--/`

### Hierarchy
```
tmux session (container)
  └─> ai --mode headless (process)
        └─> ai session (UUID)
              ├─> messages.jsonl
              └─> status.json
```

## Session Recovery After Restart

**Scenario**: Main agent restarts, needs to recover subagent information

### Find Tmux Sessions
```bash
# List all subagent tmux sessions
tmux ls | grep subagent

# Check if specific session still running
tmux ls | grep "subagent-1234567890"

# Attach to inspect
tmux attach -t subagent-1234567890
```

### Find AI Sessions
```bash
# List all ai sessions for current directory
ls -lt ~/.ai/sessions/--$(pwd | sed 's|/|-|g')--/

# Find most recent session
ls -td ~/.ai/sessions/--*--/*/ | head -1

# Read session messages
cat ~/.ai/sessions/--...--/<uuid>/messages.jsonl | jq .

# Check session status
cat ~/.ai/sessions/--...--/<uuid>/status.json | jq .
```

### Link Tmux and AI Sessions
```bash
# If you have tmux session name, find ai session:
tmux capture-pane -t subagent-1234567890 -p -S - | grep "Session ID:"

# If you have ai session ID, check if tmux session exists:
# (need to search through tmux sessions)
tmux ls | grep subagent | while read line; do
  sess=$(echo $line | cut -d: -f1)
  if tmux capture-pane -t $sess -p -S - | grep -q "<ai-session-id>"; then
    echo "Found: $sess"
  fi
done
```

### Resume Work After Restart
```bash
# 1. Check running subagents
tmux ls | grep subagent

# 2. Check recent ai sessions
ls -ltd ~/.ai/sessions/--*--/*/ | head -5

# 3. View session output
tmux attach -t subagent-xxxxx  # or
cat ~/.ai/sessions/--...--/<uuid>/messages.jsonl | jq -r '.content'

# 4. Check if subagent completed
# Look for done marker or check status.json
cat ~/.ai/sessions/--...--/<uuid>/status.json | jq .status
```

## Session Persistence

Sessions are saved to:
```
~/.ai/sessions/--<cwd>--/<session-id>/messages.jsonl
```

Useful for debugging subagent behavior after execution.

## Timeout Guidelines

| Task Type | Recommended Timeout |
|-----------|-------------------|
| Quick search/analysis | 5 minutes |
| Code review | 10 minutes |
| Multi-file refactoring | 15 minutes |
| Complex investigation | 15-30 minutes |

## Common Pitfalls

| Problem | Cause | Solution |
|---------|-------|----------|
| Subagent hangs | No timeout set | Add `--timeout 10m` |
| Process stuck | Unexpected error | `tmux kill-session -t <name>` |
| Can't find session | Wrong name | `tmux ls \| grep subagent` |
| Lost output | Output went to void | Redirect to file: `> /tmp/out.txt 2>&1` |
| **Wasted time waiting** | **Using sleep instead of tmux_wait.sh** | **Always use tmux_wait.sh** |
| **Session ID capture fails** | **Long task description** | **Write to file, pass file path** |
| **Resource leaks** | **Failed start / wait timeout leaves session** | **Use `-w` (default `--cleanup always`) or manual kill** |

## Resource Management

**Automatic Cleanup**:
- Startup failure (Session ID capture fails): auto-clean
- `-w` mode default (`--cleanup always`): auto-clean on both success and wait failure
- Optional policy: `--cleanup on-failure` or `--cleanup never`

```bash
# If Session ID capture fails, the script:
# 1. Outputs error message with full scrollback
# 2. Kills the tmux session it created
# 3. Exits with code 1

# In -w mode, timeout/unexpected exit can also auto-kill session
# (unless --cleanup never)
```

**Manual Cleanup** (if needed):

```bash
# List all subagent sessions
tmux ls | grep subagent

# Kill specific session
tmux kill-session -t subagent-1234567890

# Kill all subagent sessions (nuclear option)
tmux ls | grep subagent | cut -d: -f1 | xargs -I {} tmux kill-session -t {}
```

**Checking for Leaks**:

```bash
# Check for orphan tmux sessions
tmux ls | grep subagent

# Check for orphan processes
ps aux | grep "ai --mode headless" | grep -v grep

# Check for stale output files
ls -lt /tmp/*-output.txt | head -10
```

## Best Practices

- ✅ Always set `--timeout` to prevent runaway
- ✅ Use persona files via `--system-prompt @file`
- ✅ Run independent tasks in parallel with separate sessions
- ✅ Use `-w` for one-shot tasks to auto-clean sessions
- ✅ Keep sessions for debugging only when needed (`--cleanup never`)
- ✅ Use `tmux kill-session` to terminate stuck subagents
- ✅ Use `tmux capture-pane` to check progress
- ✅ **For long tasks (>200 chars), write to file and pass file path**
- ❌ Don't use `--max-turns` (use timeout instead)
- ❌ Don't nest subagents
- ❌ **Don't pass long text directly in command line**

## Handling Long Task Descriptions

**Problem**: Passing long task descriptions (>200 characters) directly can cause:
- Command line too long errors
- Shell quoting issues (single/double quotes in text)
- tmux send-keys failures
- start_subagent_tmux.sh unable to capture Session ID

**Solution**: Write task to file, pass file path

```bash
# ❌ BAD: Long task description directly
SESSION=$(start_subagent_tmux.sh \
  /tmp/output.txt 10m @reviewer.md \
  "Review PR #62. This is a very long description with multiple paragraphs, special characters like it's, \"quotes\", $variables, etc...")  # FAILS!

# ✅ GOOD: Write to file first
cat > /tmp/task.txt << 'EOF'
Review PR #62 (tmux-unified-task-management).

Key Changes:
- Removed interrupt file mechanism
- Fixed 4 critical bugs found by dogfooding

Focus on: correctness, performance, security
Write result to /tmp/review-62.json
EOF

SESSION=$(start_subagent_tmux.sh \
  /tmp/output.txt 10m @reviewer.md \
  "Read task from /tmp/task.txt and follow instructions")

SESSION_NAME=$(echo "$SESSION" | cut -d: -f1)
tmux_wait.sh "$SESSION_NAME" /tmp/output.txt 600
```

**Benefits**:
- ✅ No command line length limits
- ✅ No shell quoting issues
- ✅ Reliable Session ID capture
- ✅ Easier to debug (can view /tmp/task.txt)

## Output File Convention

When the main agent needs structured output from subagent:

```bash
# Main agent specifies output file in the prompt
SESSION=$(start_subagent_tmux.sh \
  /tmp/review-output.txt \
  10m \
  @reviewer.md \
  "Review this code. Write result to /tmp/review-result.json")

tmux_wait.sh "$(echo $SESSION | cut -d: -f1)" /tmp/review-output.txt 600

# Read the structured result
cat /tmp/review-result.json
```
