package rpc

import (
	_ "net/http/pprof"

	"log/slog"

	"github.com/tiancaiamao/ai/pkg/agent"
	"github.com/tiancaiamao/ai/pkg/config"
)

// setupAgent wires the agent loop: agent context, session writer, tool
// executor, compactor and LoopConfig. Shared by ACP.
// The caller owns the returned sessionWriter (Close) and agent (Shutdown).
func (app *rpcApp) setupAgent(maxTurns int) (*agent.Agent, *sessionWriter, error) {
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
	sessionComp := &sessionCompactor{
		compactor: app.compactor,
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
	// Note: proactiveCompactor and sessionComp both wrap the same underlying *compact.Compactor
	// We only need one; sessionComp provides thread-safe swapping capability
	loopCfg := app.cfg.ToLoopConfig(
		config.WithCompactor(sessionComp),
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
	loopCfg.Role = app.role
	loopCfg.AgentContextPrefix = app.agentContextPrefix
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
	ag.SetThinkingLevel(app.cfg.ThinkingLevel)
	app.ag = ag

	slog.Info("Auto-compact enabled", "maxMessages", app.compactorConfig.MaxMessages, "maxTokens", app.compactorConfig.MaxTokens)
	slog.Info("Concurrency control enabled", "maxConcurrentTools", concurrencyConfig.MaxConcurrentTools)
	slog.Info("Tool output truncation", "maxChars", toolOutputConfig.MaxChars)

	return ag, sessionWriter, nil
}

// registerAllHandlers registers all protocol + slash command handlers with
// their validation maps. Shared by ACP.
func (app *rpcApp) registerAllHandlers() {
	validToolSummaryAutomations := map[string]bool{"off": true, "fallback": true, "always": true}
	validSteeringModes := map[string]bool{"all": true, "immediate": true, "one-at-a-time": true}
	validFollowUpModes := map[string]bool{"all": true, "immediate": true, "one-at-a-time": true}
	validThinkingLevels := map[string]bool{"off": true, "minimal": true, "low": true, "medium": true, "high": true, "xhigh": true}

	app.registerHandlers(
		validToolSummaryAutomations,
		validSteeringModes,
		validFollowUpModes,
		validThinkingLevels,
	)
}

// buildSkillCommands populates app.skillCommands with the registry's visible
// slash commands plus one /skill:<name> entry per installed skill.
func (app *rpcApp) buildSkillCommands() {
	app.skillCommands = make([]SlashCommand, 0)
	for _, cmd := range app.commands.ListCommands() {

		if cmd.Hidden {
			continue
		}
		app.skillCommands = append(app.skillCommands, SlashCommand{
			Name:        cmd.Name,
			Description: cmd.Description,
		})
	}
	for _, s := range app.skillResult.Skills {
		app.skillCommands = append(app.skillCommands, SlashCommand{
			Name:        "/skill:" + s.Name,
			Description: s.Description,
		})
	}
}
