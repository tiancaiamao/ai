package main

import (
	"flag"
	"os"

	"log/slog"
)

func main() {
	mode := flag.String("mode", "", "Run mode (rpc|win). Default: win")
	sessionPathFlag := flag.String("session", "", "Session file path (rpc/win mode)")
	debugAddr := flag.String("http", "", "Enable HTTP debug server on specified address (e.g., ':6060')")
	windowName := flag.String("name", "", "window name (default +ai)")
	debug := flag.Bool("debug", false, "enable debug logging (win mode)")
	flag.Parse()

	if *mode != "rpc" {
		if err := runWinAI(*windowName, *debug, *sessionPathFlag, *debugAddr); err != nil {
			slog.Error("win-ai error", "error", err)
			os.Exit(1)
		}
		return
	}

	if err := runRPC(*sessionPathFlag, *debugAddr, os.Stdin, os.Stdout); err != nil {
		slog.Error("rpc error", "error", err)
		os.Exit(1)
	}
}
