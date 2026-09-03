// Package acp implements the 'ai acp' subcommand: an ACP (Agent Client
// Protocol) agent over stdio, compatible with agent-shell and other ACP
// clients (JSON-RPC 2.0, newline-delimited framing).
//
// Unlike a bare stdio agent, `ai acp` registers itself as a run (run.json,
// events.jsonl and a control socket under ~/.ai/runs/<id>/), so it shows up
// in `ai ls` and can be observed and driven externally (`ai send`, `ai watch`,
// `ai history --id <run>`).
package acp

import (
	"flag"
	"fmt"
	"os"

	"github.com/tiancaiamao/ai/subcommand/run"
)

// ACPSubcommand implements the 'ai acp' subcommand.
func ACPSubcommand() {
	fs := flag.NewFlagSet("acp", flag.ExitOnError)
	sessionPathFlag := fs.String("session", "", "Session file path")
	maxTurnsFlag := fs.Int("max-turns", 0, "Maximum conversation turns (0 = unlimited)")
	timeoutFlag := fs.Duration("timeout", 0, "Total execution timeout (0 = unlimited)")
	systemPromptFlag := fs.String("system-prompt", "", "Custom system prompt. Use '@' prefix to load from file (e.g., @/path/to/file.md)")
	agentConfigFlag := fs.String("agent-config", "", "Path to agent.yaml configuration file")
	debugAddr := fs.String("http", "", "Enable HTTP debug server on specified address (e.g., ':6060')")
	roleFlag := fs.String("role", "", "Agent role name (e.g. coder, orchestrator, validator). Loads ~/.ai/roles/<name>/agent.yaml")
	modelFlag := fs.String("model", "", `Override LLM model ID. Use "provider/id" for exact match (e.g. opencode/deepseek-v4-flash). Run "ai models" to list available options.`)
	fs.Parse(os.Args[1:])

	// Startup errors must go to stderr — stdout is the ACP channel.
	err := run.StdioServe(run.ServeConfig{
		Session:      *sessionPathFlag,
		SystemPrompt: *systemPromptFlag,
		MaxTurns:     *maxTurnsFlag,
		Timeout:      *timeoutFlag,
		HTTP:         *debugAddr,
		AgentConfig:  *agentConfigFlag,
		Role:         *roleFlag,
		Model:        *modelFlag,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
