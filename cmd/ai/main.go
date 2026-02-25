package main

import (
	"flag"
	"os"
	"strings"

	"log/slog"
)

func parseToolsFlag(toolsFlag string) []string {
	if toolsFlag == "" {
		return nil
	}
	tools := strings.Split(toolsFlag, ",")
	result := make([]string, 0, len(tools))
	for _, t := range tools {
		t = strings.TrimSpace(t)
		if t != "" {
			result = append(result, t)
		}
	}
	return result
}

func main() {
	mode := flag.String("mode", "", "Run mode (rpc|win|json|headless|http|qq). Default: win")
	sessionPathFlag := flag.String("session", "", "Session file path (rpc/win/json/headless mode)")
	noSessionFlag := flag.Bool("no-session", false, "Run without session persistence (headless mode only)")
	maxTurnsFlag := flag.Int("max-turns", 0, "Maximum conversation turns (0 = unlimited, headless mode only)")
	toolsFlag := flag.String("tools", "", "Comma-separated list of allowed tools (headless mode only)")
	subagentFlag := flag.Bool("subagent", false, "Run as subagent with focused system prompt (headless mode only)")
	debugAddr := flag.String("debug", "", "Enable HTTP debug server on specified address (e.g., ':6060')")
	httpAddr := flag.String("http-addr", ":8080", "HTTP server address for http mode (default: ':8080')")
	onebotURL := flag.String("onebot-url", "", "OneBot API URL for QQ bot mode (e.g., 'http://localhost:3000')")
	onebotSecret := flag.String("onebot-secret", "", "OneBot secret for webhook verification (optional)")
	windowName := flag.String("name", "", "window name (default +ai)")
	flag.Parse()

	switch *mode {
	case "rpc":
		if err := runRPC(*sessionPathFlag, *debugAddr, os.Stdin, os.Stdout); err != nil {
			slog.Error("rpc error", "error", err)
			os.Exit(1)
		}
	case "json":
		prompts := flag.Args()
		if err := runJSON(*sessionPathFlag, *debugAddr, prompts, os.Stdout); err != nil {
			slog.Error("json error", "error", err)
			os.Exit(1)
		}
	case "headless":
		prompts := flag.Args()
		tools := parseToolsFlag(*toolsFlag)
		if err := runHeadless(*sessionPathFlag, *noSessionFlag, *maxTurnsFlag, tools, *subagentFlag, prompts, os.Stdout); err != nil {
			slog.Error("headless error", "error", err)
			os.Exit(1)
		}
	case "http":
		if err := runHTTP(*httpAddr, *sessionPathFlag, *debugAddr); err != nil {
			slog.Error("http error", "error", err)
			os.Exit(1)
		}
	case "qq":
		if err := runQQBot(*onebotURL, *onebotSecret, *sessionPathFlag); err != nil {
			slog.Error("qq bot error", "error", err)
			os.Exit(1)
		}
	case "win", "":
		if err := runWinAI(*windowName, *sessionPathFlag, *debugAddr); err != nil {
			slog.Error("win-ai error", "error", err)
			os.Exit(1)
		}
	default:
		slog.Error("invalid mode", "mode", *mode, "valid_modes", "rpc|win|json|headless|http|qq")
		os.Exit(1)
	}
}