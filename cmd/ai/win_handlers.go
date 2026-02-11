package main

import (
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"log/slog"

	"github.com/sminez/ad/win/pkg/ad"
	"github.com/sminez/ad/win/pkg/repl"
	"github.com/tiancaiamao/ai/internal/winai"
	"github.com/tiancaiamao/ai/pkg/config"
)

func runWinAI(windowName string, debug bool, sessionPath string, debugAddr string) error {
	// Initialize logger early so all slog calls go to the log file
	configPath, err := config.GetDefaultConfigPath()
	if err != nil {
		return fmt.Errorf("failed to get config path: %w", err)
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		// Continue with defaults if config loading fails
		cfg, _ = config.LoadConfig(configPath)
	}
	if debug {
		cfg.Log.Level = "debug"
	}

	// Initialize logger from config
	log, err := cfg.Log.CreateLogger()
	if err != nil {
		return fmt.Errorf("failed to create logger: %w", err)
	}

	// Set the default slog logger to use our configured logger
	// This ensures all slog.Info/Error/etc calls go to the log file
	slog.SetDefault(log)

	aiLogPath := config.ResolveLogPath(cfg.Log)
	if aiLogPath != "" {
		slog.Info("Log file:", "value", aiLogPath)
	}

	if debug {
		slog.Info("Starting win-ai REPL with debug logging")
	}

	rpcInReader, rpcInWriter := io.Pipe()
	rpcOutReader, rpcOutWriter := io.Pipe()
	rpcErrReader, rpcErrWriter := io.Pipe()
	_ = rpcErrWriter.Close()

	go func() {
		defer rpcOutWriter.Close()
		if err := runRPC(sessionPath, debugAddr, rpcInReader, rpcOutWriter, debug); err != nil {
			slog.Error("rpc error", "error", err)
		}
	}()

	client, err := ad.NewClient()
	if err != nil {
		return fmt.Errorf("unable to connect to ad: %w", err)
	}
	defer func() {
		if debug {
			slog.Info("Closing client connection")
		}
		client.Close()
	}()

	// Pass the configured logger to the ad client
	client.SetLogger(log)

	if debug {
		slog.Info("Connected to ad successfully")
	}

	interpreter := winai.NewAiInterpreterWithIO(rpcInWriter, rpcOutReader, rpcErrReader, debug)
	interpreter.SetAdClient(client)
	defer interpreter.Stop()

	name := windowName
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
		Debug:                 debug,
		LogPath:               aiLogPath,
	}

	handler, err := repl.NewHandler(replCfg, client, interpreter)
	if err != nil {
		return fmt.Errorf("unable to create REPL handler: %w", err)
	}

	if debug {
		slog.Info("Starting REPL...")
	}

	if err := handler.Start(); err != nil {
		if debug {
			slog.Error("REPL start error", "error", err)
		}
		return fmt.Errorf("repl start: %w", err)
	}
	defer func() {
		if err := handler.Stop(); err != nil && debug {
			slog.Error("REPL stop error", "error", err)
		}
	}()

	eventHandler := &recoveringEventHandler{
		inner:      handler.NewEventHandler(),
		client:     client,
		bufferID:   handler.BufferID(),
		sendPrefix: sendPrefix,
	}
	if err := client.RunEventFilter(handler.BufferID(), eventHandler); err != nil {
		if debug {
			slog.Error("REPL event loop error", "error", err)
		}
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
