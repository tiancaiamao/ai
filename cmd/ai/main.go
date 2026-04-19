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

// parseSystemPrompt parses the --system-prompt flag.
// If the value starts with '@', it reads the file content.
// Otherwise, it returns the value as-is.
func parseSystemPrompt(systemPromptFlag string) string {
	if systemPromptFlag == "" {
		return ""
	}
	// If starts with '@', read file
	if strings.HasPrefix(systemPromptFlag, "@") {
		filePath := strings.TrimPrefix(systemPromptFlag, "@")
		filePath = strings.TrimSpace(filePath)
		if filePath == "" {
			slog.Warn("empty file path after '@' in --system-prompt flag")
			return ""
		}
		content, err := os.ReadFile(filePath)
		if err != nil {
			slog.Error("failed to read system-prompt file", "path", filePath, "error", err)
			return ""
		}
		// Limit file size to 64KB
		if len(content) > 64*1024 {
			slog.Warn("system-prompt file too large, truncating to 64KB", "size", len(content))
			content = content[:64*1024]
		}
		return string(content)
	}
	// Otherwise, use the value as-is
	return systemPromptFlag
}

func main() {
	mode := flag.String("mode", "", "Run mode (rpc|win|json|headless). Default: win")
	sessionPathFlag := flag.String("session", "", "Session file path (rpc/win/json/headless mode)")
	maxTurnsFlag := flag.Int("max-turns", 0, "Maximum conversation turns (0 = unlimited, headless mode only)")
	toolsFlag := flag.String("tools", "", "Comma-separated list of allowed tools (headless mode only)")
	timeoutFlag := flag.Duration("timeout", 0, "Total execution timeout (0 = unlimited, headless mode only)")
	systemPromptFlag := flag.String("system-prompt", "", "Custom system prompt. Use '@' prefix to load from file (e.g., @/path/to/file.md)")
	debugAddr := flag.String("http", "", "Enable HTTP debug server on specified address (e.g., ':6060')")
	windowName := flag.String("name", "", "window name (default +ai)")
	flag.Parse()

	// Parse system-prompt flag: if starts with '@', read file content
	systemPrompt := parseSystemPrompt(*systemPromptFlag)

	switch *mode {
	case "rpc":
		if err := runRPC(*sessionPathFlag, *debugAddr, os.Stdin, os.Stdout, systemPrompt); err != nil {
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
		if err := runHeadless(*sessionPathFlag, *maxTurnsFlag, tools, *timeoutFlag, systemPrompt, prompts, os.Stdout); err != nil {
			slog.Error("headless error", "error", err)
			os.Exit(1)
		}
	case "win", "":
		if err := runWinAI(*windowName, *sessionPathFlag, *debugAddr); err != nil {
			slog.Error("win-ai error", "error", err)
			os.Exit(1)
		}
	default:
		slog.Error("invalid mode", "mode", *mode, "valid_modes", "rpc|win|json|headless")
		os.Exit(1)
	}
}