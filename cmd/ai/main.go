package main

import (
	"flag"
	"os"

	"log/slog"
)

func main() {
	mode := flag.String("mode", "", "Run mode (rpc|win|json). Default: win")
	sessionPathFlag := flag.String("session", "", "Session file path (rpc/win/json mode)")
	debugAddr := flag.String("http", "", "Enable HTTP debug server on specified address (e.g., ':6060')")
	windowName := flag.String("name", "", "window name (default +ai)")
	debug := flag.Bool("debug", false, "enable debug logging")
	flag.Parse()

	switch *mode {
	case "rpc":
		if err := runRPC(*sessionPathFlag, *debugAddr, os.Stdin, os.Stdout, *debug); err != nil {
			slog.Error("rpc error", "error", err)
			os.Exit(1)
		}
	case "json":
		prompts := flag.Args()
		if err := runJSON(*sessionPathFlag, *debugAddr, prompts, os.Stdout, *debug); err != nil {
			slog.Error("json error", "error", err)
			os.Exit(1)
		}
	case "win", "":
		if err := runWinAI(*windowName, *debug, *sessionPathFlag, *debugAddr); err != nil {
			slog.Error("win-ai error", "error", err)
			os.Exit(1)
		}
	default:
		slog.Error("invalid mode", "mode", *mode, "valid_modes", "rpc|win|json")
		os.Exit(1)
	}
}
