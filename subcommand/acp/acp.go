// Package acp implements the 'ai acp' subcommand: an ACP (Agent Client
// Protocol) agent over stdio, compatible with agent-shell and other ACP
// clients (JSON-RPC 2.0, newline-delimited framing).
package acp

import (
	"flag"
	"fmt"
	"os"

	"github.com/tiancaiamao/ai/pkg/rpc"
	"github.com/tiancaiamao/ai/pkg/transport"
	"github.com/tiancaiamao/ai/subcommand/helpers"
)

// ACPSubcommand implements the 'ai acp' subcommand.
func ACPSubcommand() {
	fs := flag.NewFlagSet("acp", flag.ExitOnError)
	sessionPathFlag := fs.String("session", "", "Session file path")
	maxTurnsFlag := fs.Int("max-turns", 0, "Maximum conversation turns (0 = unlimited)")
	timeoutFlag := fs.Duration("timeout", 0, "Total execution timeout (0 = unlimited)")
	systemPromptFlag := fs.String("system-prompt", "", "Custom system prompt. Use '@' prefix to load from file (e.g., @/path/to/file.md)")
	debugAddr := fs.String("http", "", "Enable HTTP debug server on specified address (e.g., ':6060')")
	roleFlag := fs.String("role", "", "Agent role name (e.g. coder, orchestrator, validator). Loads ~/.ai/roles/<name>/agent.yaml")
	modelFlag := fs.String("model", "", `Override LLM model ID. Use "provider/id" for exact match (e.g. opencode/deepseek-v4-flash). Run "ai models" to list available options.`)
	fs.Parse(os.Args[1:])

	// Use fmt.Fprintf for startup errors because slog writes to io.Discard
	// during initialization (see logger.NewLogger).
	systemPrompt, err := helpers.ParseSystemPrompt(*systemPromptFlag)

	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Use fmt.Fprintf for startup errors because slog writes to io.Discard
	// during initialization (see logger.NewLogger).
	if err := rpc.RunACP(transport.NewStdio(os.Stdin, os.Stdout), *sessionPathFlag, *debugAddr, systemPrompt, *maxTurnsFlag, *timeoutFlag, *roleFlag, *modelFlag, ""); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
