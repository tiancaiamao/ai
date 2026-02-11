package main

import (
	"fmt"
	"io"

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
		if err := runRPC(sessionPath, debugAddr, rpcInReader, rpcOutWriter); err != nil {
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

	if err := handler.Run(); err != nil {
		if debug {
			slog.Error("REPL error", "error", err)
		}
		return fmt.Errorf("REPL error: %w", err)
	}

	return nil
}
