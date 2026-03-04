# AiClaw Documentation

AiClaw is an AI-powered chatbot with multi-channel support (Feishu, etc.) and scheduled tasks.

## Table of Contents

- [Quick Start](#quick-start)
- [Configuration](#configuration)
- [Commands](#commands)
- [Cron Scheduled Tasks](#cron-scheduled-tasks)
- [Workflow Skills](#workflow-skills)
- [Skills System](#skills-system)
- [Development](#development)

## Quick Start

```bash
# Build
cd claw && go build -o bin/aiclaw ./cmd/aiclaw

# Start gateway (connect to Feishu and other channels)
./bin/aiclaw

# Manage cron tasks
./bin/aiclaw cron list
./bin/aiclaw cron add -n "Daily Reminder" -m "Check todos" -c "0 9 * * *"
```

## Configuration

Configuration files are located in `~/.aiclaw/`:

```
~/.aiclaw/
├── config.json      # Main config (model, channels)
├── auth.json        # API keys
├── AGENTS.md        # Custom identity prompt
├── cron/            # Scheduled tasks
│   └── jobs.json
├── sessions/        # Session storage
├── skills/          # Skills directory (symlink)
└── memory/          # tiered-memory storage
```

### config.json

```json
{
  "model": {
    "id": "claude-3-5-sonnet-20241022",
    "provider": "anthropic",
    "baseUrl": ""
  },
  "voice": {
    "enabled": true,
    "provider": "zhipu"
  },
  "channels": {
    "feishu": {
      "app_id": "cli_xxx",
      "app_secret": "xxx"
    }
  }
}
```

### auth.json

```json
{
  "anthropic": { "apiKey": "sk-xxx" },
  "zhipu": { "apiKey": "xxx" },
  "zai": { "apiKey": "xxx" }
}
```

## Commands

```bash
# Start gateway (connect channels, process messages)
./bin/aiclaw

# Start with trace debugging enabled
./bin/aiclaw -trace

# Set log level
./bin/aiclaw -log-level debug

# Cron task management
./bin/aiclaw cron list
./bin/aiclaw cron add -n "Name" -m "Message" -c "0 9 * * *"
./bin/aiclaw cron remove <id>
./bin/aiclaw cron enable <id>
./bin/aiclaw cron disable <id>
```

## Cron Scheduled Tasks

AiClaw supports both cron expressions and fixed intervals.

### Adding Tasks

```bash
# Execute at 9:00 every day
./bin/aiclaw cron add -n "Morning Reminder" -m "Start a new day!" -c "0 9 * * *"

# Execute every 60 seconds
./bin/aiclaw cron add -n "Heartbeat" -m "ping" -e 60

# Generate daily report at 18:00
./bin/aiclaw cron add -n "Daily Report" -m "Generate today's summary" -c "0 18 * * *"
```

### Cron Expression Format

```
┌───────────── minute (0 - 59)
│ ┌───────────── hour (0 - 23)
│ │ ┌───────────── day of month (1 - 31)
│ │ │ ┌───────────── month (1 - 12)
│ │ │ │ ┌───────────── day of week (0 - 6, 0=Sunday)
│ │ │ │ │
* * * * *
```

Common examples:
- `0 9 * * *` - Every day at 9:00
- `30 18 * * 1-5` - Mon-Fri at 18:30
- `0 */2 * * *` - Every 2 hours
- `0 0 1 * *` - First day of month at 0:00

### Managing Tasks

```bash
# List all tasks
./bin/aiclaw cron list

# Output example:
# Scheduled Jobs:
# ---------------
#   [b28a1f52] Morning Reminder
#       Schedule: 0 9 * * *
#       Message:  Start a new day!
#       Status:   ✓ enabled
#       Next run: 2026-03-03 09:00

# Disable task
./bin/aiclaw cron disable b28a1f52

# Enable task
./bin/aiclaw cron enable b28a1f52

# Delete task
./bin/aiclaw cron remove b28a1f52
```

### How It Works

1. Tasks are stored in `~/.aiclaw/cron/jobs.json`
2. Gateway automatically loads and starts scheduler on startup
3. Checks for due tasks every second
4. Sends message to agent when task is due
5. **Hot reload**: Changes to `jobs.json` are detected automatically - CLI modifications take effect immediately without restart

## Skills System

Skills directory is at `~/.aiclaw/skills/` (symlinked to `claw/skills/`).

## Workflow Skills

AiClaw includes a workflow-oriented skill set for issue/worktree/PR automation:

- `wf-intake` - task -> issue -> worktree + status initialization
- `wf-tick` - cron-safe reconciliation tick and state transition engine
- `wf-worker` - subagent execution and heartbeat supervision
- `wf-pr-review` - PR/review reconciliation and fix-loop triggering
- `wf-closeout` - issue close + worktree cleanup + registry archive

See [workflow-skills.md](./workflow-skills.md) for usage and state model.

### Built-in Skills

- **tiered-memory** - Three-tier memory system (hot/warm/cold)
- **agent-self-governance** - Self-governance
- **intelligent-router** - Intelligent routing
- More skills in `skills/` directory

### tiered-memory Usage

```bash
cd ~/.aiclaw/skills/tiered-memory

# Store memory
python3 scripts/memory_cli.py store \
  --text "User prefers concise responses" \
  --category "preferences" \
  --importance 0.8

# Retrieve memory
python3 scripts/memory_cli.py retrieve \
  --query "user preferences" \
  --llm \
  --limit 5

# View stats
python3 scripts/memory_cli.py metrics
```

## Development

### Project Structure

```
claw/
├── cmd/aiclaw/         # Main entry point
│   ├── main.go         # Main logic
│   └── cmd_cron.go     # Cron CLI
├── pkg/
│   ├── adapter/        # AgentLoop adapter
│   ├── cron/           # Cron service
│   └── voice/          # Voice transcription
├── skills/             # Skills directory (27 skills)
├── docs/               # Documentation
├── go.mod
└── README.md
```

### Dependencies

- `github.com/sipeed/picoclaw` - Channel management
- `github.com/adhocore/gronx` - Cron expression parsing
- `github.com/fsnotify/fsnotify` - File watching for hot reload
- `github.com/tiancaiamao/ai` - AI Agent core

### Building

```bash
cd claw
go build -o bin/aiclaw ./cmd/aiclaw
```

## Related Links

- [AiClaw Main Project](../) - AI Agent core
- [PicoClaw](https://github.com/sipeed/picoclaw) - Channel management
