# ag — Agent Orchestration CLI

`ag` is the orchestration infrastructure for LLM agents. It provides a unified CLI for spawning, controlling, and coordinating AI agents using a **bridge-per-agent** architecture.

## Architecture

Each agent runs in its own tmux session with a dedicated bridge process (`ag bridge <id>`) that manages the `ai --mode rpc` subprocess. The bridge exposes a Unix socket for external control.

```
ag agent spawn worker-1 --input "fix bugs"
  │
  └── tmux new-session -d -s ag-worker-1 -- "ag bridge worker-1"
      │
      └── [bridge process inside tmux]
          ├── exec.Command("ai", "--mode", "rpc")
          │   ├── stdin pipe  → prompt/steer/abort
          │   └── stdout pipe → event stream → activity.json
          ├── EventReader goroutine → writes activity.json
          └── Unix socket listener → bridge.sock

ag agent steer worker-1 "don't use lib X"
  └── dial bridge.sock → send {"type":"steer","message":"..."} → close
```

### Key Design Decisions

- **Bridge-per-agent in tmux** — No single point of failure, no daemon lifecycle management
- **Unix socket control plane** — One request per connection (HTTP-style)
- **activity.json with atomic rename** — Single source of truth for agent state
- **CWD-scoped storage** — All state lives in `.ag/` under the working directory

See [docs/design.md](docs/design.md) for the full design document.

## Build

```bash
go build -o ag .
```

## Commands

### Agent Lifecycle

```bash
ag agent spawn <id> --input "task description"   # Spawn agent in tmux
ag agent status <id>                              # Structured status from activity.json
ag agent ls                                       # List all agents
ag agent steer <id> "message"                     # Mid-turn steering
ag agent abort <id>                               # Cancel current task
ag agent prompt <id> "new task"                   # Reuse session with new prompt
ag agent kill <id>                                # Force terminate (preserves files)
ag agent shutdown <id>                            # Graceful shutdown via RPC
ag agent rm <id>                                  # Delete agent directory
ag agent output <id> [--tail N]                   # Get accumulated text output
ag agent wait <id>... [--timeout 600]             # Block until terminal state
```

### Task Management

```bash
ag task create "Implement OAuth2"
ag task import-plan PLAN.yml                      # Import from plan with dependencies
ag task list [--status pending|claimed|done|failed]
ag task claim <id> --as worker-1
ag task next --as worker-1                        # Claim next unblocked task
ag task done <id> --summary "completed"
ag task fail <id> --error "reason" --retryable
ag task show <id>                                 # Includes turns/tokens/duration
ag task dep add <id> <dep-id>
ag task dep ls <id>
```

### Inter-Agent Communication

```bash
ag channel create review-queue
ag channel ls
ag send <channel> --file feedback.md              # Send to channel
ag recv <channel> [--wait --timeout 60]           # Receive from channel
```

## Project Structure

```
ag/
├── main.go                          # Entry point
├── cmd/
│   ├── root.go                      # Cobra command tree
│   ├── agent_operations.go          # Spawn logic (tmux + socket polling)
│   ├── bridge_client.go             # Socket client (steer/kill/shutdown/rm/output/wait)
│   └── bridge_cmd.go                # Hidden "ag bridge <id>" subcommand
├── internal/
│   ├── agent/
│   │   └── agent.go                 # Agent ID validation, List, ReadActivity
│   ├── bridge/
│   │   ├── types.go                 # AgentActivity, BridgeCommand, SpawnConfig
│   │   ├── activity.go              # ActivityWriter with rate limiting + atomic rename
│   │   ├── eventreader.go           # Parse ai RPC stdout → ActivityWriter
│   │   ├── socket.go                # Unix domain socket server
│   │   └── bridge.go                # Bridge lifecycle (Run function)
│   ├── channel/
│   │   └── channel.go               # Async message channels
│   ├── storage/
│   │   └── storage.go               # Atomic file I/O, path management
│   └── task/
│       └── task.go                  # Task CRUD, dependencies, DAG scheduling
├── docs/
│   ├── design.md                    # Architecture design document
│   ├── SPEC.md                      # Feature specification
│   └── PLAN.md                      # Implementation plan
└── README.md
```

## Storage Layout

All state lives under `.ag/` in the working directory:

```
.ag/
├── agents/
│   └── <id>/
│       ├── meta.json               # Spawn config (system prompt, cwd, timeout)
│       ├── activity.json           # Real-time activity (status, turns, tokens, last text)
│       ├── bridge.sock             # Unix socket (only while running)
│       ├── bridge-stderr           # Bridge process stderr
│       ├── stderr                  # ai process stderr
│       ├── stderr.tail             # Last 4KB of stderr (on crash)
│       └── output                  # Accumulated text output (on exit)
├── channels/
│   └── <name>/
│       └── messages/
└── tasks/
    └── <id>/
        └── task.json               # Task state (status, dependencies, claimant, summary)
```

## Testing

```bash
go test ./... -v                    # All tests
go test ./internal/bridge -v        # Bridge package only
go test ./internal/task -v          # Task package only
```

## Prerequisites

- **tmux** must be installed and in PATH
- **ai** binary must be in PATH (the agent runtime)
- Go 1.24+

## See Also

- [SKILL.md](SKILL.md) — Skill manifest and usage guide for the orchestrating agent
- [docs/design.md](docs/design.md) — Bridge-per-agent architecture design