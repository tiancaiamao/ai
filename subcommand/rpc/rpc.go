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
	fmt.Fprintf(os.Stderr, `ai - AI coding assistant

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
  --max-turns <n>          Maximum conversation turns (0 = unlimited)
  --timeout <duration>     Total execution timeout (0 = unlimited)
  --input <text>           Initial prompt to send after startup
  --model <id>             Override LLM model ID (e.g. claude-sonnet-4-20250514)

Flags for 'serve':
  --session <path>         Session file path
  --system-prompt <text>   Custom system prompt (@file to load from file)
  --max-turns <n>          Maximum conversation turns (0 = unlimited)
  --timeout <duration>     Total execution timeout (0 = unlimited)
  --http <addr>            Enable HTTP debug server (e.g., ':6060')
  --input <text>           Initial prompt to send after startup (serve only)
  --input-file <path>      Read initial prompt from file (serve only)
  --name <text>            Human-readable name for the run (serve only)
  --model <id>             Override LLM model ID (e.g. claude-sonnet-4-20250514)

Flags for 'ls':
  --all                    Include finished runs
  --json                   JSON output

Flags for 'watch':
  --id <run-id>            Run ID or prefix (auto-selects by cwd if omitted)
  --since <offset>         Start reading from byte offset (machine-readable)
  --follow                 Continuously stream events until agent exits
  --follow --pretty        Stream formatted output (readable conversation)
  --follow --summary       Stream final assistant text only (no intermediate output)
  --follow --timeout 2m    Timeout after duration (use with --pretty/--summary for polling)

Flags for 'send':
  --id <run-id>            Run ID or prefix (auto-selects by cwd if omitted)

Flags for 'kill':
  --id <run-id>            Run ID or prefix (auto-selects by cwd if omitted)
  --force                  Send SIGKILL instead of graceful abort

Examples:
  ai run                          Start agent with interactive TUI
  ai run --input "fix the bug"    Start with an initial prompt
  ai serve                        Start agent as background daemon
  ai serve --input "fix the bug"  Start daemon with an initial prompt
  ai ls                           List running agents
  ai send "hello"                 Send message to agent in current directory
  ai send "/session"              Send slash command
  ai watch                        Attach to agent's TUI
  ai kill                         Stop agent in current directory
  ai kill --id abc123             Stop specific run by ID
  ai kill --force                 Force kill (SIGKILL)
`)
}
