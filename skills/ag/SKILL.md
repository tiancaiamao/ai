---
name: ag
description: Agent orchestration runtime for other skills. 用户通过对话触发，agent 在后台使用 ag 命令执行。
---

# ag — Agent Orchestration CLI

## Overview

`ag` provides a unified interface for spawning, communicating, and coordinating AI agents. It wraps the low-level tmux + headless mode pattern into a clean CLI with additional infrastructure for multi-agent workflows.

## Conversation-First Positioning

`ag` 是**内部执行层**，不是用户主交互层。

- 用户接口：自然语言（由 `workflow` / `implement` 等 skill 承接）
- `ag` 接口：供 agent 在后台调用

默认规则：

1. 不要求用户手工输入 `ag` 命令来推进流程。
2. agent 负责把用户意图翻译为 `ag` 操作。
3. 仅在用户明确要求“显示底层命令”时，才暴露 CLI 细节。

### History: From subagent to ag

Previously, the `subagent` skill provided basic agent spawning via `start_subagent_tmux.sh`. However, it had limitations:
- No unified interface for agent lifecycle
- No built-in message passing
- No task management
- Hard to compose multiple agents

The `ag` CLI was created to address these limitations, providing:
- **Agent lifecycle**: spawn, wait, kill, rm, status
- **Communication**: channels, send/recv messages
- **Task management**: create/import, dependencies, next-claim, done/fail
- **Team runtime**: team-scoped state via `team init/use/done/cleanup`
- **Pattern scripts**: reusable multi-agent workflows (pair, parallel, pipeline, fan-out)

**The subagent skill is now deprecated.** Use `ag spawn` instead. The low-level `start_agent.sh` script is now part of `ag`'s internal implementation.

## Setup

```bash
export AG_BIN=~/.ai/skills/ag/ag
```

The binary is pre-built at `~/.ai/skills/ag/ag`. Source code lives in the project repo at `skills/ag/`.

To rebuild:

```bash
cd <project-repo>/skills/ag && go build -o ~/.ai/skills/ag/ag .
cp ag ~/.local/bin/ag  # optional: add to PATH
```

### Internal Implementation

Under the hood, `ag spawn` uses `internal/tmux/start_agent.sh`, which creates an isolated tmux session and runs `ai --mode headless`. This was previously the `subagent` skill, but is now an internal implementation detail.

**Direct use of `start_agent.sh` is discouraged.** Use `ag spawn` for all agent spawning needs.

If you need direct access to the low-level script (e.g., for debugging), it's at:

```bash
~/.ai/skills/ag/internal/tmux/start_agent.sh
```

## CLI Commands (Internal Reference)

### Agent Lifecycle

```bash
# Spawn an agent (runs in tmux, returns immediately)
$AG_BIN spawn --id my-agent --system prompt.md --input task.md --timeout 10m

# Spawn with mock (for testing patterns, no LLM)
$AG_BIN spawn --id test-agent --mock --mock-script /path/to/mock.sh --input input.txt

# Wait for agent to finish
$AG_BIN wait my-agent --timeout 600    # seconds

# Get output (only when done)
$AG_BIN output my-agent > result.md

# Check status
$AG_BIN status my-agent                 # spawning | running | done | failed | killed
$AG_BIN ls                              # list all agents

# Cleanup
$AG_BIN rm my-agent                     # remove completed/failed agent state
$AG_BIN kill my-agent                   # terminate running agent
```

### Communication

```bash
# Send message to an agent's inbox or a named channel
$AG_BIN send my-agent --file feedback.md
echo "hello" | $AG_BIN send my-agent
$AG_BIN send my-agent "inline message"

# Receive message (from channel or agent output)
$AG_BIN recv my-agent                   # non-blocking, fails if no messages
$AG_BIN recv my-agent --wait --timeout 60   # block until message arrives
$AG_BIN recv my-channel --all           # get all messages at once

# Channel management
$AG_BIN channel create review-queue
$AG_BIN channel ls
$AG_BIN channel rm review-queue
```

### Team Runtime

```bash
# Initialize and activate a team namespace
$AG_BIN team init feat-auth --description "Auth feature implementation"

# Show / switch active team
$AG_BIN team current
$AG_BIN team use feat-auth
$AG_BIN team list

# End / clean up a team
$AG_BIN team done
$AG_BIN team cleanup                # current team
$AG_BIN team cleanup --team feat-auth --force
```

All `ag` state (`agents/channels/tasks`) is scoped to the current team when a team is selected.

### Task Management

```bash
# Create tasks
$AG_BIN task create "Implement OAuth2"
$AG_BIN task create "Write tests" --file spec.md
$AG_BIN task import-plan .workflow/artifacts/features/feature/PLAN.yml

# Dependencies (DAG)
$AG_BIN task dep add T002 T001      # T002 depends on T001
$AG_BIN task dep ls T002
$AG_BIN task dep rm T002 T001

# Claim and complete
$AG_BIN task claim t001 --as worker-1    # atomic, fails if already claimed
$AG_BIN task next --as worker-2           # next pending, unblocked
$AG_BIN task done t001 --output result.md
$AG_BIN task fail t002 --error "blocked"

# Inspect
$AG_BIN task list                       # all tasks
$AG_BIN task list --status pending      # filter by status
$AG_BIN task show t001                  # full details
```

### Plan-Driven Team Execution (Recommended for workflow implement phase)

```bash
AG_BIN=~/.ai/skills/ag/ag
ART=.workflow/artifacts/features/feature

$AG_BIN team init feat-user-auth --description "feature implement"
$AG_BIN task import-plan "$ART/PLAN.yml" --spec "$ART/SPEC.md"
$AG_BIN task list

# Worker loop (manual or scripted):
# 1) claim next unblocked task
# 2) run implementer/spec-review/quality-review agents
# 3) mark done/fail
TASK=$($AG_BIN task next --as worker-1 | cut -f1)
echo "claimed $TASK"
```

## Pattern Scripts

Patterns are bash scripts in `~/.ai/skills/ag/patterns/`. They compose `ag` CLI commands into common multi-agent workflows.

### pair.sh — Worker-Judge Loop

One agent works, another reviews. Loop until approved.

```bash
$AG_BIN ~/.ai/skills/ag/patterns/pair.sh <worker-prompt> <judge-prompt> <input-file> [max-rounds]
```

**When to use:**
- Code review → fix → re-review
- Spec writing → quality check
- Any "produce → verify" cycle

**How it works:**
1. Round 1: spawn worker with input → get output → spawn judge with worker output
2. If judge says APPROVED → return worker output
3. If judge says REJECTED → feed judge feedback + original task back to worker
4. Repeat up to max-rounds

**Worker/Judge prompt conventions:**
- Worker prompt: "You are a [role]. Do [task]. Write output to stdout."
- Judge prompt: "You are a reviewer. Check [criteria]. End with APPROVED or REJECTED."

**Example:**
```bash
# Code review cycle
$AG_BIN ~/.ai/skills/ag/patterns/pair.sh \
  code-reviewer.md \      # reviews the code
  qa-reviewer.md \        # checks review quality
  changed-files.diff \    # input: the diff
  3                       # max rounds
```

### parallel.sh — N Agents in Parallel

Spawn multiple agents, each gets a unique index, collect all results.

```bash
$AG_BIN ~/.ai/skills/ag/patterns/parallel.sh <count> <system-prompt> <input-topic> [output-dir]
```

**When to use:**
- Explore multiple directories/approaches simultaneously
- Get diverse perspectives on a topic
- Partition work across agents

**How it works:**
1. Creates input for each agent with a unique index (0, 1, 2, ...)
2. Spawns all agents in parallel
3. Waits for all to complete
4. Collects results into output-dir/agent-{0,1,2,...}.md

**Example:**
```bash
# Explore 3 areas in parallel
$AG_BIN ~/.ai/skills/ag/patterns/parallel.sh \
  3 \                     # 3 agents
  explorer.md \           # system prompt
  "analyze the auth module" \
  /tmp/explore-results    # output dir
# Results in /tmp/explore-results/agent-0.md, agent-1.md, agent-2.md
```

### pipeline.sh — Sequential Stages

Each stage's output becomes the next stage's input.

```bash
$AG_BIN ~/.ai/skills/ag/patterns/pipeline.sh <input-file> <stage1-prompt> <stage2-prompt> ...
```

**When to use:**
- Spec → Plan → Implement (when no review loops needed)
- Transform chains (analyze → summarize → format)
- Any sequential processing

**Example:**
```bash
$AG_BIN ~/.ai/skills/ag/patterns/pipeline.sh \
  requirements.md \
  spec-writer.md \
  planner.md \
  implementer.md
```

### fan-out.sh — Task Queue + Worker Pool

Create tasks from a plan, workers claim and execute them in parallel, then merge.

```bash
$AG_BIN ~/.ai/skills/ag/patterns/fan-out.sh <plan-file> <worker-count> <worker-prompt> <merger-prompt>
```

**When to use:**
- Implement plan has N independent subtasks
- Running N test suites in parallel
- Any "split → execute → merge" pattern

**How it works:**
1. Creates one `ag task` per line in plan-file
2. Spawns worker-count workers
3. Each worker loops: claim task → spawn agent → wait → mark done/fail
4. After all tasks complete, spawns merger agent with all outputs

**Plan file format:** One task description per line. Lines starting with `#` are skipped.

**Example:**
```bash
cat > plan.txt << 'EOF'
# Feature: add login page
Implement the login form component
Add form validation
Write unit tests for login
EOF

$AG_BIN ~/.ai/skills/ag/patterns/fan-out.sh \
  plan.txt 3 worker.md merger.md
```

## Combining Patterns

Patterns can be nested or chained:

```bash
TMP=$(mktemp -d)

# Step 1: Explore in parallel
$AG_BIN ~/.ai/skills/ag/patterns/parallel.sh 3 explorer.md "the feature" $TMP/explore

# Step 2: Merge explores, then pair-write spec
cat $TMP/explore/agent-*.md > $TMP/all-explores.md
$AG_BIN ~/.ai/skills/ag/patterns/pair.sh spec-writer.md spec-reviewer.md $TMP/all-explores.md 3 > $TMP/spec.md

# Step 3: Plan from spec
$AG_BIN ~/.ai/skills/ag/patterns/pipeline.sh $TMP/spec.md planner.md > $TMP/plan.txt

# Step 4: Fan-out implementation
$AG_BIN ~/.ai/skills/ag/patterns/fan-out.sh $TMP/plan.txt 4 implementer.md integrator.md
```

## Important Notes

- **Always set AG_BIN** — `export AG_BIN=~/.ai/skills/ag/ag`
- **Always clean up** — `ag rm <id>` after getting output, or `.ag/` accumulates stale state
- **Agent IDs must be unique** — pair.sh auto-generates unique IDs per round
- **Mock mode** (`--mock`) for testing patterns without burning tokens
- **Timeout defaults** — spawn: 10m, wait in patterns: 60s. Override with `--timeout`
- **Working directory** — use `--cwd` to set the agent's working directory

## Migration from subagent Skill

If you have scripts or code that used the deprecated `subagent` skill, here's how to migrate:

### Old Way (subagent)

```bash
SESSION=$(~/.ai/skills/subagent/bin/start_subagent_tmux.sh \
  /tmp/output.txt \
  10m \
  @planner.md \
  "Task description")

SESSION_NAME=$(echo "$SESSION" | cut -d: -f1)
~/.ai/skills/tmux/bin/tmux_wait.sh "$SESSION_NAME" /tmp/output.txt 600
OUTPUT=$(cat /tmp/output.txt)
```

### New Way (ag)

```bash
ag spawn --id planner --system @planner.md --input "Task description" --timeout 10m
ag wait planner --timeout 600
OUTPUT=$(ag output planner)
ag rm planner
```

### Migration Benefits

| Aspect | subagent | ag |
|--------|-----------|-----|
| **Agent lifecycle** | Manual (tmux commands) | Built-in (spawn, wait, kill, rm) |
| **Status tracking** | None | `ag status`, `ag ls` |
| **Message passing** | Manual file I/O | `ag send`, `ag recv`, channels |
| **Task management** | None | `ag task create/claim/done` |
| **Error handling** | Manual | Automatic with status codes |
| **Cleanup** | Manual | `ag rm` handles everything |

### Advanced Migration: Channels

Old subagent patterns used files for inter-agent communication:

```bash
# Old way: use files
~/bin/start_subagent_tmux.sh /tmp/agent1/input.txt 10m @agent1.md "work"
~/bin/start_subagent_tmux.sh /tmp/agent2/input.txt 10m @agent2.md "review /tmp/agent1/output.txt"
```

New way: use channels

```bash
# New way: use channels
ag channel create review-queue
ag send worker-1 --file spec.md
ag send worker-1 "do this work"

# Worker receives messages
ag recv worker-1 --wait --timeout 60
```

### When to Use Internal Scripts

You might need `~/.ai/skills/ag/internal/tmux/start_agent.sh` directly if:

- **Debugging tmux behavior**: Check if the session is created correctly
- **Custom session management**: Need non-standard cleanup or monitoring
- **Integration testing**: Testing ag's internal implementation

**For all other cases, use `ag spawn`.**
