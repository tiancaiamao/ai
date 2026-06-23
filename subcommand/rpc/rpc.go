package rpcsubcommand

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"log/slog"

	"github.com/tiancaiamao/ai/pkg/rpc"
	"github.com/tiancaiamao/ai/subcommand/helpers"
)

// RPCSubcommand implements the 'ai rpc' subcommand.
func RPCSubcommand() {
	fs := flag.NewFlagSet("rpc", flag.ExitOnError)
	sessionPathFlag := fs.String("session", "", "Session file path")
	maxTurnsFlag := fs.Int("max-turns", 0, "Maximum conversation turns (0 = unlimited)")
	timeoutFlag := fs.Duration("timeout", 0, "Total execution timeout (0 = unlimited)")
	systemPromptFlag := fs.String("system-prompt", "", "Custom system prompt. Use '@' prefix to load from file (e.g., @/path/to/file.md)")
	debugAddr := fs.String("http", "", "Enable HTTP debug server on specified address (e.g., ':6060')")
	agentConfigFlag := fs.String("agent-config", "", "Path to agent.yaml configuration file")
	modelFlag := fs.String("model", "", "Override LLM model ID (e.g. claude-sonnet-4-20250514)")
	runidFlag := fs.String("runid", "", "Run ID from parent ai serve process (used for subagent tracking)")
	fs.Parse(os.Args[1:])

	// Setup signal handling for graceful shutdown.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		sig := <-sigCh
		fmt.Fprintf(os.Stderr, "[rpc] received signal: %v, aborting agent\n", sig)
		rpc.AgentAbort() // Trigger agent abort in RunRPC
	}()

	systemPrompt := helpers.ParseSystemPrompt(*systemPromptFlag)

	if err := rpc.RunRPC(*sessionPathFlag, *debugAddr, os.Stdin, os.Stdout, systemPrompt, *maxTurnsFlag, *timeoutFlag, *agentConfigFlag, *modelFlag, *runidFlag); err != nil {
		slog.Error("rpc error", "error", err)
		os.Exit(1)
	}
}

// PrintUsage prints the CLI usage text to stderr.
func PrintUsage() {
	fmt.Fprint(os.Stderr, `ai - AI coding assistant

Usage:
  ai <subcommand> [flags]

Subcommands:
  run             Start agent with interactive TUI (serve + watch)
  serve           Start agent as background daemon
  rpc             Start in raw RPC mode (stdin/stdout JSON-RPC)
  ls              List running and recent runs
  watch           Attach to a running serve instance (TUI)
  send            Send a message to a running serve instance
  kill            Stop a running agent instance

Flags for 'run':
  --session <path>         Session file path
  --system-prompt <text>   Custom system prompt (@file to load from file)
  --role <role>            Agent role: coder (default), orchestrator, validator
  --max-turns <n>          Maximum conversation turns (0 = unlimited)
  --timeout <duration>     Total execution timeout (0 = unlimited)
  --input <text>           Initial prompt to send after startup
  --model <id>             Override LLM model ID (e.g. claude-sonnet-4-20250514)

Flags for 'serve':
  --session <path>         Session file path
  --system-prompt <text>   Custom system prompt (@file to load from file)
  --role <role>            Agent role: coder (default), orchestrator, validator
  --max-turns <n>          Maximum conversation turns (0 = unlimited)
  --timeout <duration>     Total execution timeout (0 = unlimited)
  --http <addr>            Enable HTTP debug server (e.g., ':6060')
  --input <text>           Initial prompt to send after startup
  --input-file <path>      Read initial prompt from file (avoids ARG_MAX limits)
  --name <text>            Human-readable name for the run
  --id-file <path>         Write run ID to this file after startup
  --model <id>             Override LLM model ID (e.g. claude-sonnet-4-20250514)

Flags for 'rpc':
  --session <path>         Session file path
  --system-prompt <text>   Custom system prompt (@file to load from file)
  --agent-config <path>    Path to agent.yaml configuration file
  --max-turns <n>          Maximum conversation turns (0 = unlimited)
  --timeout <duration>     Total execution timeout (0 = unlimited)
  --http <addr>            Enable HTTP debug server (e.g., ':6060')
  --model <id>             Override LLM model ID (e.g. claude-sonnet-4-20250514)

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
  ai rpc                          Start raw JSON-RPC on stdin/stdout
  ai ls                           List running agents
  ai ls --all                     Include finished runs
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
