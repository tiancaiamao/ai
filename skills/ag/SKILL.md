---
name: ag
description: Agent orchestration CLI. Use `ag` to spawn, communicate, and coordinate AI agents.
  Combine primitives into patterns: pair (worker-judge), parallel, pipeline, fan-out.
---

# ag — Agent Orchestration

## Setup

```bash
export AG_BIN=~/.ai/skills/ag/ag
```

The binary is pre-built. Source code lives in the project repo at `skills/workflow/ag/`.
If you need to rebuild:

```bash
cd <project-repo>/skills/workflow/ag && go build -o ~/.ai/skills/ag/ag .
```

## CLI Commands

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

### Task Management

```bash
# Create tasks
$AG_BIN task create "Implement OAuth2"
$AG_BIN task create "Write tests" --file spec.md

# Claim and complete
$AG_BIN task claim t001 --as worker-1    # atomic, fails if already claimed
$AG_BIN task done t001 --output result.md
$AG_BIN task fail t002 --error "blocked"

# Inspect
$AG_BIN task list                       # all tasks
$AG_BIN task list --status pending      # filter by status
$AG_BIN task show t001                  # full details
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
