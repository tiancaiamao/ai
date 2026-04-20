---
name: ag
description: Provide subagent and agent orchestration runtime. Spawns AI agents as detached processes with bridge-per-agent architecture, supports mid-turn steering, structured status, real-time observability via stream.log, task DAG scheduling, and inter-agent channels.
---

# ag — Agent Orchestration CLI

## Overview

`ag` orchestrates AI agents using a **bridge-per-agent** architecture. Each agent runs as a detached background process with a Go bridge that manages `ai --mode rpc` and exposes a Unix socket for real-time control.

**Key capabilities:**
- Spawn agents with immediate control (steer, abort, prompt)
- Real-time observability via stream.log (tail -f for humans, cursor for LLMs)
- Structured status (turns, tokens, last tool, last text)
- Task DAG with dependencies and auto-scheduling
- Inter-agent async message channels
- Event conversion via `ag conv` (replaces `ai --mode headless`)

## Conversation-First Positioning

`ag` 是**内部执行层**，不是用户主交互层。

- 用户接口：自然语言（由 `workflow` / `implement` 等 skill 承接）
- `ag` 接口：供 agent 在后台调用

默认规则：
1. 不要求用户手工输入 `ag` 命令来推进流程
2. agent 负责把用户意图翻译为 `ag` 操作
3. 仅在用户明确要求"显示底层命令"时，才暴露 CLI 细节

## Architecture

```
ag agent spawn worker-1 --input "fix bugs"
  │
  └── ag bridge worker-1 (detached process, Setpgid)
      │
      └── [bridge process]
          ├── ai --mode rpc (stdin/stdout pipes)
          ├── StreamWriter → stream.log (O_APPEND, real-time readable)
          ├── EventReader → activity.json (atomic rename, rate-limited)
          └── Unix socket → bridge.sock (one-request-per-connection)

ag agent steer worker-1 "use lib X instead"
  └── dial bridge.sock → {"type":"steer","message":"..."} → {"ok":true}
```

**No central daemon. No tmux dependency.** Each agent is independent. Bridge crash = one agent down, not all.

## Setup

```bash
# Build and install
cd skills/ag && go build -o ~/.ai/skills/ag/ag .

# Prerequisites
# - ai binary in PATH (the agent runtime)
# - Go 1.24+ (for building)
```

## CLI Reference

### Agent Lifecycle

```bash
# Spawn agent (blocks until bridge.sock ready, max 10s)
ag agent spawn <id> --input "task description" [--system prompt.md] [--cwd /path] [--timeout 10m]

# Structured status from activity.json
ag agent status <id>
# Output: Status, Pid, Turns, Tokens (in/out/total), LastTool, LastText, Duration

# List all agents
ag agent ls

# Tail agent output stream
ag agent tail <id>                # Last 50 lines
ag agent tail <id> -f             # Follow (like tail -f, exits when agent done)
ag agent tail <id> --lines 200    # More context
ag agent tail <id> --since 4096   # LLM: incremental read with byte cursor

# Mid-turn steering (agent is running, inject new direction)
ag agent steer <id> "don't use external libraries"

# Abort current task (agent stays alive for next prompt)
ag agent abort <id>

# New prompt on existing session (reuse conversation)
ag agent prompt <id> "now add tests"

# Force terminate (preserves agent directory for diagnostics)
ag agent kill <id>

# Graceful shutdown via RPC pipe
ag agent shutdown <id>

# Delete agent directory (only in terminal state: done/failed/killed)
ag agent rm <id>

# Get accumulated text output (works for running and terminal agents)
ag agent output <id> [--tail 50]

# Block until agent reaches terminal state
ag agent wait <id>... [--timeout 600]
```

### Event Conversion

```bash
# Convert ai --mode rpc JSON events to readable text
ai --mode rpc | ag conv              # All output
ai --mode rpc | ag conv --only text  # Assistant text only
ai --mode rpc | ag conv --only tools # Tool calls only
```

This replaces the removed `ai --mode headless`.

### Agent ID Rules

- Pattern: `^[a-zA-Z0-9_-]{1,64}$`
- Storage: `.ag/agents/<id>/`

### Task Management

```bash
# Create task
ag task create "Implement OAuth2" [--file spec.md]

# Import from plan YAML
ag task import-plan .workflow/artifacts/feature/PLAN.yml

# Import from plan YAML (two-phase: create all, then link deps)
ag task import-plan PLAN.yml [--spec spec.md]

# List / filter
ag task list [--status pending|claimed|done|failed]

# Claim / auto-claim next unblocked
ag task claim <id> --as worker-1
ag task next --as worker-1

# Complete with structured result
ag task done <id> --summary "completed successfully"

# Fail with retry hint
ag task fail <id> --error "timeout" --retryable

# Show details (includes turns/tokens from claimant agent)
ag task show <id>

# Dependencies
ag task dep add <id> <dep-id>
ag task dep ls <id>
```

### Inter-Agent Communication

```bash
ag channel create <name>
ag channel ls
ag channel rm <name>
ag send <channel> --file feedback.md
ag send <channel> "inline message"
ag recv <channel> [--wait --timeout 60]
ag recv <channel> --all
```

## Storage Layout

All state under `.ag/` in the working directory (CWD-scoped):

```
.ag/
├── agents/<id>/
│   ├── meta.json          # Spawn config
│   ├── activity.json      # Real-time activity (atomic rename)
│   ├── stream.log         # Real-time append-only output (text + tools + meta)
│   ├── bridge.sock        # Unix socket (running only)
│   ├── bridge-stderr      # Bridge process stderr
│   ├── stderr             # ai process stderr
│   ├── stderr.tail        # Last 4KB of stderr (on crash)
│   └── output             # Final output (copy of stream.log on exit)
├── channels/<name>/
│   └── messages/
└── tasks/<id>/
    └── task.json          # Task state + dependencies
```

## Activity Status Lifecycle

```
spawning → running → done
                  → failed
                  → killed
```

- `spawning`: process started, bridge initializing
- `running`: ai process active, EventReader tracking events
- `done`: ai exited cleanly (exit code 0)
- `failed`: ai exited with error or crashed
- `killed`: terminated by `ag agent kill`

Stale detection: if process PID is no longer alive but activity shows "running", `ag agent status` auto-marks as "failed" with reason.

## Concurrency Limit

⚠️ **LLM 提供商限流**：主 agent + 子 agent 同时运行数**不得超过 3**（即最多 2 个子 agent）。并发稍高即触发 API rate limit，导致子 agent 卡住或失败。

**规则**：
- 同时存活的 agent（含主 agent 自身）≤ 3
- 需要更多子 agent 时，必须等前一批完成（`ag agent rm`）后再 spawn 新的
- 各 skill 文档中应遵守此限制，不可并行 spawn 超过 2 个子 agent

## How Skills Use ag

### implement skill (task fan-out)

```
1. ag task import-plan PLAN.yml
2. for each worker:
     ag agent spawn worker-N --input "$(ag task next --as worker-N)"
3. ag agent wait worker-1 worker-2 ...
4. ag task done T001 --summary "$(ag agent output worker-1)"
5. repeat for next wave of unblocked tasks
```

### workflow skill (worker-judge loop)

```
1. ag agent spawn writer --system writer.md --input "write spec"
2. ag agent spawn reviewer --system reviewer.md --input "review spec"
3. loop:
   ag agent wait writer --timeout 300
   ag agent steer reviewer "$(ag agent output writer)"
   ag agent wait reviewer --timeout 300
   ag agent steer writer "$(ag agent output reviewer)"
```

## Reference

- [README.md](README.md) — Project overview and build instructions
- [docs/design.md](docs/design.md) — Bridge-per-agent architecture design