# ai — AI Coding Agent (Go)

`ai` is a Go-based AI coding agent with an RPC-first architecture, designed for editor and CLI integration. It features a subcommand-based CLI, session persistence, LLM-driven context management, and a pluggable skills system.

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
- `--input-file <path>` — Read initial prompt from file (avoids shell ARG_MAX)
- `--role <coder\|orchestrator\|validator>` — Agent role (affects system prompt)
- `--name <text>` — Human-readable name for the run

**`serve` only:**
- `--http <addr>` — Enable HTTP debug server (e.g., `:6060`)
- `--id-file <path>` — Write run ID to file after startup

**`watch`:**
- `--id <run-id>` — Run ID or prefix (auto-selects by cwd if omitted)

**`send`:**
- `--id <run-id>` — Run ID or prefix
- `--wait` — Send message and wait for response
- `--timeout <duration>` — Max wait time

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
| `ZAI_MAX_CONCURRENT_TOOLS` | No | `5` | Max concurrent tool execution |
| `ZAI_TOOL_TIMEOUT` | No | `30` | Tool execution timeout (seconds) |
| `ZAI_QUEUE_TIMEOUT` | No | `60` | Tool queue wait timeout (seconds) |

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
    "api": "openai-completions",
    "maxTokens": 16384
  },
  "thinkingLevel": "off",
  "compactor": {
    "maxMessages": 50,
    "maxTokens": 8000,
    "keepRecent": 5,
    "keepRecentTokens": 20000,
    "reserveTokens": 16384,
        "toolCallCutoff": 10,
    "autoCompact": true
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

In `rpc` mode, `ai` reads JSON-RPC commands from stdin and writes responses/events to stdout. See [docs/rpc-protocol.md](docs/rpc-protocol.md) for full details.

### Commands

| Command | Description |
|---------|-------------|
| `prompt` | Send a user message |
| `steer` | Inject mid-turn guidance |
| `follow_up` | Queue a follow-up message |
| `abort` | Cancel current turn |
| `ping` | Health check |

### Events

| Event | Description |
|-------|-------------|
| `server_start` | Server initialized |
| `agent_start` / `agent_end` | Agent processing lifecycle |
| `turn_start` / `turn_end` | Turn boundaries |
| `message_start` / `message_update` / `message_end` | Streaming message chunks |
| `tool_execution_start` / `tool_execution_end` | Tool invocations |
| `compaction_start` / `compaction_end` | Context compaction |
| `llm_retry` | LLM API retry (rate limit, etc.) |
| `loop_guard_triggered` | Loop guard protection |
| `tool_call_recovery` | Tool call recovery |
| `error` | Error event |

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
| `find_skill` | Search and discover available skills |

## Skills System

Skills are Markdown files (with optional YAML frontmatter) loaded from:
- `~/.ai/skills/` — Global skills
- `.ai/skills/` — Project skills

Skills extend the agent's capabilities with domain-specific instructions, prompts, and scripts. Use the `find_skill` tool to search for relevant skills during a session.

See [skills/](skills/) for available skill packages.

## File Locations

| Path | Description |
|------|-------------|
| `~/.ai/config.json` | Configuration |
| `~/.ai/auth.json` | API credentials |
| `~/.ai/ai-{pid}.log` | Per-process logs |
| `~/.ai/sessions/--<cwd>--/` | Session data (per working directory) |
| `~/.ai/skills/` | Global skills |
| `.ai/skills/` | Project skills |
| `~/.ai/traces/` | Perfetto-compatible trace files |
| `~/.ai/runs/` | Run metadata for `ai serve`/`ai run` |

## Tracing

The agent writes Perfetto-compatible trace files to `~/.ai/traces/`. Events include tool execution, LLM calls, agent lifecycle, and log bridge output. Events are configurable via `pkg/traceevent/config.go`.

## Session Persistence

Sessions are stored as append-only JSONL files under `~/.ai/sessions/--<sanitized-path>--/`. Key properties:
- Directory-based with `messages.jsonl` as the primary file
- Header entry contains session ID, CWD, git version metadata
- Fork support: branch conversations from any point
- Checkpoint + journal: efficient recovery with periodic snapshots
- Compaction snapshots: post-compaction state saved to `compactions/` files
- Legacy format auto-migration on load

See [docs/session-format.md](docs/session-format.md) for format details.

## Architecture

See [docs/architecture.md](docs/architecture.md) for detailed component diagrams and data flow.

## Documentation

- [docs/README.md](docs/README.md) — Documentation index and live docs
- [CHANGELOG.md](CHANGELOG.md) — Functional changes per commit
- [CLAUDE.md](CLAUDE.md) — Agent guidance for this repository

## License

Consistent with the original project (see [pi-mono](https://github.com/badlogic/pi-mono)).