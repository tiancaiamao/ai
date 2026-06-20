package main

import (
	"os"

	"github.com/tiancaiamao/ai/pkg/cli"
)

func main() {
	if len(os.Args) < 2 {
		cli.PrintUsage()
		return
	}

	binPath := os.Args[0]
	subcmd := os.Args[1]
	os.Args = os.Args[1:]

	switch subcmd {
	case "rpc":
		cli.RPCSubcommand()
	case "run":
		cli.RunSubcommand(binPath)
	case "serve":
		cli.ServeSubcommand(binPath)
	case "ls":
		cli.LsSubcommand()
	case "watch":
		cli.WatchSubcommand()
	case "send":
		cli.SendSubcommand()
	case "kill":
		cli.KillSubcommand()
	default:
		os.Exit(1)
	}
}
