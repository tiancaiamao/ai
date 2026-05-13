---
name: ag
description: Provide subagent and agent orchestration runtime. Spawns AI agents as detached processes with bridge-per-agent architecture, supports mid-turn steering, structured status, real-time observability via stream.log, task DAG scheduling, and inter-agent channels. Backend-agnostic: supports any CLI-based agent via pluggable backends (json-rpc and raw protocols).
---

# ag — Agent Orchestration CLI

## Overview

`ag` orchestrates AI agents using a **bridge-per-agent** architecture. Each agent runs as a detached background process with a Go bridge that manages the agent subprocess and exposes a Unix socket for real-time control.

**Key capabilities:**
- Spawn agents with immediate control (steer, abort, prompt) — backend-dependent
- Real-time observability via stream.log (tail -f for humans, cursor for LLMs)
- Structured status (turns, tokens, last tool, last text)
- Task DAG with dependencies and auto-scheduling
- Inter-agent async message channels
- Multi-backend support: any CLI-based agent (ai, codex, claude, etc.)
- Event conversion via `ag conv` (replaces `ai --mode headless`)

## Backend Support

`ag` supports multiple agent backends via a pluggable configuration system. Each backend defines:
- **Command + args** to spawn the agent process
- **Protocol**: `json-rpc` (structured event stream) or `raw` (line-by-line stdout capture)
- **Capabilities**: which control-plane operations are supported (steer, abort, prompt)

### Default Backend: `ai`

The default backend uses `ai rpc` with full JSON-RPC event parsing. This provides:
- Token counting (in/out/total)
- Tool call tracking
- Mid-turn steering, abort, and follow-up prompts
- Full structured activity.json

### Codex Backend

The codex backend uses `codex exec --full-auto --json --skip-git-repo-check` with the `raw` protocol. Key considerations:

| 要点 | 说明 |
|------|------|
| **代理** | ⛔ 必须在 spawn 的 shell 中 `export HTTP_PROXY=http://127.0.0.1:8119` 和 `HTTPS_PROXY`。`exec.Command` 继承父进程环境变量，所以 export 就够了 |
| **backends.yaml** | 必须在 CWD。`FindBackendsFile` 在当前目录查找，找不到则报 `unknown backend "codex"` |
| **raw protocol** | 不支持 steer/abort/prompt，只能等 codex 自行完成。用 `ag wait --timeout` 控制超时 |
| **--skip-git-repo-check** | codex 默认要求在 git repo 中运行，非 git 目录（如 /tmp）需要此 flag |
| **调试** | 看不到活动时检查 `.ag/agents/<id>/stderr`，常见原因是代理未设置导致连接超时 |

### Backends Configuration

Backends are defined in `backends.yaml` (co-located with the ag skill):

```yaml
backends:
  ai:
    command: ai
    args: ["rpc"]
    protocol: json-rpc
    supports:
      steer: true
      abort: true
      prompt: true
  codex:
    command: codex
    args: ["exec", "--full-auto", "--json", "--skip-git-repo-check"]
    protocol: raw
    supports:
      steer: false
      abort: false
      prompt: false
```

If no `backends.yaml` is found, `ag` defaults to the `ai` backend.

### Protocol Types

| Protocol | Event Parsing | Token Counting | Steering | Use Case |
|----------|--------------|----------------|----------|----------|
| `json-rpc` | Full (EventReader) | ✅ | ✅ | ai backend |
| `raw` | Line-by-line stdout | ❌ | ❌ | Simple CLI agents |

## Conversation-First Positioning

`ag` 是**内部执行层**，不是用户主交互层。

- 用户接口：自然语言（由上层 skill 承接）
- `ag` 接口：供 agent 在后台调用

默认规则：
1. 不要求用户手工输入 `ag` 命令来推进流程
2. agent 负责把用户意图翻译为 `ag` 操作
3. 仅在用户明确要求"显示底层命令"时，才暴露 CLI 细节

## Architecture

```
ag agent spawn worker-1 --input "fix bugs" [--backend codex]
  │
  └── ag bridge worker-1 (detached process, Setpgid)
      │
      └── [bridge process]
          ├── <backend command> <args> (stdin/stdout pipes)
          ├── StreamWriter → stream.log (O_APPEND, real-time readable)
          ├── EventReader (json-rpc) or RawReader (raw) → activity.json
          └── Unix socket → bridge.sock (one-request-per-connection)

ag agent steer worker-1 "use lib X instead"
  └── dial bridge.sock → {"type":"steer","message":"..."} → {"ok":true}
  └── (for raw backends: error "backend does not support steer")
```

**No central daemon. No tmux dependency.** Each agent is independent. Bridge crash = one agent down, not all.

## Setup

```bash
# Build and install
cd skills/ag && go build -o ~/.ai/skills/ag/ag .

# Prerequisites
# - At least one agent binary in PATH (e.g., ai, codex)
# - Go 1.24+ (for building)
# - backends.yaml (optional, defaults to ai-only)
```

## CLI Reference

### Agent Lifecycle

```bash
# Spawn agent (blocks until bridge.sock ready, max 10s)
ag agent spawn <id> --input "task description" [--system @prompt.md] [--cwd /path] [--backend ai]

# Backend selection: use a non-default backend
ag agent spawn worker-1 --backend codex --input "fix bugs in auth"

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
# Convert ai rpc JSON events to readable text
ai rpc | ag conv                      # All output
ai rpc | ag conv --only text           # Assistant text only
ai rpc | ag conv --only tools          # Tool calls only
```

This replaces the removed `ai --mode headless`.

### --system Flag

`--system` is **optional**. Behavior:

- **Omitted**: agent uses `ai`'s default coding agent system prompt — includes tool usage guidance, verification requirements, skills auto-discovery, project context (AGENTS.md), and workspace awareness. **This is the recommended default for most tasks.**
- **Provided**: replaces the entire system prompt. Use `@file.md` to load from file. Only use when the task needs a specialized persona (e.g., reviewer, planner) that differs significantly from a coding agent.

Common mistake: passing a hand-written `--system` that is *worse* than the default. When in doubt, omit it.

### Agent ID Rules

- Pattern: `^[a-zA-Z0-9_-]{1,64}$`
- Storage: `.ag/agents/<id>/`

### Task Management

```bash
# Create task
ag task create "Implement OAuth2" [--file spec.md]

# Import from plan YAML
ag task import-plan tasks.yml

# Import from plan YAML (two-phase: create all, then link deps)
ag task import-plan PLAN.yml [--design design.md]

# List / filter
ag task list [--status pending|claimed|done|failed]

# Claim / auto-claim next unblocked
ag task claim <id> --claimant worker-1
ag task next --claimant worker-1

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

## Callback Protocol (Prompt-as-Callback)

后台任务完成后，通过 `ag agent prompt` 回调主 agent，而非主 agent 轮询等待。

### 核心约定

```
主 agent (ID: main)          后台任务 (任何进程)
     │                              │
     ├── spawn + 传自己的 ID ──────→│
     │   "完成后执行:                │
     │    ag agent prompt main      │
     │    'task:<name> result:...'" │
     │                              │
     │  ← 继续做别的事或等消息 ──────│ ... 执行中 ...
     │                              │
     │←── ag agent prompt main ─────│ 完成！
     │    "task:build result:ok"     │
     │                              │
     └── LLM 收到新 prompt，被唤醒处理
```

### 三要素

1. **Agent ID = 回信地址**：主 agent 把自己的 ID 告诉后台任务
2. **`ag agent prompt <id> <msg>` = 叩门**：后台任务完成时执行这一条命令
3. **ai 进程活着 = 前提**：前台 agent 的 LLM 会话保持运行，prompt 注入后自然唤醒

### 适用场景

| 场景 | 传统方式 | Callback 方式 |
|------|---------|--------------|
| `ag task run --detach` | 主 agent 轮询 `ag task log` | scheduler 完成后 `ag agent prompt main` |
| 子 agent spawn | `ag agent wait`（阻塞） | 子 agent 完成后 `ag agent prompt main` |
| shell 后台任务 | 主 agent 等待 | 脚本末尾 `ag agent prompt main` |
| CI/CD pipeline | 无通知 | pipeline 末尾回调 |

### 在 Prompt 中的写法

主 agent 在 delegation prompt 中加入：

```
你的回调地址是 agent ID: "<main-agent-id>"。
完成全部工作后，执行以下命令通知我:
ag agent prompt <main-agent-id> "task:<task-name> status:done summary:<简要结果>"
如果失败，执行:
ag agent prompt <main-agent-id> "task:<task-name> status:failed error:<错误原因>"
```

### 注意事项

- `ag agent prompt` 要求 ai 进程仍然存活。如果 agent 已退出，prompt 会失败
- 如果后台任务可能比主 agent 存活更久，使用 `ag send <channel>` 作为持久化备选（消息写入文件，不依赖进程存活）
- 主 agent 不需要做任何特殊配置——它只需要保持 LLM 会话运行

## Concurrency Limit

⚠️ **LLM 提供商限流**：主 agent + 子 agent 同时运行数**不得超过 3**（即最多 2 个子 agent）。并发稍高即触发 API rate limit，导致子 agent 卡住或失败。

**规则**：
- 同时存活的 agent（含主 agent 自身）≤ 3
- 需要更多子 agent 时，必须等前一批完成（`ag agent rm`）后再 spawn 新的
- 各 skill 文档中应遵守此限制，不可并行 spawn 超过 2 个子 agent

## Patterns

### Task Fan-Out

并行执行多个独立任务，每个任务一个 agent。

```
1. ag task import-plan tasks.yml
2. Get next task: ID=$(ag task next --claimant worker-N)
   Get description: ag task show $ID
3. ag agent spawn worker-N --input "Task $ID: <title>\n<description>"
4. ag agent wait worker-1 worker-2 ...
5. ag task done T001 --summary "$(ag agent output worker-1)"
6. repeat for next wave of unblocked tasks
```

### Worker-Judge Loop

一个 agent 产出，另一个 agent 审查，循环直到通过。

**Worker 保持跨轮次存活**：worker 只 spawn 一次，后续轮次通过 `ag agent prompt` 发送 judge 反馈。这保留了 worker 的完整上下文，使其能在先前尝试的基础上迭代改进。Judge 每轮重新 spawn 以保持独立性。

```
1. ag agent spawn worker --system worker-prompt --input "task description"
2. ag agent wait worker --timeout 300
3. ag agent output worker > /tmp/worker-output.txt
   (do NOT rm worker — keep it alive for the loop)
4. ag agent spawn judge --system judge-prompt --input "$(cat /tmp/worker-output.txt)"
5. ag agent wait judge --timeout 60
6. ag agent output judge > /tmp/judge-output.txt
7. ag agent rm judge
8. if APPROVED → ag agent rm worker, done
   else → ag agent prompt worker "$(cat /tmp/judge-feedback.txt)"
          ag agent wait worker --timeout 300
          repeat from step 3
```

封装在 `patterns/pair.sh` 中：

```bash
pair.sh <worker-system-prompt-file> <judge-system-prompt-file> <input-file> [max-rounds]
```

## Reference

- [README.md](README.md) — Project overview and build instructions
- [docs/design.md](docs/design.md) — Bridge-per-agent architecture design