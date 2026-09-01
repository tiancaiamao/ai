package main

import (
	"fmt"
	"os"

	"github.com/tiancaiamao/ai/subcommand/acp"
	"github.com/tiancaiamao/ai/subcommand/kill"
	"github.com/tiancaiamao/ai/subcommand/ls"
	"github.com/tiancaiamao/ai/subcommand/models"
	"github.com/tiancaiamao/ai/subcommand/run"
	"github.com/tiancaiamao/ai/subcommand/send"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	binPath := os.Args[0]
	subcmd := os.Args[1]
	os.Args = os.Args[1:]

	switch subcmd {
	case "-h", "--help", "help":
		printUsage()
		os.Exit(0)
	case "models":
		models.ModelsSubcommand()
	case "acp":
		acp.ACPSubcommand()
	case "run":
		run.RunSubcommand(binPath)
	case "serve":
		run.ServeSubcommand(binPath)
	case "watch":
		run.WatchSubcommand()
	case "ls":
		ls.LsSubcommand()
	case "send":
		send.SendSubcommand()
	case "kill":
		kill.KillSubcommand()
	case "login-codex":
		loginCodexSubcommand()
	default:
		fmt.Fprintf(os.Stderr, "ai: unknown command %q\n\n", subcmd)
		printUsage()
		os.Exit(1)
	}
}
