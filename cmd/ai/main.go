package main

import (
	"os"

	"github.com/tiancaiamao/ai/subcommand/kill"
	"github.com/tiancaiamao/ai/subcommand/ls"
	rpcsubcommand "github.com/tiancaiamao/ai/subcommand/rpc"
	"github.com/tiancaiamao/ai/subcommand/run"
	"github.com/tiancaiamao/ai/subcommand/send"
)

func main() {
	if len(os.Args) < 2 {
		// TODO: Add usage print from individual packages
		os.Exit(1)
		return
	}

	binPath := os.Args[0]
	subcmd := os.Args[1]
	os.Args = os.Args[1:]

	switch subcmd {
	case "rpc":
		rpcsubcommand.RPCSubcommand()
	case "run":
		run.RunSubcommand(binPath)
	case "serve":
		run.ServeSubcommand(binPath)
	case "ls":
		ls.LsSubcommand()
	case "watch":
		run.WatchSubcommand()
	case "send":
		send.SendSubcommand()
	case "kill":
		kill.KillSubcommand()
	default:
		os.Exit(1)
	}
}
