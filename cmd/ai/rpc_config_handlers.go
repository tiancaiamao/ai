package main

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/tiancaiamao/ai/pkg/agent"
	"github.com/tiancaiamao/ai/pkg/compact"
	"github.com/tiancaiamao/ai/pkg/config"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/rpc"
	traceevent "github.com/tiancaiamao/ai/pkg/traceevent"
)

// --- Configuration handlers ---

func (app *rpcApp) collectSessionUsageFromAgent() (int, int, int, int, rpc.SessionTokenStats, float64) {
	return collectSessionUsage(app.ag.GetMessages())
}

func (app *rpcApp) getSessionStats() (*rpc.SessionStats, error) {
	userCount, assistantCount, toolCalls, toolResults, tokens, cost := app.collectSessionUsageFromAgent()

	actx := app.ag.GetContext()
	tokens.SystemPromptTokens = len(app.systemPrompt) / 4
	tokens.SystemToolsTokens = actx.EstimateToolsTokens()
	activeWindowTokens := agent.EstimateConversationTokens(app.ag.GetMessages())
	tokens.ActiveWindowTokens = activeWindowTokens + tokens.SystemPromptTokens + tokens.SystemToolsTokens

	var tokenRate *rpc.TokenRateStats
	if metrics := app.ag.GetMetrics(); metrics != nil {
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
		SessionFile:       app.sess.GetPath(),
		SessionID:         app.sessionID,
		UserMessages:      userCount,
		AssistantMessages: assistantCount,
		ToolCalls:         toolCalls,
		ToolResults:       toolResults,
		TotalMessages:     len(app.ag.GetMessages()),
		CompactionCount:   app.sess.GetCompactionCount(),
		Tokens:            tokens,
		TokenRate:         tokenRate,
		Cost:              cost,
		Workspace:         app.ws.GetGitRoot(),
		CurrentWorkdir:    app.ws.GetCWD(),
	}, nil
}

func (app *rpcApp) getCurrentAILogPath() string {
	aiLogPath := app.traceOutputPath
	if handler := traceevent.GetHandler(); handler != nil {
		if fh, ok := handler.(*traceevent.FileHandler); ok {
			aiLogPath = fh.TraceFilePath("")
		}
	}
	return aiLogPath
}

func (app *rpcApp) handleModelSet(args string) (any, error) {
	var provider, modelID string
	var jsonData struct {
		Provider string `json:"provider"`
		ModelID  string `json:"modelId"`
	}
	if app.parseJSONArgs(args, &jsonData) {
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

	specs, modelsPath, err := loadModelSpecs(app.cfg)
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

	app.model = llm.Model{
		ID:            spec.ID,
		Provider:      spec.Provider,
		BaseURL:       spec.BaseURL,
		API:           spec.API,
		ContextWindow: spec.ContextWindow,
		MaxTokens:     spec.MaxTokens,
		Reasoning:     spec.Reasoning,
	}
	app.apiKey = newAPIKey

	app.cfg.Model.ID = spec.ID
	app.cfg.Model.Provider = spec.Provider
	app.cfg.Model.BaseURL = spec.BaseURL
	app.cfg.Model.API = spec.API
	app.cfg.Model.MaxTokens = spec.MaxTokens

	app.ag.SetModel(app.model)
	app.ag.SetAPIKey(app.apiKey)

	// Recreate compactor with new model
	app.compactor = compact.NewCompactor(app.compactorConfig, app.model, app.apiKey, app.systemPrompt, spec.ContextWindow)
	app.sessionComp.Update(app.compactor)
	app.ag.SetCompactor(app.sessionComp)
	app.ag.SetContextWindow(spec.ContextWindow)

	if err := config.SaveConfig(app.cfg, app.configPath); err != nil {
		slog.Info("Failed to save config:", "value", err)
	}

	info := modelInfoFromSpec(spec)
	app.stateMu.Lock()
	app.currentModelInfo = info
	app.currentContextWindow = spec.ContextWindow
	app.stateMu.Unlock()
	return &info, nil
}

func (app *rpcApp) handleModelList() (any, error) {
	specs, modelsPath, err := loadModelSpecs(app.cfg)
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
		if spec.Provider == app.model.Provider && spec.ID == app.model.ID {
			currentIndex = i
		}
	}

	return map[string]any{
		"models":       models,
		"currentIndex": currentIndex,
		"current": map[string]any{
			"provider": app.model.Provider,
			"id":       app.model.ID,
		},
	}, nil
}

func (app *rpcApp) handleSetAutoCompaction(value string) (any, error) {
	enabled := strings.TrimSpace(strings.ToLower(value))
	var jsonData struct {
		Enabled bool `json:"enabled"`
	}
	if app.parseJSONArgs(value, &jsonData) {
		app.compactorConfig.AutoCompact = jsonData.Enabled
		app.stateMu.Lock()
		app.autoCompactionEnabled = jsonData.Enabled
		app.stateMu.Unlock()
		slog.Info("set auto-compaction", "enabled", jsonData.Enabled)
		return map[string]any{"setting": "auto-compaction", "value": jsonData.Enabled}, nil
	}
	val := enabled == "true" || enabled == "1"
	app.compactorConfig.AutoCompact = val
	app.stateMu.Lock()
	app.autoCompactionEnabled = val
	app.stateMu.Unlock()
	slog.Info("set auto-compaction", "enabled", val)
	return map[string]any{"setting": "auto-compaction", "value": val}, nil
}

func (app *rpcApp) handleSetThinkingLevel(value string, validLevels map[string]bool) (any, error) {
	level := strings.ToLower(strings.TrimSpace(value))
	var jsonData struct {
		Level string `json:"level"`
	}
	if app.parseJSONArgs(value, &jsonData) {
		level = strings.ToLower(strings.TrimSpace(jsonData.Level))
	}
	if !validLevels[level] {
		return nil, fmt.Errorf("invalid thinking level; valid: off, minimal, low, medium, high, xhigh")
	}
	app.stateMu.Lock()
	app.currentThinkingLevel = level
	app.stateMu.Unlock()
	app.ag.SetThinkingLevel(level)
	return map[string]any{"setting": "thinking-level", "value": level}, nil
}

func (app *rpcApp) handleSetFollowUpMode(value string, validModes map[string]bool) (any, error) {
	mode := strings.ToLower(strings.TrimSpace(value))
	var jsonData struct {
		Mode string `json:"mode"`
	}
	if app.parseJSONArgs(value, &jsonData) {
		mode = strings.ToLower(jsonData.Mode)
	}
	if !validModes[mode] {
		return nil, fmt.Errorf("invalid follow-up mode; valid: all, immediate, one-at-a-time")
	}
	app.stateMu.Lock()
	app.followUpMode = mode
	app.stateMu.Unlock()
	return map[string]any{"setting": "follow-up-mode", "value": mode}, nil
}

func (app *rpcApp) handleSetSteeringMode(value string, validModes map[string]bool) (any, error) {
	mode := strings.ToLower(strings.TrimSpace(value))
	var jsonData struct {
		Mode string `json:"mode"`
	}
	if app.parseJSONArgs(value, &jsonData) {
		mode = strings.ToLower(jsonData.Mode)
	}
	if !validModes[mode] {
		return nil, fmt.Errorf("invalid steering mode; valid: all, immediate, one-at-a-time")
	}
	app.stateMu.Lock()
	app.steeringMode = mode
	app.stateMu.Unlock()
	return map[string]any{"setting": "steering-mode", "value": mode}, nil
}

func (app *rpcApp) handleSetToolCallCutoff(value string) (any, error) {
	var cutoff int
	var jsonData struct {
		Cutoff int `json:"cutoff"`
	}
	if app.parseJSONArgs(value, &jsonData) {
		cutoff = jsonData.Cutoff
	} else {
		var err error
		cutoff, err = strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return nil, fmt.Errorf("invalid cutoff value: %w", err)
		}
	}
	if cutoff < 0 {
		return nil, fmt.Errorf("cutoff must be >= 0")
	}
	app.compactorConfig.ToolCallCutoff = cutoff
	app.ag.SetToolCallCutoff(cutoff)
	if err := config.SaveConfig(app.cfg, app.configPath); err != nil {
		slog.Info("Failed to save config:", "value", err)
	}
	return map[string]any{"setting": "tool-call-cutoff", "value": cutoff}, nil
}

func (app *rpcApp) handleSetToolSummaryAutomation(value string, validModes map[string]bool) (any, error) {
	var jsonData struct {
		Mode string `json:"mode"`
	}
	mode := strings.ToLower(strings.TrimSpace(value))
	if app.parseJSONArgs(value, &jsonData) {
		mode = strings.ToLower(jsonData.Mode)
	}
	if !validModes[mode] {
		return nil, fmt.Errorf("invalid tool summary automation mode; valid: off, fallback, always")
	}
	app.compactorConfig.ToolSummaryAutomation = mode
	if err := config.SaveConfig(app.cfg, app.configPath); err != nil {
		slog.Info("Failed to save config:", "value", err)
	}
	return map[string]any{"setting": "tool-summary-automation", "value": mode}, nil
}

func (app *rpcApp) handleSetToolSummaryStrategy(value string, validStrategies map[string]bool) (any, error) {
	var jsonData struct {
		Strategy string `json:"strategy"`
	}
	strategy := strings.ToLower(strings.TrimSpace(value))
	if app.parseJSONArgs(value, &jsonData) {
		strategy = strings.ToLower(jsonData.Strategy)
	}
	if !validStrategies[strategy] {
		return nil, fmt.Errorf("invalid tool summary strategy; valid: llm, heuristic, off")
	}
	app.compactorConfig.ToolSummaryStrategy = strategy
	if err := config.SaveConfig(app.cfg, app.configPath); err != nil {
		slog.Info("Failed to save config:", "value", err)
	}
	return map[string]any{"setting": "tool-summary-strategy", "value": strategy}, nil
}

func (app *rpcApp) handleSetSessionName(value string) (any, error) {
	name := strings.TrimSpace(value)
	var jsonData struct {
		Name string `json:"name"`
	}
	if app.parseJSONArgs(value, &jsonData) {
		name = jsonData.Name
	}
	slog.Info("set session-name", "name", name)
	if name == "" {
		return nil, fmt.Errorf("session name cannot be empty")
	}
	if _, err := app.sess.AppendSessionInfo(name, ""); err != nil {
		return nil, err
	}
	if err := app.sessionMgr.UpdateSessionName(app.sessionID, name, ""); err != nil {
		slog.Info("Failed to update session metadata:", "value", err)
	}
	if err := app.sessionMgr.SaveCurrent(); err != nil {
		slog.Info("Failed to update session metadata:", "value", err)
	}
	app.stateMu.Lock()
	app.sessionName = name
	app.stateMu.Unlock()
	return map[string]any{"setting": "session-name", "value": name}, nil
}

func (app *rpcApp) handleSetTraceEvents(value string) (any, error) {
	if strings.TrimSpace(value) == "" {
		return map[string]any{"events": traceevent.GetEnabledEvents()}, nil
	}

	events := strings.Fields(value)
	var jsonData struct {
		Events []string `json:"events"`
	}
	if app.parseJSONArgs(value, &jsonData) && len(jsonData.Events) > 0 {
		events = jsonData.Events
	}
	slog.Info("set trace-events", "events", events)

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

func (app *rpcApp) handleShowSettings() (any, error) {
	compaction := buildCompactionState(app.compactorConfig, app.compactor)

	model := app.currentModelInfo.ID
	if app.currentModelInfo.Provider != "" {
		model = app.currentModelInfo.Provider + "/" + app.currentModelInfo.ID
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
	if app.autoCompactionEnabled {
		autoCompStr = "on"
	}

	showThinkingStr := "off"
	if app.showThinking {
		showThinkingStr = "on"
	}
	showToolsStr := "off"
	if app.showTools {
		showToolsStr = "on"
	}
	showPrefixStr := "off"
	if app.showPrefix {
		showPrefixStr = "on"
	}

	return map[string]any{
		"type": "settings",
		"data": map[string]any{
			"model":                         model,
			"show-thinking":                 showThinkingStr,
			"tools":                         showToolsStr,
			"prefix":                        showPrefixStr,
			"thinking-level":                app.currentThinkingLevel,
			"busy-mode":                     app.busyMode,
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
}

func (app *rpcApp) handleSetAutoRetry(value string) (any, error) {
	enabled := strings.TrimSpace(strings.ToLower(value))
	var jsonData struct {
		Enabled bool `json:"enabled"`
	}
	if app.parseJSONArgs(value, &jsonData) {
		app.ag.SetAutoRetry(jsonData.Enabled)
		slog.Info("set auto-retry", "enabled", jsonData.Enabled)
		return map[string]any{"setting": "auto-retry", "value": jsonData.Enabled}, nil
	}
	val := enabled == "true" || enabled == "1"
	app.ag.SetAutoRetry(val)
	slog.Info("set auto-retry", "enabled", val)
	return map[string]any{"setting": "auto-retry", "value": val}, nil
}

func (app *rpcApp) handleSet(args string, validToolSummaryStrategies, validToolSummaryAutomations, validSteeringModes, validFollowUpModes, validThinkingLevels map[string]bool) (any, error) {
	parts := strings.Fields(args)
	if len(parts) == 0 || parts[0] == "help" {
		return map[string]any{
			"usage": "/set <key> [value]",
			"settings": []string{
				"auto-retry <on|off>",
				"auto-compaction <on|off>",
				"busy-mode <steer|follow-up|reject>",
				"follow-up-mode <all|immediate|one-at-a-time>",
				"prefix-display <on|off|toggle>",
				"session-name <name>",
				"steering-mode <all|immediate|one-at-a-time>",
				"thinking-display <on|off|toggle>",
				"thinking-level <off|minimal|low|medium|high|xhigh>",
				"tool-call-cutoff <n>",
				"tool-summary-automation <off|fallback|always>",
				"tool-summary-strategy <llm|heuristic|off>",
				"tools-display <on|off|toggle>",
				"trace-events [on|off|all|enable <selectors>|disable <selectors>]",
			},
		}, nil
	}

	key := parts[0]
	value := ""
	if len(parts) > 1 {
		value = strings.TrimSpace(strings.TrimPrefix(args, key))
	}

	switch key {
	case "auto-retry":
		return app.handleSetAutoRetry(value)

	case "auto-compaction":
		return app.handleSetAutoCompaction(value)

	case "busy-mode":
		mode := strings.TrimSpace(value)
		switch mode {
		case "steer", "follow-up", "reject":
			app.busyMode = mode
			return map[string]any{"setting": "busy-mode", "value": app.busyMode}, nil
		default:
			return nil, fmt.Errorf("usage: /set busy-mode <steer|follow-up|reject>")
		}

	case "follow-up-mode":
		return app.handleSetFollowUpMode(value, validFollowUpModes)

	case "prefix-display":
		switch strings.TrimSpace(value) {
		case "on":
			app.showPrefix = true
		case "off":
			app.showPrefix = false
		case "toggle", "":
			app.showPrefix = !app.showPrefix
		default:
			return nil, fmt.Errorf("usage: /set prefix-display <on|off|toggle>")
		}
		return map[string]any{"setting": "prefix-display", "value": app.showPrefix}, nil

	case "steering-mode":
		return app.handleSetSteeringMode(value, validSteeringModes)

	case "thinking-display":
		switch strings.TrimSpace(value) {
		case "on":
			app.showThinking = true
		case "off":
			app.showThinking = false
		case "toggle", "":
			app.showThinking = !app.showThinking
		default:
			return nil, fmt.Errorf("usage: /set thinking-display <on|off|toggle>")
		}
		return map[string]any{"setting": "thinking-display", "value": app.showThinking}, nil

	case "thinking-level":
		return app.handleSetThinkingLevel(value, validThinkingLevels)

	case "tool-call-cutoff":
		return app.handleSetToolCallCutoff(value)

	case "tool-summary-automation":
		return app.handleSetToolSummaryAutomation(value, validToolSummaryAutomations)

	case "tool-summary-strategy":
		return app.handleSetToolSummaryStrategy(value, validToolSummaryStrategies)

	case "tools-display":
		switch strings.TrimSpace(value) {
		case "on":
			app.showTools = true
		case "off":
			app.showTools = false
		case "toggle", "":
			app.showTools = !app.showTools
		default:
			return nil, fmt.Errorf("usage: /set tools-display <on|off|toggle>")
		}
		return map[string]any{"setting": "tools-display", "value": app.showTools}, nil

	case "session-name":
		return app.handleSetSessionName(value)

	case "trace-events":
		return app.handleSetTraceEvents(value)

	default:
		return nil, fmt.Errorf("unknown setting: %s (use /set help for available settings)", key)
	}
}

func (app *rpcApp) handleModel(args string) (any, error) {
	arg := strings.TrimSpace(args)
	if arg == "" {
		return app.handleModelList()
	}

	if idx, err := strconv.Atoi(arg); err == nil {
		specs, modelsPath, err := loadModelSpecs(app.cfg)
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

	h, ok := app.server.GetSlashHandler("set_model")
	if !ok {
		return nil, fmt.Errorf("unknown command: set_model")
	}
	return h(arg)
}

func (app *rpcApp) handleToggle(args string) (any, error) {
	kind := strings.TrimSpace(args)
	switch kind {
	case "thinking":
		app.showThinking = !app.showThinking
		return map[string]any{"setting": "thinking", "value": app.showThinking}, nil
	case "prefix":
		app.showPrefix = !app.showPrefix
		return map[string]any{"setting": "prefix", "value": app.showPrefix}, nil
	case "tools":
		app.showTools = !app.showTools
		return map[string]any{"setting": "tools", "value": app.showTools}, nil
	default:
		return nil, fmt.Errorf("usage: /toggle <thinking|prefix|tools>")
	}
}

// handleShow handles the /show slash command.
func (app *rpcApp) handleShow(args string) (any, error) {
	subCmd := strings.TrimSpace(args)
	switch subCmd {
	case "settings", "":
		return app.handleShowSettings()
	default:
		return nil, fmt.Errorf("usage: /show settings")
	}
}

// registerConfigHandlers registers configuration-related slash commands.
func (app *rpcApp) registerConfigHandlers(validToolSummaryStrategies, validToolSummaryAutomations, validSteeringModes, validFollowUpModes, validThinkingLevels map[string]bool) {
	app.server.RegisterSlash("set", "Configure agent settings", func(args string) (any, error) {
		return app.handleSet(args, validToolSummaryStrategies, validToolSummaryAutomations, validSteeringModes, validFollowUpModes, validThinkingLevels)
	})

	app.server.RegisterHiddenSlash("set_model", "Set the active model by ID (internal, use /model instead)", func(args string) (any, error) {
		return app.handleModelSet(args)
	})

	app.server.RegisterSlash("model", "List models or set the active model", func(args string) (any, error) {
		return app.handleModel(args)
	})

	app.server.RegisterSlash("thinking", "Set thinking level (off/low/medium/high)", func(args string) (any, error) {
		h, ok := app.server.GetSlashHandler("set")
		if !ok {
			return nil, fmt.Errorf("unknown command: set")
		}
		return h("thinking-level " + args)
	})

	app.server.RegisterSlash("trace-events", "Configure trace events", func(args string) (any, error) {
		h, ok := app.server.GetSlashHandler("set")
		if !ok {
			return nil, fmt.Errorf("unknown command: set")
		}
		return h("trace-events " + args)
	})

	app.server.RegisterSlash("toggle", "Toggle display settings (thinking, prefix, tools)", func(args string) (any, error) {
		return app.handleToggle(args)
	})

	app.server.RegisterSlash("show", "Show agent settings or pipeline info", func(args string) (any, error) {
		return app.handleShow(args)
	})

	app.server.RegisterSlash("context", "Show current state, session stats, and available models", func(args string) (any, error) {
		_ = args
		stateH, _ := app.server.GetSlashHandler("get_state")
		statsH, _ := app.server.GetSlashHandler("get_session_stats")
		modelsH, _ := app.server.GetSlashHandler("get_available_models")

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
	})

	// get_session_stats
	app.server.RegisterHiddenSlash("get_session_stats", "Get session stats (internal)", func(args string) (any, error) {
		return app.getSessionStats()
	})
	// set_* backward-compatible forwarders
	app.server.RegisterHiddenSlash("set_auto_compaction", "Set auto-compaction (internal)", app.forwardToSet("auto-compaction"))
	app.server.RegisterHiddenSlash("set_thinking_level", "Set thinking level (internal)", app.forwardToSet("thinking-level"))
	app.server.RegisterHiddenSlash("set_trace_events", "Set trace events (internal)", app.forwardToSet("trace-events"))
	app.server.RegisterHiddenSlash("get_trace_events", "Get trace events (internal)", app.forwardToSet("trace-events"))

	// Hidden aliases — config-related
	app.registerHiddenAlias("model-select", "Select a model (alias for /model)", "model")
	app.registerHiddenAlias("get_available_models", "List all available models (internal)", "model")
}
