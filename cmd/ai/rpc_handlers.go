package main

import (
	"io"
	_ "net/http/pprof"
	"time"

	"log/slog"

	"github.com/tiancaiamao/ai/pkg/agent"
	"github.com/tiancaiamao/ai/pkg/config"
	"github.com/tiancaiamao/ai/pkg/rpc"
)

func runRPC(sessionPath string, debugAddr string, input io.Reader, output io.Writer, customSystemPrompt string, maxTurns int, timeout time.Duration, agentConfigPath string, modelOverride string, runID string) error {
	// --- Construct rpcApp (config, model, session, tools, compactor, skills) ---
	app, err := newRPCApp(sessionPath, rpcAppSetupParams{
		customSystemPrompt: customSystemPrompt,
		maxTurns:           maxTurns,
		debugAddr:          debugAddr,
		agentConfigPath:    agentConfigPath,
		modelOverride:      modelOverride,
		runID:              runID,
	})
	if err != nil {
		return err
	}

	// --- Create agent context ---
	agentCtx := app.createBaseContext()

	// Apply tool filtering from agent.yaml (after skills are loaded & registered).
	if app.agentConfig != nil {
		if enabled := app.agentConfig.GetEnabledTools(); enabled != nil {
			agentCtx.SetAllowedTools(enabled)
			slog.Info("Applied tool whitelist from agent config", "tools", enabled)
		}
	}

	// --- Pre-config: sessionWriter, sessionComp, executor, toolOutputConfig ---
	sessionWriter := newSessionWriter(256)
	defer sessionWriter.Close()
	sessionComp := &sessionCompactor{
		session:   app.sess,
		compactor: app.compactor,
		writer:    sessionWriter,
	}
	app.sessionWriter = sessionWriter
	app.sessionComp = sessionComp

	concurrencyConfig := app.cfg.Concurrency
	if concurrencyConfig == nil {
		concurrencyConfig = config.DefaultConcurrencyConfig()
	}
	executor := agent.NewToolExecutor(
		concurrencyConfig.MaxConcurrentTools,
		concurrencyConfig.QueueTimeout,
	)

	toolOutputConfig := app.cfg.ToolOutput
	if toolOutputConfig == nil {
		toolOutputConfig = config.DefaultToolOutputConfig()
	}
	app.toolOutputConfig = toolOutputConfig

	// Build LoopConfig with all settings
	loopCfg := app.cfg.ToLoopConfig(
		config.WithCompactors([]agent.Compactor{app.ctxManager, sessionComp}),
		config.WithContextWindow(app.currentContextWindow),
		config.WithToolCallCutoff(app.compactorConfig.ToolCallCutoff),
		config.WithExecutor(executor),
		config.WithToolOutputLimits(agent.ToolOutputLimits{
			MaxChars: toolOutputConfig.MaxChars,
		}),
	)

	// Set model and apiKey
	loopCfg.Model = app.model
	loopCfg.APIKey = app.apiKey
	loopCfg.GetWorkingDir = app.ws.GetCWD
	loopCfg.GetStartupPath = app.ws.GetInitialCWD
	loopCfg.RunID = app.runID
	loopCfg.GetSessionDir = func() string {
		if app.sess != nil {
			return app.sess.GetDir()
		}
		return ""
	}

	// Set max turns limit if specified
	if maxTurns > 0 {
		loopCfg.MaxTurns = maxTurns
		slog.Info("Max turns limit set", "max_turns", maxTurns)
	}

	// Apply agent config hooks if available
	if app.agentConfig != nil {
		loopCfg.Hooks = app.agentConfig.BuildHooks()
	}

	app.loopCfg = loopCfg

	// Create agent with LoopConfig
	ag := agent.NewAgentFromConfigWithContext(app.model, app.apiKey, agentCtx, loopCfg)
	defer ag.Shutdown()
	ag.SetThinkingLevel("high")
	app.ag = ag

	// Initialize checkpoint manager for persistent state
	if app.sess != nil {
		sessionDir := app.sess.GetDir()
		if mgr, err := agent.NewAgentContextCheckpointManager(sessionDir); err != nil {
			slog.Warn("Failed to create checkpoint manager", "error", err)
			app.checkpointMgr = nil
		} else {
			app.checkpointMgr = mgr
			defer func() {
				if app.checkpointMgr != nil {
					app.checkpointMgr.Close()
				}
			}()
		}
	}

	slog.Info("Auto-compact enabled", "maxMessages", app.compactorConfig.MaxMessages, "maxTokens", app.compactorConfig.MaxTokens)
	slog.Info("Concurrency control enabled", "maxConcurrentTools", concurrencyConfig.MaxConcurrentTools, "toolTimeout", concurrencyConfig.ToolTimeout)
	slog.Info("Tool output truncation", "maxChars", toolOutputConfig.MaxChars)

	// --- Create RPC server ---
	server := rpc.NewServer()
	server.SetOutput(output)
	app.server = server

	// Start timeout watchdog if timeout is set.
	// Must be after server creation so server.Cancel() is available.
	if timeout > 0 {
		go func() {
			<-time.After(timeout)
			slog.Warn("[RPC] Timeout reached, aborting agent", "timeout", timeout)
			ag.Abort()
			// Cancel the RPC server so RunWithIO unblocks and the process exits.
			// Without this, the process lingers forever waiting for stdin.
			server.Cancel()
		}()
	}

	// --- Register all handlers ---
	validToolSummaryStrategies := map[string]bool{"llm": true, "heuristic": true, "off": true}
	validToolSummaryAutomations := map[string]bool{"off": true, "fallback": true, "always": true}
	validSteeringModes := map[string]bool{"all": true, "immediate": true, "one-at-a-time": true}
	validFollowUpModes := map[string]bool{"all": true, "immediate": true, "one-at-a-time": true}
	validThinkingLevels := map[string]bool{"off": true, "minimal": true, "low": true, "medium": true, "high": true, "xhigh": true}

	app.registerHandlers(
		validToolSummaryStrategies,
		validToolSummaryAutomations,
		validSteeringModes,
		validFollowUpModes,
		validThinkingLevels,
	)

	// --- Build skill commands list ---
	app.skillCommands = make([]rpc.SlashCommand, 0)
	for _, cmd := range server.ListSlashCommands() {
		if cmd.Hidden {
			continue
		}
		app.skillCommands = append(app.skillCommands, rpc.SlashCommand{
			Name:        cmd.Name,
			Description: cmd.Description,
		})
	}
	for _, s := range app.skillResult.Skills {
		app.skillCommands = append(app.skillCommands, rpc.SlashCommand{
			Name:        "/skill:" + s.Name,
			Description: s.Description,
		})
	}

	// --- Start event emitter ---
	shutdownEmitter, eventEmitterDone := app.initEventEmitter()

	// --- Emit start event ---
	app.emitStartEvent()

	// --- Start debug server if enabled ---
	app.startDebugServer()

	// --- Run RPC server ---
	slog.Info("RPC server started", "model", app.model.ID, "cwd", app.cwd)
	slog.Info("Waiting for commands...")
	runErr := server.RunWithIO(input, output)

	// Server stopped, event emitter will exit automatically
	slog.Info("RPC server stopped, waiting for cleanup...")

	// Wait for agent to complete
	slog.Info("Waiting for agent to complete...")
	ag.Wait()

	close(shutdownEmitter)
	<-eventEmitterDone

	slog.Info("Agent completed, exiting...")
	return runErr
}
