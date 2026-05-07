package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"

	"github.com/tiancaiamao/ai/pkg/agent"
	"github.com/tiancaiamao/ai/pkg/compact"
	"github.com/tiancaiamao/ai/pkg/config"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/rpc"
	traceevent "github.com/tiancaiamao/ai/pkg/traceevent"
)

// RegisterSlashCommands registers all slash command handlers on core.Server.
func (c *RPCCore) RegisterSlashCommands() {
	// === Slash command handlers ===
	// All commands below are registered as slash commands.
	// They are invoked via the prompt channel: {"type": "prompt", "message": "/command args"}
	// and intercepted by the prompt handler before entering the agent loop.
	// For backward compatibility, they also handle direct JSON-RPC calls where
	// args is raw JSON from cmd.Data (e.g. {"provider":"xxx","modelId":"yyy"}).

	c.Server.RegisterSlash("clear_session", "Clear all messages in the current session", c.handleClearSession)
	c.Server.RegisterSlash("new_session", "Create a new session and switch to it", c.HandleNewSession)
	c.Server.RegisterSlash("list_sessions", "List all sessions with their metadata", c.HandleListSessions)
	c.Server.RegisterSlash("switch_session", "Switch to a session by name or ID", c.HandleSwitchSession)
	c.Server.RegisterSlash("delete_session", "Delete a session by ID", c.HandleDeleteSession)
	c.Server.RegisterSlash("set_session_name", "Set a human-readable name for the current session", c.HandleSetSessionName)
	c.Server.RegisterSlash("get_state", "Get the current agent state (model, session, streaming status)", c.handleGetState)
	c.Server.RegisterSlash("get_messages", "Get all messages in the current session", c.handleGetMessages)
	c.Server.RegisterSlash("compact", "Compact conversation history to reduce context size", c.handleCompact)
	c.Server.RegisterSlash("get_available_models", "List all available models", c.handleGetAvailableModels)
	c.Server.RegisterSlash("set_model", "Set the active model by ID", c.handleSetModel)
	c.Server.RegisterSlash("cycle_model", "Cycle to the next available model", c.handleCycleModel)

	skillCommands := buildSkillCommands(c.SkillResult.Skills)

	c.Server.RegisterSlash("get_commands", "List available slash commands and skills", func(args string) (any, error) {
		slog.Info("Received get_commands")
		return map[string]any{"commands": skillCommands}, nil
	})

	c.Server.RegisterSlash("get_session_stats", "Get session statistics (token counts, message count)", c.handleGetSessionStats)
		c.Server.RegisterSlash("set_auto_retry", "Configure automatic retry on LLM errors", c.handleSetAutoRetry)
	c.Server.RegisterSlash("abort_retry", "Abort the current automatic retry", c.handleAbortRetry)
	c.Server.RegisterSlash("export_html", "Export the current session as HTML", c.handleExportHTML)
	c.Server.RegisterSlash("set_auto_compaction", "Configure automatic context compaction settings", c.handleSetAutoCompaction)
	c.Server.RegisterSlash("set_tool_call_cutoff", "Set the maximum number of tool calls per turn", c.handleSetToolCallCutoff)

	validToolSummaryStrategies := map[string]bool{
		"llm":       true,
		"heuristic": true,
		"off":       true,
	}
	validToolSummaryAutomations := map[string]bool{
		"off":      true,
		"fallback": true,
		"always":   true,
	}

	c.Server.RegisterSlash("set_tool_summary_strategy", "Set how tool outputs are summarized", func(args string) (any, error) {
		return c.handleSetToolSummaryStrategy(args, validToolSummaryStrategies)
	})

	c.Server.RegisterSlash("set_tool_summary_automation", "Configure automatic tool output summarization", func(args string) (any, error) {
		return c.handleSetToolSummaryAutomation(args, validToolSummaryAutomations)
	})

	c.Server.RegisterSlash("set_trace_events", "Enable/disable trace event categories", c.handleSetTraceEvents)
	c.Server.RegisterSlash("get_trace_events", "Get current trace event configuration", c.handleGetTraceEvents)
	c.Server.RegisterSlash("get_workflow_status", "Get workflow task status", c.handleGetWorkflowStatus)

	validSteeringModes := map[string]bool{
		"all":           true,
		"immediate":     true,
		"one-at-a-time": true,
	}
	validFollowUpModes := map[string]bool{
		"all":           true,
		"immediate":     true,
		"one-at-a-time": true,
	}

	c.Server.RegisterSlash("set_steering_mode", "Set how steering messages are queued", func(args string) (any, error) {
		return c.handleSetSteeringMode(args, validSteeringModes)
	})

	c.Server.RegisterSlash("set_follow_up_mode", "Set how follow-up messages are delivered", func(args string) (any, error) {
		return c.handleSetFollowUpMode(args, validFollowUpModes)
	})

	validThinkingLevels := map[string]bool{
		"off":     true,
		"minimal": true,
		"low":     true,
		"medium":  true,
		"high":    true,
		"xhigh":   true,
	}
	thinkingCycle := []string{"off", "minimal", "low", "medium", "high", "xhigh"}

	c.Server.RegisterSlash("set_thinking_level", "Set the thinking/reasoning level (off/low/medium/high)", func(args string) (any, error) {
		return c.handleSetThinkingLevel(args, validThinkingLevels)
	})

	c.Server.RegisterSlash("cycle_thinking_level", "Cycle to the next thinking level", func(args string) (any, error) {
		return c.handleCycleThinkingLevel(thinkingCycle)
	})

	c.Server.RegisterSlash("get_last_assistant_text", "Get the last assistant text response", c.handleGetLastAssistantText)
	c.Server.RegisterSlash("get_fork_messages", "Get messages for a fork point", c.handleGetForkMessages)
	c.Server.RegisterSlash("get_tree", "Get the conversation tree structure", c.handleGetTree)
	c.Server.RegisterSlash("resume_on_branch", "Resume generation on a specific branch", c.HandleResumeOnBranch)
	c.Server.RegisterSlash("fork", "Fork the conversation at a specific entry point", c.HandleFork)

	// --- Slash command aliases (user-facing short names) ---
	// These map the short command names users type (e.g. /help, /session)
	// to the canonical RPC handlers registered above.

	// Simple aliases: forward to an existing handler.
	registerAlias := func(alias, desc, canonical string) {
		c.Server.RegisterSlash(alias, desc, func(args string) (any, error) {
			h, ok := c.Server.GetSlashHandler(canonical)
			if !ok {
				return nil, fmt.Errorf("unknown command: /%s", canonical)
			}
			return h(args)
		})
	}

	// /help — list all registered slash commands
	c.Server.RegisterSlash("help", "Show available slash commands", func(args string) (any, error) {
		commands := c.Server.ListSlashCommands()
		return map[string]any{
			"commands": commands,
		}, nil
	})

	registerAlias("skills", "List available skills", "get_commands")
	registerAlias("session", "Get the current agent state (model, session, streaming status)", "get_state")
	registerAlias("new", "Create a new session", "new_session")
	registerAlias("messages", "Get session messages", "get_messages")
	registerAlias("tree", "Show conversation tree", "get_tree")
	registerAlias("thinking", "Set thinking level (off/low/medium/high)", "set_thinking_level")
	registerAlias("trace-events", "Configure trace events", "set_trace_events")
	registerAlias("model-select", "Select a model", "model")

	c.Server.RegisterSlash("model", "List models or set the active model", c.handleModel)
	c.Server.RegisterSlash("resume", "List sessions or resume a session by ID/name", c.handleResume)
	c.Server.RegisterSlash("context", "Show current state, session stats, and available models", c.handleContext)
	c.Server.RegisterSlash("toggle", "Toggle display settings (thinking, prefix)", c.handleToggle)
	c.Server.RegisterSlash("set_thinking_display", "Toggle thinking display on/off", c.handleSetThinkingDisplay)
	c.Server.RegisterSlash("set_tools_display", "Toggle tools display on/off", c.handleSetToolsDisplay)
	c.Server.RegisterSlash("set_prefix_display", "Toggle prefix display on/off", c.handleSetPrefixDisplay)
	c.Server.RegisterSlash("set_busy_mode", "Set behavior when agent is busy (steer, follow-up, reject)", c.handleSetBusyMode)
	c.Server.RegisterSlash("show", "Show agent settings or pipeline info", c.handleShow)
	c.Server.RegisterSlash("quit", "Exit the application", c.handleQuit)
	c.Server.RegisterSlash("abort", "Abort the current agent execution", c.handleSlashAbort)
	c.Server.RegisterSlash("follow-up", "Add a follow-up message when agent is busy", c.handleSlashFollowUp)
	c.Server.RegisterSlash("steer", "Steer the current agent execution", c.handleSlashSteer)
}

// --- Individual command handler methods ---

func (c *RPCCore) handleClearSession(args string) (any, error) {
	slog.Info("Received clear_session")
	if err := c.Sess.Clear(); err != nil {
		return nil, err
	}
	c.SetAgentContext(c.CreateBaseContext())
	slog.Info("Session cleared")
	return nil, nil
}

func (c *RPCCore) handleGetState(args string) (any, error) {
	slog.Info("Received get_state")
	compactionState := buildCompactionState(c.CompactorConfig, c.Compactor)
	c.StateMu.Lock()
	currentSessionID := c.SessionID
	currentSessionName := c.SessionName
	streaming := c.IsStreaming
	compacting := c.IsCompacting
	thinkingLevel := c.CurrentThinkingLevel
	autoCompact := c.AutoCompactionEnabled
	currentSteeringMode := c.SteeringMode
	currentFollowUpMode := c.FollowUpMode
	modelInfo := c.CurrentModelInfo
	c.StateMu.Unlock()

	aiLogPath := c.TraceOutputPath
	if handler := traceevent.GetHandler(); handler != nil {
		if fh, ok := handler.(*traceevent.FileHandler); ok {
			aiLogPath = fh.TraceFilePath("")
		}
	}

	return &rpc.SessionState{
		Model:                 &modelInfo,
		ThinkingLevel:         thinkingLevel,
		IsStreaming:           streaming,
		IsCompacting:          compacting,
		SteeringMode:          currentSteeringMode,
		FollowUpMode:          currentFollowUpMode,
		SessionFile:           c.Sess.GetPath(),
		SessionID:             currentSessionID,
		SessionName:           currentSessionName,
		AIPid:                 os.Getpid(),
		AILogPath:             aiLogPath,
		AIWorkingDir:          c.Ws.GetCWD(),
		AIStartupPath:         c.Ws.GetGitRoot(),
		AutoCompactionEnabled: autoCompact,
		MessageCount:          len(c.Ag.GetMessages()),
		PendingMessageCount:   c.Ag.GetPendingFollowUps(),
		Compaction:            compactionState,
	}, nil
}

func (c *RPCCore) handleGetMessages(args string) (any, error) {
	slog.Info("Received get_messages")
	messages := c.Ag.GetMessages()
	result := make([]any, len(messages))
	for i, msg := range messages {
		result[i] = msg
	}
	return map[string]any{"messages": result}, nil
}

func (c *RPCCore) handleCompact(args string) (any, error) {
	slog.Info("Received compact")
	beforeCount := len(c.Ag.GetMessages())

	estimatedTokens := c.Compactor.EstimateContextTokensOld(c.Ag.GetMessages())
	keepTokens := c.Compactor.KeepRecentTokens()

	if !c.Sess.CanCompact(c.Compactor) {
		if estimatedTokens < keepTokens {
			return nil, fmt.Errorf("all %d messages (%d tokens) fit within keep-recent budget (%d tokens); no compaction needed",
				beforeCount, estimatedTokens, keepTokens)
		}
		return nil, fmt.Errorf("no messages available for compaction (all within retention window)")
	}

	compactionInfo := agent.CompactionInfo{
		Auto:    false,
		Before:  beforeCount,
		Trigger: "manual_command",
	}
	c.StateMu.Lock()
	c.IsCompacting = true
	c.StateMu.Unlock()
	c.Server.EmitEvent(agent.NewCompactionStartEvent(compactionInfo))
	defer func() {
		c.StateMu.Lock()
		c.IsCompacting = false
		c.StateMu.Unlock()
		c.Server.EmitEvent(agent.NewCompactionEndEvent(compactionInfo))
	}()

	var response *rpc.CompactResult
	err := runDetachedTraceSpan(
		"compaction",
		traceevent.CategoryEvent,
		[]traceevent.Field{{Key: "source", Value: "manual"}},
		func(_ context.Context, span *traceevent.Span) error {
			span.AddField("before_messages", beforeCount)

			result, err := c.Sess.Compact(c.Compactor)
			if err != nil {
				slog.Info("Compact failed:", "value", err)
				return err
			}

			c.Ag.GetContext().RecentMessages = c.Sess.GetMessages()

			afterCount := len(c.Ag.GetMessages())
			span.AddField("after_messages", afterCount)
			span.AddField("tokens_before", result.TokensBefore)
			span.AddField("tokens_after", result.TokensAfter)
			compactionInfo.After = afterCount

			slog.Info("Compact successful", "before", beforeCount, "after", afterCount)
			response = &rpc.CompactResult{
				FirstKeptEntryID: result.FirstKeptEntryID,
				TokensBefore:     result.TokensBefore,
				TokensAfter:      result.TokensAfter,
			}

			if c.CheckpointMgr != nil && c.CheckpointMgr.ShouldCheckpoint() {
				agentCtx := c.Ag.GetContext()
				slog.Info("[Loop] Creating checkpoint after manual compact", "trigger", "manual_command", "turn", agentCtx.AgentState.TotalTurns)
				checkpointTurn, err := c.CheckpointMgr.CreateSnapshot(agentCtx, agentCtx.LLMContext, agentCtx.AgentState.TotalTurns)
				if err != nil {
					slog.Warn("[Loop] Failed to create checkpoint after manual compact", "error", err, "turn", agentCtx.AgentState.TotalTurns)
				} else {
					slog.Info("[Loop] Checkpoint created after manual compact", "trigger", "manual_command", "checkpoint_turn", checkpointTurn)
				}
			}
			return nil
		},
	)
	if err != nil {
		compactionInfo.Error = err.Error()
		return nil, err
	}
	return response, nil
}

func (c *RPCCore) handleGetAvailableModels(args string) (any, error) {
	slog.Info("Received get_available_models")
	specs, modelsPath, err := loadModelSpecs(c.Cfg)
	if err != nil {
		return nil, fmt.Errorf("load models from %s: %w", modelsPath, err)
	}

	specs = filterModelSpecsWithKeys(specs)
	if len(specs) == 0 {
		authPath, _ := config.GetDefaultAuthPath()
		return nil, fmt.Errorf("no models available (missing API keys?). Set provider keys or update %s", authPath)
	}

	models := make([]rpc.ModelInfo, 0, len(specs))
	for _, spec := range specs {
		models = append(models, modelInfoFromSpec(spec))
	}
	return map[string]any{"models": models}, nil
}

func (c *RPCCore) handleSetModel(args string) (any, error) {
	var provider, modelID string
	var jsonData struct {
		Provider string `json:"provider"`
		ModelID  string `json:"modelId"`
	}
	if parseJSONArgs(args, &jsonData) {
		provider, modelID = jsonData.Provider, jsonData.ModelID
	} else {
		parts := strings.SplitN(args, " ", 2)
		provider = strings.TrimSpace(parts[0])
		if len(parts) > 1 {
			modelID = strings.TrimSpace(parts[1])
		}
	}
	slog.Info("Received set_model", "provider", provider, "modelId", modelID)
	if strings.TrimSpace(provider) == "" || strings.TrimSpace(modelID) == "" {
		return nil, fmt.Errorf("provider and modelId are required")
	}

	specs, modelsPath, err := loadModelSpecs(c.Cfg)
	if err != nil {
		return nil, fmt.Errorf("load models from %s: %w", modelsPath, err)
	}
	filtered := filterModelSpecsWithKeys(specs)
	spec, ok := findModelSpec(filtered, provider, modelID)
	if !ok {
		if _, exists := findModelSpec(specs, provider, modelID); exists {
			authPath, _ := config.GetDefaultAuthPath()
			envVar := strings.ToUpper(strings.TrimSpace(provider)) + "_API_KEY"
			return nil, fmt.Errorf("no API key for %q (set %s or update %s)", provider, envVar, authPath)
		}
		return nil, fmt.Errorf("model not found: %s/%s (edit %s)", provider, modelID, modelsPath)
	}
	if strings.TrimSpace(spec.BaseURL) == "" || strings.TrimSpace(spec.API) == "" {
		return nil, fmt.Errorf("model %s/%s missing baseUrl or api in %s", spec.Provider, spec.ID, modelsPath)
	}

	newAPIKey, err := config.ResolveAPIKey(spec.Provider)
	if err != nil {
		return nil, err
	}

	c.Model = llm.Model{
		ID:            spec.ID,
		Provider:      spec.Provider,
		BaseURL:       spec.BaseURL,
		API:           spec.API,
		ContextWindow: spec.ContextWindow,
		MaxTokens:     spec.MaxTokens,
	}
	c.APIKey = newAPIKey

	c.Cfg.Model.ID = spec.ID
	c.Cfg.Model.Provider = spec.Provider
	c.Cfg.Model.BaseURL = spec.BaseURL
	c.Cfg.Model.API = spec.API
	c.Cfg.Model.MaxTokens = spec.MaxTokens

	c.Ag.SetModel(c.Model)
	c.Ag.SetAPIKey(c.APIKey)

	c.Compactor = compact.NewCompactor(c.CompactorConfig, c.Model, c.APIKey, c.SystemPrompt, spec.ContextWindow)
	c.SessionComp.Update(c.Sess, c.Compactor)
	c.Ag.SetCompactor(c.SessionComp)
	c.Ag.SetContextWindow(spec.ContextWindow)

	if err := config.SaveConfig(c.Cfg, c.ConfigPath); err != nil {
		slog.Info("Failed to save config:", "value", err)
	}

	info := modelInfoFromSpec(spec)
	c.StateMu.Lock()
	c.CurrentModelInfo = info
	c.CurrentContextWindow = spec.ContextWindow
	c.StateMu.Unlock()
	return &info, nil
}

func (c *RPCCore) handleCycleModel(args string) (any, error) {
	slog.Info("Received cycle_model")
	specs, modelsPath, err := loadModelSpecs(c.Cfg)
	if err != nil {
		return nil, fmt.Errorf("load models from %s: %w", modelsPath, err)
	}
	filtered := filterModelSpecsWithKeys(specs)
	if len(filtered) == 0 {
		authPath, _ := config.GetDefaultAuthPath()
		return nil, fmt.Errorf("no models available (missing API keys?). Set provider keys or update %s", authPath)
	}
	if len(filtered) == 1 {
		return nil, nil
	}

	index := -1
	for i, spec := range filtered {
		if spec.Provider == c.Model.Provider && spec.ID == c.Model.ID {
			index = i
			break
		}
	}
	next := filtered[0]
	if index >= 0 {
		next = filtered[(index+1)%len(filtered)]
	}

	newAPIKey, err := config.ResolveAPIKey(next.Provider)
	if err != nil {
		return nil, err
	}

	c.Model = llm.Model{
		ID:            next.ID,
		Provider:      next.Provider,
		BaseURL:       next.BaseURL,
		API:           next.API,
		ContextWindow: next.ContextWindow,
		MaxTokens:     next.MaxTokens,
	}
	c.APIKey = newAPIKey

	c.Cfg.Model.ID = next.ID
	c.Cfg.Model.Provider = next.Provider
	c.Cfg.Model.BaseURL = next.BaseURL
	c.Cfg.Model.API = next.API
	c.Cfg.Model.MaxTokens = next.MaxTokens

	c.Ag.SetModel(c.Model)
	c.Ag.SetAPIKey(c.APIKey)

	c.Compactor = compact.NewCompactor(c.CompactorConfig, c.Model, c.APIKey, c.SystemPrompt, next.ContextWindow)
	c.SessionComp.Update(c.Sess, c.Compactor)
	c.Ag.SetCompactor(c.SessionComp)
	c.Ag.SetContextWindow(next.ContextWindow)

	if err := config.SaveConfig(c.Cfg, c.ConfigPath); err != nil {
		slog.Info("Failed to save config:", "value", err)
	}

	info := modelInfoFromSpec(next)
	c.StateMu.Lock()
	c.CurrentModelInfo = info
	c.CurrentContextWindow = next.ContextWindow
	c.StateMu.Unlock()

	return &rpc.CycleModelResult{
		Model:         info,
		ThinkingLevel: c.CurrentThinkingLevel,
		IsScoped:      false,
	}, nil
}

func (c *RPCCore) handleGetSessionStats(args string) (any, error) {
	slog.Info("Received get_session_stats")
	messages := c.Ag.GetMessages()
	userCount, assistantCount, toolCalls, toolResults, tokens, cost := collectSessionUsage(messages)

	agentCtx := c.Ag.GetContext()
	currentSystemPrompt := c.buildSystemPrompt()
	tokens.SystemPromptTokens = len(currentSystemPrompt) / 4
	tokens.SystemToolsTokens = agentCtx.EstimateToolsTokens()
	activeWindowTokens := agent.EstimateConversationTokens(messages)
	tokens.ActiveWindowTokens = activeWindowTokens + tokens.SystemPromptTokens + tokens.SystemToolsTokens

	c.StateMu.Lock()
	currentSessionID := c.SessionID
	c.StateMu.Unlock()
	var tokenRate *rpc.TokenRateStats
	if metrics := c.Ag.GetMetrics(); metrics != nil {
		llmMetrics := metrics.GetLLMMetrics()
		tokenRate = &rpc.TokenRateStats{
			ActiveInputPerSec:   llmMetrics.ActiveInputTokensPerSec,
			ActiveOutputPerSec:  llmMetrics.ActiveOutputTokensPerSec,
			ActiveTotalPerSec:   llmMetrics.ActiveTotalTokensPerSec,
			WallInputPerSec:     llmMetrics.WallInputTokensPerSec,
			WallOutputPerSec:    llmMetrics.WallOutputTokensPerSec,
			WallTotalPerSec:     llmMetrics.WallTotalTokensPerSec,
			LastInputPerSec:     llmMetrics.LastInputTokensPerSec,
			LastOutputPerSec:    llmMetrics.LastOutputTokensPerSec,
			LastTotalPerSec:     llmMetrics.LastTotalTokensPerSec,
			RecentWindowSeconds: llmMetrics.RecentWindowSeconds,
			RecentInputPerSec:   llmMetrics.RecentInputTokensPerSec,
			RecentOutputPerSec:  llmMetrics.RecentOutputTokensPerSec,
			RecentTotalPerSec:   llmMetrics.RecentTotalTokensPerSec,
		}
	}
	return &rpc.SessionStats{
		SessionFile:       c.Sess.GetPath(),
		SessionID:         currentSessionID,
		UserMessages:      userCount,
		AssistantMessages: assistantCount,
		ToolCalls:         toolCalls,
		ToolResults:       toolResults,
		TotalMessages:     len(messages),
		CompactionCount:   c.Sess.GetCompactionCount(),
		Tokens:            tokens,
		TokenRate:         tokenRate,
		Cost:              cost,
		Workspace:         c.Ws.GetGitRoot(),
		CurrentWorkdir:    c.Ws.GetCWD(),
	}, nil
}

func (c *RPCCore) handleSetAutoRetry(args string) (any, error) {
	enabled := strings.TrimSpace(strings.ToLower(args))
	var jsonData struct {
		Enabled bool `json:"enabled"`
	}
	if parseJSONArgs(args, &jsonData) {
		c.Ag.SetAutoRetry(jsonData.Enabled)
		slog.Info("Received set_auto_retry", "enabled", jsonData.Enabled)
		return nil, nil
	}
	slog.Info("Received set_auto_retry", "enabled", enabled)
	c.Ag.SetAutoRetry(enabled == "true" || enabled == "1")
	return nil, nil
}

func (c *RPCCore) handleAbortRetry(args string) (any, error) {
	slog.Info("Received abort_retry")
	c.Ag.Abort()
	return nil, nil
}

func (c *RPCCore) handleExportHTML(args string) (any, error) {
	slog.Info("Received export_html", "outputPath", args)
	return "", fmt.Errorf("export_html is not supported")
}

func (c *RPCCore) handleSetAutoCompaction(args string) (any, error) {
	enabled := strings.TrimSpace(strings.ToLower(args))
	var jsonData struct {
		Enabled bool `json:"enabled"`
	}
	if parseJSONArgs(args, &jsonData) {
		c.CompactorConfig.AutoCompact = jsonData.Enabled
		c.StateMu.Lock()
		c.AutoCompactionEnabled = jsonData.Enabled
		c.StateMu.Unlock()
		slog.Info("Received set_auto_compaction: enabled=", "value", jsonData.Enabled)
		return nil, nil
	}
	val := enabled == "true" || enabled == "1"
	c.CompactorConfig.AutoCompact = val
	c.StateMu.Lock()
	c.AutoCompactionEnabled = val
	c.StateMu.Unlock()
	slog.Info("Received set_auto_compaction: enabled=", "value", val)
	return nil, nil
}

func (c *RPCCore) handleSetToolCallCutoff(args string) (any, error) {
	var cutoff int
	var jsonData struct {
		Cutoff int `json:"cutoff"`
	}
	if parseJSONArgs(args, &jsonData) {
		cutoff = jsonData.Cutoff
	} else {
		var err error
		cutoff, err = strconv.Atoi(strings.TrimSpace(args))
		if err != nil {
			return nil, fmt.Errorf("invalid cutoff value: %w", err)
		}
	}
	slog.Info("Received set_tool_call_cutoff", "cutoff", cutoff)
	if cutoff < 0 {
		return nil, fmt.Errorf("cutoff must be >= 0")
	}
	c.CompactorConfig.ToolCallCutoff = cutoff
	c.Ag.SetToolCallCutoff(cutoff)
	if err := config.SaveConfig(c.Cfg, c.ConfigPath); err != nil {
		slog.Info("Failed to save config:", "value", err)
	}
	return map[string]any{"cutoff": cutoff}, nil
}

func (c *RPCCore) handleSetToolSummaryStrategy(args string, validStrategies map[string]bool) (any, error) {
	var jsonData struct {
		Strategy string `json:"strategy"`
	}
	strategy := strings.ToLower(strings.TrimSpace(args))
	if parseJSONArgs(args, &jsonData) {
		strategy = strings.ToLower(jsonData.Strategy)
	}
	slog.Info("Received set_tool_summary_strategy", "strategy", strategy)
	if !validStrategies[strategy] {
		return nil, fmt.Errorf("invalid tool summary strategy")
	}
	c.CompactorConfig.ToolSummaryStrategy = strategy
	if err := config.SaveConfig(c.Cfg, c.ConfigPath); err != nil {
		slog.Info("Failed to save config:", "value", err)
	}
	return map[string]any{"strategy": strategy}, nil
}

func (c *RPCCore) handleSetToolSummaryAutomation(args string, validModes map[string]bool) (any, error) {
	var jsonData struct {
		Mode string `json:"mode"`
	}
	mode := strings.ToLower(strings.TrimSpace(args))
	if parseJSONArgs(args, &jsonData) {
		mode = strings.ToLower(jsonData.Mode)
	}
	slog.Info("Received set_tool_summary_automation", "mode", mode)
	if !validModes[mode] {
		return nil, fmt.Errorf("invalid tool summary automation mode")
	}
	c.CompactorConfig.ToolSummaryAutomation = mode
	if err := config.SaveConfig(c.Cfg, c.ConfigPath); err != nil {
		slog.Info("Failed to save config:", "value", err)
	}
	return map[string]any{"mode": mode}, nil
}

func (c *RPCCore) handleSetTraceEvents(args string) (any, error) {
	events := strings.Fields(args)
	var jsonData struct {
		Events []string `json:"events"`
	}
	if parseJSONArgs(args, &jsonData) && len(jsonData.Events) > 0 {
		events = jsonData.Events
	}
	slog.Info("Received set_trace_events", "events", events)

	if len(events) == 0 {
		return map[string]any{"events": traceevent.ResetToDefaultEvents()}, nil
	}

	normalized := make([]string, 0, len(events))
	for _, e := range events {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" {
			continue
		}
		normalized = append(normalized, e)
	}
	if len(normalized) == 0 {
		return map[string]any{"events": traceevent.ResetToDefaultEvents()}, nil
	}

	applyExpanded := func(expanded []string, replace bool) []string {
		if replace {
			traceevent.DisableAllEvents()
		}
		for _, eventName := range expanded {
			traceevent.EnableEvent(eventName)
		}
		return traceevent.GetEnabledEvents()
	}

	op := normalized[0]
	switch op {
	case "on":
		return map[string]any{"events": traceevent.ResetToDefaultEvents()}, nil
	case "all":
		expanded, _ := traceevent.ExpandEventSelectors([]string{"all"})
		return map[string]any{"events": applyExpanded(expanded, true)}, nil
	case "default":
		return map[string]any{"events": traceevent.ResetToDefaultEvents()}, nil
	case "off", "none":
		traceevent.DisableAllEvents()
		return map[string]any{"events": []string{}}, nil
	case "enable":
		if len(normalized) == 1 {
			return nil, fmt.Errorf("trace-events enable requires at least one selector")
		}
		expanded, unknown := traceevent.ExpandEventSelectors(normalized[1:])
		if len(unknown) > 0 {
			return nil, fmt.Errorf("unknown trace events/selectors: %s", strings.Join(unknown, ", "))
		}
		return map[string]any{"events": applyExpanded(expanded, false)}, nil
	case "disable":
		if len(normalized) == 1 {
			return nil, fmt.Errorf("trace-events disable requires at least one selector")
		}
		expanded, unknown := traceevent.ExpandEventSelectors(normalized[1:])
		if len(unknown) > 0 {
			return nil, fmt.Errorf("unknown trace events/selectors: %s", strings.Join(unknown, ", "))
		}
		for _, eventName := range expanded {
			traceevent.DisableEvent(eventName)
		}
		return map[string]any{"events": traceevent.GetEnabledEvents()}, nil
	default:
		expanded, unknown := traceevent.ExpandEventSelectors(normalized)
		if len(unknown) > 0 {
			return nil, fmt.Errorf("unknown trace events/selectors: %s", strings.Join(unknown, ", "))
		}
		return map[string]any{"events": applyExpanded(expanded, true)}, nil
	}
}

func (c *RPCCore) handleGetTraceEvents(args string) (any, error) {
	slog.Info("Received get_trace_events")
	return map[string]any{"events": traceevent.GetEnabledEvents()}, nil
}

func (c *RPCCore) handleGetWorkflowStatus(args string) (any, error) {
	slog.Info("Received get_workflow_status")
	status, err := getWorkflowStatus(c.Ws.GetCWD())
	if err != nil {
		return nil, err
	}
	if status == nil {
		return nil, nil
	}
	return status, nil
}

func (c *RPCCore) handleSetSteeringMode(args string, validModes map[string]bool) (any, error) {
	var jsonData struct {
		Mode string `json:"mode"`
	}
	mode := strings.ToLower(strings.TrimSpace(args))
	if parseJSONArgs(args, &jsonData) {
		mode = strings.ToLower(jsonData.Mode)
	}
	slog.Info("Received set_steering_mode", "mode", mode)
	if !validModes[mode] {
		return nil, fmt.Errorf("invalid steering mode")
	}
	c.StateMu.Lock()
	c.SteeringMode = mode
	c.StateMu.Unlock()
	return nil, nil
}

func (c *RPCCore) handleSetFollowUpMode(args string, validModes map[string]bool) (any, error) {
	var jsonData struct {
		Mode string `json:"mode"`
	}
	mode := strings.ToLower(strings.TrimSpace(args))
	if parseJSONArgs(args, &jsonData) {
		mode = strings.ToLower(jsonData.Mode)
	}
	slog.Info("Received set_follow_up_mode", "mode", mode)
	if !validModes[mode] {
		return nil, fmt.Errorf("invalid follow-up mode")
	}
	c.StateMu.Lock()
	c.FollowUpMode = mode
	c.StateMu.Unlock()
	return nil, nil
}

func (c *RPCCore) handleSetThinkingLevel(args string, validLevels map[string]bool) (any, error) {
	level := strings.ToLower(strings.TrimSpace(args))
	var jsonData struct {
		Level string `json:"level"`
	}
	if parseJSONArgs(args, &jsonData) {
		level = strings.ToLower(strings.TrimSpace(jsonData.Level))
	}
	if !validLevels[level] {
		return nil, fmt.Errorf("invalid thinking level")
	}
	c.StateMu.Lock()
	c.CurrentThinkingLevel = level
	c.StateMu.Unlock()
	c.Ag.SetThinkingLevel(level)
	return map[string]any{"level": level}, nil
}

func (c *RPCCore) handleCycleThinkingLevel(cycle []string) (any, error) {
	c.StateMu.Lock()
	current := c.CurrentThinkingLevel
	c.StateMu.Unlock()

	next := "medium"
	for i, level := range cycle {
		if level == current {
			next = cycle[(i+1)%len(cycle)]
			break
		}
	}

	c.StateMu.Lock()
	c.CurrentThinkingLevel = next
	c.StateMu.Unlock()
	c.Ag.SetThinkingLevel(next)
	return map[string]any{"level": next}, nil
}

func (c *RPCCore) handleGetLastAssistantText(args string) (any, error) {
	slog.Info("Received get_last_assistant_text")
	messages := c.Ag.GetMessages()
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" {
			return map[string]any{"text": messages[i].ExtractText()}, nil
		}
	}
	return "", nil
}

func (c *RPCCore) handleGetForkMessages(args string) (any, error) {
	slog.Info("Received get_fork_messages")
	forkMessages := c.Sess.GetUserMessagesForForking()
	result := make([]rpc.ForkMessage, 0, len(forkMessages))
	for _, msg := range forkMessages {
		result = append(result, rpc.ForkMessage{
			EntryID: msg.EntryID,
			Text:    msg.Text,
		})
	}
	return map[string]any{"messages": result}, nil
}

func (c *RPCCore) handleGetTree(args string) (any, error) {
	slog.Info("Received get_tree")
	entries := c.Sess.GetEntries()
	tree := buildTreeEntries(entries, c.Sess.GetLeafID())
	return map[string]any{"entries": tree}, nil
}

func (c *RPCCore) handleModel(args string) (any, error) {
	arg := strings.TrimSpace(args)
	if arg == "" {
		specs, modelsPath, err := loadModelSpecs(c.Cfg)
		if err != nil {
			return nil, fmt.Errorf("load models from %s: %w", modelsPath, err)
		}
		specs = filterModelSpecsWithKeys(specs)
		if len(specs) == 0 {
			authPath, _ := config.GetDefaultAuthPath()
			return nil, fmt.Errorf("no models available (missing API keys?). Set provider keys or update %s", authPath)
		}

		models := make([]rpc.ModelInfo, 0, len(specs))
		currentIndex := -1
		for i, spec := range specs {
			models = append(models, modelInfoFromSpec(spec))
			if spec.Provider == c.Model.Provider && spec.ID == c.Model.ID {
				currentIndex = i
			}
		}

		return map[string]any{
			"models":       models,
			"currentIndex": currentIndex,
			"current": map[string]any{
				"provider": c.Model.Provider,
				"id":       c.Model.ID,
			},
		}, nil
	}

	if idx, err := strconv.Atoi(arg); err == nil {
		specs, modelsPath, err := loadModelSpecs(c.Cfg)
		if err != nil {
			return nil, fmt.Errorf("load models from %s: %w", modelsPath, err)
		}
		specs = filterModelSpecsWithKeys(specs)
		if len(specs) == 0 {
			authPath, _ := config.GetDefaultAuthPath()
			return nil, fmt.Errorf("no models available (missing API keys?). Set provider keys or update %s", authPath)
		}
		if idx < 0 || idx >= len(specs) {
			return nil, fmt.Errorf("model index out of range")
		}
		arg = fmt.Sprintf("%s %s", specs[idx].Provider, specs[idx].ID)
	} else if strings.Contains(arg, "/") {
		parts := strings.SplitN(arg, "/", 2)
		provider := strings.TrimSpace(parts[0])
		modelID := strings.TrimSpace(parts[1])
		if provider == "" || modelID == "" {
			return nil, fmt.Errorf("invalid model selector: %s", arg)
		}
		arg = fmt.Sprintf("%s %s", provider, modelID)
	}

	h, ok := c.Server.GetSlashHandler("set_model")
	if !ok {
		return nil, fmt.Errorf("unknown command: set_model")
	}
	return h(arg)
}

func (c *RPCCore) handleResume(args string) (any, error) {
	arg := strings.TrimSpace(args)
	if arg == "" {
		h, ok := c.Server.GetSlashHandler("list_sessions")
		if !ok {
			return nil, fmt.Errorf("unknown command: list_sessions")
		}
		return h("")
	}
	h, ok := c.Server.GetSlashHandler("switch_session")
	if !ok {
		return nil, fmt.Errorf("unknown command: switch_session")
	}
	return h(arg)
}

func (c *RPCCore) handleContext(args string) (any, error) {
	stateH, _ := c.Server.GetSlashHandler("get_state")
	statsH, _ := c.Server.GetSlashHandler("get_session_stats")
	modelsH, _ := c.Server.GetSlashHandler("get_available_models")

	stateResult, err := stateH("")
	if err != nil {
		return nil, err
	}
	statsResult, _ := statsH("")
	modelsResult, _ := modelsH("")

	return map[string]any{
		"state":  stateResult,
		"stats":  statsResult,
		"models": modelsResult,
	}, nil
}

func (c *RPCCore) handleToggle(args string) (any, error) {
	kind := strings.TrimSpace(args)
	switch kind {
	case "thinking":
		c.ShowThinking = !c.ShowThinking
		return map[string]any{"setting": "thinking", "value": c.ShowThinking}, nil
	case "prefix":
		c.ShowPrefix = !c.ShowPrefix
		return map[string]any{"setting": "prefix", "value": c.ShowPrefix}, nil
	default:
		return nil, fmt.Errorf("usage: /toggle <thinking|prefix>")
	}
}

func (c *RPCCore) handleSetThinkingDisplay(args string) (any, error) {
	switch strings.TrimSpace(args) {
	case "on":
		c.ShowThinking = true
	case "off":
		c.ShowThinking = false
	case "toggle", "":
		c.ShowThinking = !c.ShowThinking
	default:
		return nil, fmt.Errorf("usage: /set_thinking_display <on|off|toggle>")
	}
	return map[string]any{"setting": "thinking", "value": c.ShowThinking}, nil
}

func (c *RPCCore) handleSetToolsDisplay(args string) (any, error) {
	switch strings.TrimSpace(args) {
	case "on":
		c.ShowTools = true
	case "off":
		c.ShowTools = false
	case "toggle", "":
		c.ShowTools = !c.ShowTools
	default:
		return nil, fmt.Errorf("usage: /set_tools_display <on|off|toggle>")
	}
	return map[string]any{"setting": "tools", "value": c.ShowTools}, nil
}

func (c *RPCCore) handleSetPrefixDisplay(args string) (any, error) {
	switch strings.TrimSpace(args) {
	case "on":
		c.ShowPrefix = true
	case "off":
		c.ShowPrefix = false
	case "toggle", "":
		c.ShowPrefix = !c.ShowPrefix
	default:
		return nil, fmt.Errorf("usage: /set_prefix_display <on|off|toggle>")
	}
	return map[string]any{"setting": "prefix", "value": c.ShowPrefix}, nil
}

func (c *RPCCore) handleSetBusyMode(args string) (any, error) {
	mode := strings.TrimSpace(args)
	switch mode {
	case "steer", "follow-up", "reject":
		c.BusyMode = mode
		return map[string]any{"setting": "busy-mode", "value": c.BusyMode}, nil
	default:
		return nil, fmt.Errorf("usage: /set_busy_mode <steer|follow-up|reject>")
	}
}

func (c *RPCCore) handleShow(args string) (any, error) {
	subCmd := strings.TrimSpace(args)
	switch subCmd {
	case "settings", "":
		compaction := buildCompactionState(c.CompactorConfig, c.Compactor)

		model := c.CurrentModelInfo.ID
		if c.CurrentModelInfo.Provider != "" {
			model = c.CurrentModelInfo.Provider + "/" + c.CurrentModelInfo.ID
		}

		compactionContext := "unknown"
		compactionReserve := "unknown"
		compactionLimit := "unknown"
		compactionMaxMessages := "disabled"
		compactionMaxTokens := "disabled"
		compactionKeepRecent := "unknown"
		compactionKeepRecentTokens := "unknown"
		if compaction != nil {
			compactionContext = formatIntOrUnknown(compaction.ContextWindow)
			compactionReserve = formatIntOrUnknown(compaction.ReserveTokens)
			compactionLimit = formatTokenLimit(compaction)
			compactionMaxMessages = formatLimit(compaction.MaxMessages)
			compactionMaxTokens = formatLimit(compaction.MaxTokens)
			compactionKeepRecent = formatIntOrUnknown(compaction.KeepRecent)
			compactionKeepRecentTokens = formatIntOrUnknown(compaction.KeepRecentTokens)
		}

		autoCompStr := "off"
		if c.AutoCompactionEnabled {
			autoCompStr = "on"
		}

		showThinkingStr := "off"
		if c.ShowThinking {
			showThinkingStr = "on"
		}
		showToolsStr := "off"
		if c.ShowTools {
			showToolsStr = "on"
		}
		showPrefixStr := "off"
		if c.ShowPrefix {
			showPrefixStr = "on"
		}

		return map[string]any{
			"type": "settings",
			"data": map[string]any{
				"model":                         model,
				"show-thinking":                 showThinkingStr,
				"tools":                         showToolsStr,
				"prefix":                        showPrefixStr,
				"thinking-level":                c.CurrentThinkingLevel,
				"busy-mode":                     c.BusyMode,
				"auto-compaction":               autoCompStr,
				"compaction-context-window":     compactionContext,
				"compaction-reserve-tokens":     compactionReserve,
				"compaction-token-limit":        compactionLimit,
				"compaction-max-messages":       compactionMaxMessages,
				"compaction-max-tokens":         compactionMaxTokens,
				"compaction-keep-recent":        compactionKeepRecent,
				"compaction-keep-recent-tokens": compactionKeepRecentTokens,
			},
		}, nil
	case "pipeline":
		return map[string]any{"message": "pipeline info not yet available"}, nil
	default:
		return nil, fmt.Errorf("usage: /show settings|pipeline")
	}
}

func (c *RPCCore) handleQuit(args string) (any, error) {
	slog.Info("Received quit command, exiting application")
	os.Exit(0)
	return nil, nil // unreachable, but Go requires return
}

func (c *RPCCore) handleSlashAbort(args string) (any, error) {
	slog.Info("Received abort command")
	c.StateMu.Lock()
	streaming := c.IsStreaming
	c.StateMu.Unlock()

	if !streaming {
		return nil, fmt.Errorf("agent is not streaming")
	}

	c.Ag.Abort()
	return map[string]any{"status": "aborting"}, nil
}

func (c *RPCCore) handleSlashFollowUp(args string) (any, error) {
	message := strings.TrimSpace(args)
	if message == "" {
		return nil, fmt.Errorf("usage: /follow-up <message>")
	}

	slog.Info("Received follow-up command")
	c.StateMu.Lock()
	streaming := c.IsStreaming
	c.StateMu.Unlock()

	if !streaming {
		return nil, fmt.Errorf("agent is not busy")
	}

	if c.FollowUpMode != "one-at-a-time" && c.FollowUpMode != "queue" {
		return nil, fmt.Errorf("follow-up mode is '%s', not enabled", c.FollowUpMode)
	}

	if len(c.FollowUpQueue) > 0 && c.FollowUpMode == "one-at-a-time" {
		return nil, fmt.Errorf("follow-up queue already has a pending message")
	}

	expandedMessage := c.ExpandSkillCommands(message)
	c.FollowUpQueue = append(c.FollowUpQueue, expandedMessage)
	return map[string]any{"status": "queued", "message": expandedMessage}, nil
}

func (c *RPCCore) handleSlashSteer(args string) (any, error) {
	message := strings.TrimSpace(args)
	if message == "" {
		return nil, fmt.Errorf("usage: /steer <message>")
	}

	slog.Info("Received steer command")
	c.StateMu.Lock()
	streaming := c.IsStreaming
	busyBehavior := c.BusyMode
	c.StateMu.Unlock()

	if !streaming {
		return nil, fmt.Errorf("agent is not streaming")
	}

	if busyBehavior == "reject" {
		return nil, fmt.Errorf("agent is busy and busy mode is set to reject")
	}

	expandedMessage := c.ExpandSkillCommands(message)
	c.Ag.Steer(expandedMessage)
	return map[string]any{"status": "steering", "message": expandedMessage}, nil
}
