package main

import (
	"fmt"
	"os"

	"github.com/tiancaiamao/ai/subcommand/kill"
	"github.com/tiancaiamao/ai/subcommand/ls"
	"github.com/tiancaiamao/ai/subcommand/models"
	rpcsubcommand "github.com/tiancaiamao/ai/subcommand/rpc"
	"github.com/tiancaiamao/ai/subcommand/run"
	"github.com/tiancaiamao/ai/subcommand/send"
)

func main() {
	if len(os.Args) < 2 {
		rpcsubcommand.PrintUsage()
		os.Exit(1)
	}

	binPath := os.Args[0]
	subcmd := os.Args[1]
	os.Args = os.Args[1:]

	switch subcmd {
	case "-h", "--help", "help":
		rpcsubcommand.PrintUsage()
		os.Exit(0)
	case "models":
		models.ModelsSubcommand()
	case "rpc":
		rpcsubcommand.RPCSubcommand()
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
	default:
		fmt.Fprintf(os.Stderr, "ai: unknown command %q\n\n", subcmd)
		rpcsubcommand.PrintUsage()
		os.Exit(1)
	}
}
