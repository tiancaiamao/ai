// ai-win — ad editor integration for ai agent.
//
// Spawns "ai --mode rpc" as a subprocess and bridges it to the ad editor
// via the ad client and REPL framework.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"unicode/utf8"

	"log/slog"

	"github.com/sminez/ad/win/pkg/ad"
	"github.com/sminez/ad/win/pkg/repl"
	"github.com/tiancaiamao/ai/pkg/config"
	"github.com/tiancaiamao/ai/win/internal/winai"
)

const sendPrefix = ";; "

func main() {
	sessionPath := flag.String("session", "", "Session file path")
	debugAddr := flag.String("http", "", "Enable HTTP debug server (e.g., ':6060')")
	windowName := flag.String("name", "", "Window name (default +ai)")
	flag.Parse()

	if err := run(windowName, sessionPath, debugAddr); err != nil {
		slog.Error("ai-win error", "error", err)
		os.Exit(1)
	}
}

func run(windowName *string, sessionPath *string, debugAddr *string) error {
	// Load ai config for logger setup.
	configPath, err := config.GetDefaultConfigPath()
	if err != nil {
		return fmt.Errorf("failed to get config path: %w", err)
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		cfg, _ = config.LoadConfig(configPath)
	}

	log, err := cfg.Log.CreateLogger()
	if err != nil {
		return fmt.Errorf("failed to create logger: %w", err)
	}
	slog.SetDefault(log)

	aiLogPath := config.ResolveLogPath(cfg.Log)

	// Connect to ad editor.
	client, err := ad.NewClient()
	if err != nil {
		return fmt.Errorf("unable to connect to ad: %w", err)
	}
	defer client.Close()
	client.SetLogger(log)

		// Build args for the ai subprocess.
	args := []string{"--mode", "rpc"}
	if *sessionPath != "" {
		args = append(args, "--session", *sessionPath)
	}
	if *debugAddr != "" {
		args = append(args, "--http", *debugAddr)
	} else {
		// Prevent AiInterpreter.Start() from auto-appending "-http :6060".
		// Old in-process mode only enabled debug server when explicitly requested.
		args = append(args, "--http", "")
	}

		// Create interpreter that spawns "ai --mode rpc" as subprocess.
	// Note: do NOT call interpreter.Start() here — repl.Handler.Start() will do that.
	interpreter := winai.NewAiInterpreter("ai", args)
	interpreter.SetAdClient(client)
	defer interpreter.Stop()

	name := *windowName
	if name == "" {
		name = "+ai"
	}

	replCfg := repl.Config{
		Prompt:     "",
		WindowName: name,
		WelcomeMessage: `# Ai REPL
#
# Use send-to-win to send prompts (prefix ";; ").
# Controls: use win-ctl or send /command via send-to-win.
#
`,
		SendPrefix:            sendPrefix,
		InputPrefix:           "",
		EchoSendInput:         false,
		EnableKeyboardExecute: false,
		EnableExecute:         false,
		LogPath:               aiLogPath,
	}

	handler, err := repl.NewHandler(replCfg, client, interpreter)
	if err != nil {
		return fmt.Errorf("unable to create REPL handler: %w", err)
	}

	if err := handler.Start(); err != nil {
		return fmt.Errorf("repl start: %w", err)
	}
	defer handler.Stop()

	eventHandler := &recoveringEventHandler{
		inner:      handler.NewEventHandler(),
		client:     client,
		bufferID:   handler.BufferID(),
		sendPrefix: sendPrefix,
	}
	if err := client.RunEventFilter(handler.BufferID(), eventHandler); err != nil {
		return fmt.Errorf("REPL error: %w", err)
	}

	return nil
}

// recoveringEventHandler wraps repl event handling and restores full send-to-win input
// when ad's event txt payload is truncated.
type recoveringEventHandler struct {
	inner      ad.EventHandler
	client     *ad.Client
	bufferID   string
	sendPrefix string
}

func (h *recoveringEventHandler) HandleInsert(source ad.EventSource, from, to int, txt string, client *ad.Client) (ad.Outcome, error) {
	if source == ad.SourceFsys && strings.HasPrefix(txt, h.sendPrefix) {
		eventChars := utf8.RuneCountInString(txt)
		if to > from && (to-from) > eventChars {
			if recovered, err := h.readBodyRange(from, to); err == nil && strings.HasPrefix(recovered, h.sendPrefix) {
				txt = recovered
			}
		}
	}
	return h.inner.HandleInsert(source, from, to, txt, client)
}

func (h *recoveringEventHandler) HandleDelete(source ad.EventSource, from, to int, client *ad.Client) (ad.Outcome, error) {
	return h.inner.HandleDelete(source, from, to, client)
}

func (h *recoveringEventHandler) HandleExecute(source ad.EventSource, from, to int, txt string, client *ad.Client) (ad.Outcome, error) {
	return h.inner.HandleExecute(source, from, to, txt, client)
}

func (h *recoveringEventHandler) readBodyRange(from, to int) (string, error) {
	body, err := h.client.ReadBody(h.bufferID)
	if err != nil {
		return "", err
	}
	runes := []rune(body)
	if from < 0 || to < from || to > len(runes) {
		return "", fmt.Errorf("invalid range %d..%d (body=%d)", from, to, len(runes))
	}
	return string(runes[from:to]), nil
}