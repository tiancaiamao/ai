# ai — AI Coding Agent (Go)

`ai` is a Go-based AI coding agent with an RPC-first architecture, designed for editor and CLI integration. It features a subcommand-based CLI, session persistence, LLM-driven context management, a pluggable skills system, and an agent orchestration runtime (`ag`).

## Build & Install

```bash
go install ./cmd/ai
```

This produces the `ai` binary. Requires Go 1.24+.

## CLI Usage

The `ai` binary uses subcommands:

```bash
ai <subcommand> [flags]
```

### Subcommands

| Subcommand | Description |
|------------|-------------|
| `run` | Start agent with interactive TUI (serves + watches in one process) |
| `serve` | Start agent as a background daemon (foreground process, redirect I/O to files) |
| `rpc` | Raw JSON-RPC mode over stdin/stdout (for programmatic integration) |
| `ls` | List running and recent agent instances |
| `watch` | Attach to a running serve instance (TUI) |
| `send` | Send a message to a running agent instance |
| `kill` | Stop a running agent instance |

### Examples

```bash
# Interactive TUI session
ai run
ai run --input "fix the bug in main.go"
ai run --session /path/to/session-dir

# Background daemon + attach
ai serve --input "explain the architecture"
ai watch
ai send "what about error handling?"
ai kill

# RPC mode (for editor/tool integration)
ai rpc < commands.jsonl

# List runs
ai ls
ai ls --all --json
```

### Flags

**`run` / `serve`:**
- `--session <path>` — Session directory path
- `--system-prompt <text>` — Custom system prompt (`@file` to load from file)
- `--max-turns <n>` — Maximum conversation turns (0 = unlimited)
- `--timeout <duration>` — Total execution timeout (0 = unlimited)
- `--input <text>` — Initial prompt to send after startup

**`serve` only:**
- `--http <addr>` — Enable HTTP debug server (e.g., `:6060`)
- `--name <text>` — Human-readable name for the run

**`watch`:**
- `--id <run-id>` — Run ID or prefix (auto-selects by cwd if omitted)

**`send`:**
- `--id <run-id>` — Run ID or prefix

**`kill`:**
- `--id <run-id>` — Run ID or prefix
- `--force` — Send SIGKILL instead of graceful abort

**`ls`:**
- `--all` — Include finished runs
- `--json` — JSON output

## Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `ZAI_API_KEY` | Yes | — | API key for the LLM provider |
| `ZAI_BASE_URL` | No | `https://api.z.ai/api/coding/paas/v4` | LLM API base URL |
| `ZAI_MODEL` | No | `glm-4.5-air` | Model ID |
| `ZAI_MAX_TOKENS` | No | Model default | Max output tokens |

API key can also be stored in `~/.ai/auth.json`:

```json
{
  "zai": {
    "type": "api_key",
    "key": "your-api-key"
  }
}
```

Priority: environment variables > `auth.json` > config defaults.

## Configuration

Config file: `~/.ai/config.json`

```json
{
  "model": {
    "id": "glm-4.5-air",
    "provider": "zai",
    "baseUrl": "https://api.z.ai/api/coding/paas/v4",
    "api": "openai-completions"
  },
  "compactor": {
    "maxMessages": 100,
    "maxTokens": 100000,
    "keepRecent": 10,
    "reserveTokens": 16384,
    "toolCallCutoff": 10
  },
  "concurrency": {
    "maxConcurrentTools": 5,
    "toolTimeout": 30,
    "queueTimeout": 60
  },
  "toolOutput": {
    "maxChars": 10000
  },
  "log": {
    "level": "info",
    "file": "~/.ai/ai-{pid}.log"
  }
}
```

## RPC Protocol

In `rpc` mode, `ai` reads JSON-RPC commands from stdin and writes responses/events to stdout.

### Commands

- `prompt` — Send a user message
- `steer` — Inject mid-turn guidance
- `follow_up` — Queue a follow-up message
- `abort` — Cancel current turn
- `ping` — Health check

### Events

| Event | Description |
|-------|-------------|
| `server_start` | Server initialized |
| `agent_start` / `agent_end` | Agent processing lifecycle |
| `turn_start` / `turn_end` | Turn boundaries |
| `message_start` / `message_update` / `message_end` | Streaming message chunks |
| `tool_execution_start` / `tool_execution_end` | Tool invocations |

`message_update` types: `text_start`, `text_delta`, `text_end`, `toolcall_delta`, `thinking_delta`.

## Built-in Tools

| Tool | Description |
|------|-------------|
| `read` | Read file contents |
| `write` | Write to file |
| `edit` | Edit file by replacing text (supports fuzzy matching) |
| `bash` | Execute shell commands (with timeout control) |
| `grep` | Search file contents (ripgrep or grep) |
| `change_workspace` | Change working directory |
| `context_management` | LLM-driven context truncation/compaction |

## Skills System

Skills are Markdown files (with optional YAML frontmatter) loaded from:
- `~/.ai/skills/` — Global skills
- `.ai/skills/` — Project skills

Skills extend the agent's capabilities with domain-specific instructions, prompts, and scripts.

### Key Skills

| Skill | Description |
|-------|-------------|
| `ag` | Agent orchestration runtime — spawn, steer, and coordinate AI agents |
| `brainstorm` | Explore user intent through conversation |
| `plan` | Read design docs, produce task plan with dependency DAG |
| `implement` | Code-driven task execution with ag task scheduler |
| `brainstorm` | Interactive design exploration → design.md |
| `review` | Code review using codex-rs methodology |
| `systematic-debugging` | Structured debugging workflow |
| `github` | GitHub interaction via `gh` CLI |
| `land` | Merge PRs with branch management |
| `tmux` | Background process management |


## Agent Orchestration (`ag`)

`ag` is a companion CLI (built from `skills/ag/`) for orchestrating multiple AI agents:

```bash
# Build ag
cd skills/ag && go install .

# Spawn agents
ag agent spawn worker-1 --input "fix authentication bug"
ag agent spawn reviewer --system @reviewer.md --input "review the changes"

# Monitor
ag agent status worker-1
ag agent output worker-1 --tail 50

# Control
ag agent steer worker-1 "also check error handling"
ag agent kill worker-1

# Task DAG scheduling
ag task import-plan tasks.md
ag task next --claimant worker-1
ag task done T001 --summary "completed"
```

Key features:
- Bridge-per-agent architecture (no central daemon, no tmux dependency)
- Multi-backend support (`ai` JSON-RPC, `codex` raw protocol)
- Inter-agent communication via channels
- Task DAG with dependency resolution

See `skills/ag/SKILL.md` for full documentation.

## File Locations

| Path | Description |
|------|-------------|
| `~/.ai/config.json` | Configuration |
| `~/.ai/auth.json` | API credentials |
| `~/.ai/ai-{pid}.log` | Per-process logs |
| `~/.ai/sessions/--<cwd>--/` | Session data (per git repo root) |
| `~/.ai/skills/` | Global skills |
| `.ai/skills/` | Project skills |
| `~/.ai/traces/` | Perfetto-compatible trace files |
| `~/.ai/runs/` | Run metadata for `ai serve`/`ai run` |

## Tracing

The agent writes Perfetto-compatible trace files to `~/.ai/traces/`. Events include tool execution, LLM calls, agent lifecycle, and log bridge output. Events are configurable via `pkg/traceevent/config.go`.

## Session Persistence

Sessions are stored as append-only JSONL files under `~/.ai/sessions/--<sanitized-path>--/`. Key properties:
- Directory-based with `messages.jsonl` as the primary file
- Header entry contains session ID and metadata
- Fork support: branch conversations from any point
- Checkpoint + journal: efficient recovery with periodic snapshots
- Legacy format auto-migration on load

## Architecture

See [docs/architecture.md](docs/architecture.md) for detailed component diagrams and data flow.

## License

Consistent with the original project (see [pi-mono](https://github.com/badlogic/pi-mono)).