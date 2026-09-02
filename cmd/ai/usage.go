package main

import (
	"fmt"
	"os"
)

func printUsage() {
	fmt.Fprint(os.Stderr, `ai - AI coding assistant

Usage:
  ai <subcommand> [flags]

Subcommands:
  run             Start agent with interactive TUI (serve + watch)
  serve           Start agent as background daemon
  acp             Start as ACP agent over stdio (agent-shell, Zed, etc.)
  ls              List running and recent runs
  models          List available models (use "provider/id" syntax for --model)
  watch           Attach to a running serve instance (TUI)
  send            Send a message to a running serve instance
  kill            Stop a running agent instance

Flags for 'run':
  --session <path>         Session file path
  --system-prompt <text>   Custom system prompt (@file to load from file)
    --role <role>            Agent role name: loads ~/.ai/roles/<role>/agent.yaml
  --max-turns <n>          Maximum conversation turns (0 = unlimited)
  --timeout <duration>     Total execution timeout (0 = unlimited)
  --input <text>           Initial prompt to send after startup
  --model <id>             Override LLM model ID. Use "provider/id" for exact match (e.g. opencode/deepseek-v4-flash). Run "ai models" to list available options.

Flags for 'serve':
  --session <path>         Session file path
  --system-prompt <text>   Custom system prompt (@file to load from file)
    --role <role>            Agent role name: loads ~/.ai/roles/<role>/agent.yaml
  --max-turns <n>          Maximum conversation turns (0 = unlimited)
  --timeout <duration>     Total execution timeout (0 = unlimited)
  --http <addr>            Enable HTTP debug server (e.g., ':6060')
  --input <text>           Initial prompt to send after startup
  --input-file <path>      Read initial prompt from file (avoids ARG_MAX limits)
  --name <text>            Human-readable name for the run
  --id-file <path>         Write run ID to this file after startup
  --model <id>             Override LLM model ID. Use "provider/id" for exact match (e.g. opencode/deepseek-v4-flash). Run "ai models" to list available options.

Flags for 'ls':
  --all                    Include finished runs
  --json                   JSON output

Flags for 'watch':
  --id <run-id>            Run ID or prefix (auto-selects by cwd if omitted)
  --follow                 Continuously stream events until agent exits
  --follow --pretty        Stream formatted output (readable conversation)
  --follow --summary       Stream final assistant text only (no intermediate output)
  --follow --timeout 2m    Timeout after duration (use with --pretty/--summary for polling)

Flags for 'send':
  --id <run-id>            Run ID or prefix (auto-selects by cwd if omitted)
  --wait                   Wait for agent to finish and stream the response
  --summary                With --wait: only show final assistant text
  --timeout <duration>     With --wait: max wait time (0 = unlimited)

Flags for 'kill':
  --id <run-id>            Run ID or prefix (auto-selects by cwd if omitted)
  --force                  Send SIGKILL instead of graceful abort

Examples:
  ai run                          Start agent with interactive TUI
  ai run --input "fix the bug"    Start with an initial prompt
  ai serve                        Start agent as background daemon
  ai serve --input "fix the bug"  Start daemon with an initial prompt
  ai acp                         Start ACP on stdin/stdout
  ai ls                           List running agents
  ai ls --all                     Include finished agents
  ai models                       List available models
  ai models deepseek               Search models by keyword
  ai send "hello"                 Send message to agent in current directory
  ai send "/session"              Send slash command
  ai send --wait "fix the bug"    Send and wait for response
  ai watch                        Attach to agent's TUI
  ai watch --follow --pretty      Stream formatted output
  ai kill                         Stop agent in current directory
  ai kill --id abc123             Stop specific run by ID
  ai kill --force                 Force kill (SIGKILL)
`)
}
