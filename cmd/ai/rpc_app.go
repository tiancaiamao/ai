package main

import (
	"context"
	"encoding/json"
	"fmt"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tiancaiamao/ai/pkg/agent"
	"github.com/tiancaiamao/ai/pkg/command"
	"github.com/tiancaiamao/ai/pkg/compact"
	"github.com/tiancaiamao/ai/pkg/config"
	"github.com/tiancaiamao/ai/pkg/llm"
	"github.com/tiancaiamao/ai/pkg/prompt"
	"github.com/tiancaiamao/ai/pkg/rpc"
	"github.com/tiancaiamao/ai/pkg/session"
	"github.com/tiancaiamao/ai/pkg/skill"
	"github.com/tiancaiamao/ai/pkg/tools"
	traceevent "github.com/tiancaiamao/ai/pkg/traceevent"
)

// rpcApp holds all state that was previously captured by closure variables in runRPC.
// Each field corresponds to a local variable or parameter that was captured by
// one or more server.Register / server.RegisterSlash closures.
type rpcApp struct {
	// --- Parameters from runRPC ---
	customSystemPrompt string
	maxTurns           int
	output             io.Writer
	debugAddr          string

	// --- Config ---
	cfg        *config.Config
	configPath string

	// --- Model ---
	model              llm.Model
	apiKey             string
	activeSpec         config.ModelSpec
	currentModelInfo   rpc.ModelInfo
	currentContextWindow int

	// --- Paths ---
	cwd       string
	agentDir  string

	// --- Session ---
	sessionPath string
	sessionMgr  *session.SessionManager
	sess        *session.Session
	sessionID   string
	sessionName string

	// --- Workspace & Tools ---
	ws       *tools.Workspace
	registry *tools.Registry

	// --- Compaction ---
	compactor      *compact.Compactor
	ctxManager     *compact.ContextManager
	compactorConfig *compact.Config

	// --- Tracing ---
	traceOutputPath string

	// --- Skills ---
	skillResult *skill.LoadResult
	skillStats  *skill.SkillStatsFile
	skillCommands []rpc.SlashCommand

	// --- Agent ---
	ag           *agent.Agent
	agentCtx     *agentctx.AgentContext
		loopCfg      *agent.LoopConfig
	executor     agent.ToolExecutor
	toolOutputConfig *config.ToolOutputConfig
	checkpointMgr *agent.AgentContextCheckpointManager

	// --- Session I/O ---
	sessionWriter *sessionWriter
	sessionComp   *sessionCompactor

	// --- System prompt ---
	systemPrompt string

	// --- RPC Server ---
	server *rpc.Server

	// --- Mutable state protected by stateMu ---
	stateMu           sync.Mutex
	isStreaming        bool
	isCompacting       bool
	currentThinkingLevel string
	autoCompactionEnabled bool
	steeringMode       string
	followUpMode       string
	pendingSteer       bool
	followUpQueue      []string
	showThinking       bool
	showTools          bool
	showPrefix         bool
	busyMode           string

	// --- Internal helper functions ---
	// These are assigned in initHelpers and used by handler closures.
	buildSystemPrompt               func(currentSess *session.Session) string
	restoreLLMContextFromCompaction func(sess *session.Session)
	createBaseContext               func() *agentctx.AgentContext
	setAgentContext                 func(ctx *agentctx.AgentContext)
	expandSkillCommands             func(text string) string
	compactBeforeRequest            func(trigger string)
	updateCheckpointManager         func() error
}

// parseJSONArgs attempts to unmarshal args as JSON into target.
// Returns true if args looks like JSON and was successfully parsed.
func (app *rpcApp) parseJSONArgs(args string, target any) bool {
	args = strings.TrimSpace(args)
	if len(args) > 0 && args[0] == '{' {
		return json.Unmarshal([]byte(args), target) == nil
	}
	return false
}

// initHelpers creates the closures that need access to app fields.
// Must be called after all fields are populated.
func (app *rpcApp) initHelpers() {
	// buildSystemPrompt builds the full system prompt for the given session.
	app.buildSystemPrompt = func(currentSess *session.Session) string {
		if app.customSystemPrompt != "" {
			slog.Info("Using custom system prompt", "length", len(app.customSystemPrompt))
			return app.customSystemPrompt
		}
		promptBuilder := prompt.NewBuilderWithWorkspace("", app.ws)
		promptBuilder.SetTools(app.registry.All()).SetSkills(app.skillResult.Skills).SetSkillStats(app.skillStats)

		if currentSess != nil {
			sessionDir := currentSess.GetDir()
			if sessionDir != "" {
				wm := agentctx.NewLLMContext(sessionDir)
				_ = wm
			}
		}

		return promptBuilder.Build()
	}

	// restoreLLMContextFromCompaction restores the llm context overview.md
	// from the latest compaction summary on the current session branch.
	app.restoreLLMContextFromCompaction = func(sess *session.Session) {
		summary := sess.GetLastCompactionSummary()
		if summary == "" {
			slog.Info("[resume-on-branch] No compaction summary found, skipping llm context restore")
			return
		}

		sessionDir := sess.GetDir()
		if sessionDir == "" {
			slog.Warn("[resume-on-branch] No session directory, cannot restore llm context")
			return
		}

		wm := agentctx.NewLLMContext(sessionDir)
		if err := wm.WriteContent(summary); err != nil {
			slog.Warn("[resume-on-branch] Failed to restore llm context", "error", err)
		} else {
			slog.Info("[resume-on-branch] Restored llm context from compaction summary", "summary_len", len(summary))
		}
	}

	// createBaseContext creates a new agent context from the current session.
	app.createBaseContext = func() *agentctx.AgentContext {
		app.systemPrompt = app.buildSystemPrompt(app.sess)
		ctx := agentctx.NewAgentContext(app.systemPrompt)
		for _, tool := range app.registry.All() {
			ctx.AddTool(tool)
		}
		if app.sess != nil {
			sessionDir := app.sess.GetDir()
			if sessionDir != "" {
				ctx.LLMContext = ""
			}
			ctx.RecentMessages = app.sess.GetMessages()
			if sessionDir != "" {
				if cpInfo, err := agentctx.LoadLatestCheckpoint(sessionDir); err == nil && cpInfo != nil {
					cpPath := filepath.Join(sessionDir, cpInfo.Path)
					if snapshot, err := agentctx.LoadCheckpoint(sessionDir, cpInfo); err == nil && snapshot != nil {
						if len(snapshot.RecentMessages) > 0 {
							sessionMsgCount := len(ctx.RecentMessages)
							ctx.RecentMessages = snapshot.RecentMessages
							slog.Info("Restored messages from checkpoint",
								"checkpoint_messages", len(snapshot.RecentMessages),
								"session_messages", sessionMsgCount,
								"saved", sessionMsgCount-len(snapshot.RecentMessages),
							)
						}
						if snapshot.AgentState != nil {
							ctx.AgentState = snapshot.AgentState
							if snapshot.AgentState.CurrentWorkingDir != "" {
								if err := app.ws.SetCWD(snapshot.AgentState.CurrentWorkingDir); err != nil {
									slog.Warn("Failed to restore CWD from checkpoint", "cwd", snapshot.AgentState.CurrentWorkingDir, "error", err)
								}
							}
							slog.Info("Restored agent state from checkpoint",
								"turns", snapshot.AgentState.TotalTurns,
								"tokens", snapshot.AgentState.TokensUsed,
								"toolCallsSince", snapshot.AgentState.ToolCallsSinceLastTrigger,
								"cwd", snapshot.AgentState.CurrentWorkingDir,
							)
						}
						if snapshot.LLMContext != "" {
							ctx.LLMContext = snapshot.LLMContext
						}
					} else {
						if savedState, err := agentctx.LoadCheckpointAgentState(cpPath); err == nil {
							ctx.AgentState = savedState
							if savedState.CurrentWorkingDir != "" {
								if err := app.ws.SetCWD(savedState.CurrentWorkingDir); err != nil {
									slog.Warn("Failed to restore CWD from checkpoint", "cwd", savedState.CurrentWorkingDir, "error", err)
								}
							}
							slog.Info("Restored agent state from checkpoint (no messages)",
								"turns", savedState.TotalTurns,
								"tokens", savedState.TokensUsed,
								"toolCallsSince", savedState.ToolCallsSinceLastTrigger,
								"cwd", savedState.CurrentWorkingDir,
							)
						}
					}
				}
			}
			ctx.OnCompactEvent = func(detail *agentctx.CompactEventDetail) error {
				return app.sess.AppendCompactEvent(detail)
			}
		}
		return ctx
	}

	app.setAgentContext = func(ctx *agentctx.AgentContext) {
		app.ag.SetContext(ctx)
	}

	app.expandSkillCommands = func(text string) string {
		expanded := skill.ExpandCommand(text, app.skillResult.Skills)
		if skill.IsSkillCommand(text) {
			skillName := skill.ExtractSkillName(text)
			app.skillStats.RecordUsage(skillName)
			if err := app.skillStats.Save(); err != nil {
				slog.Error("Failed to save skill stats", "skill", skillName, "error", err)
			}
		}
		return expanded
	}

	app.compactBeforeRequest = func(trigger string) {
		if app.compactor == nil || app.sess == nil {
			return
		}

		messages := app.ag.GetMessages()
		if !app.compactor.ShouldCompactOld(messages) {
			return
		}
		if !app.sess.CanCompact(app.compactor) {
			slog.Info("Pre-request compaction skipped: session not compactable",
				"trigger", trigger,
				"messages", len(messages),
				"estimatedTokens", app.compactor.EstimateContextTokensOld(messages))
			return
		}

		beforeCount := len(messages)
		compactionInfo := agent.CompactionInfo{
			Auto:    true,
			Before:  beforeCount,
			Trigger: trigger,
		}

		app.stateMu.Lock()
		app.isCompacting = true
		app.stateMu.Unlock()
		app.server.EmitEvent(agent.NewCompactionStartEvent(compactionInfo))

		err := runDetachedTraceSpan(
			"compaction",
			traceevent.CategoryEvent,
			[]traceevent.Field{
				{Key: "source", Value: "pre_request"},
				{Key: "auto", Value: true},
				{Key: "trigger", Value: trigger},
				{Key: "before_messages", Value: beforeCount},
			},
			func(_ context.Context, span *traceevent.Span) error {
				result, err := app.sess.Compact(app.compactor)
				if err != nil {
					return err
				}

				app.ag.GetContext().RecentMessages = app.sess.GetMessages()
				afterCount := len(app.ag.GetMessages())
				compactionInfo.After = afterCount

				span.AddField("after_messages", afterCount)
				span.AddField("tokens_before", result.TokensBefore)
				span.AddField("tokens_after", result.TokensAfter)
				return nil
			},
		)

		app.stateMu.Lock()
		app.isCompacting = false
		app.stateMu.Unlock()

		if err != nil {
			compactionInfo.Error = err.Error()
			if session.IsNonActionableCompactionError(err) {
				slog.Info("Pre-request compaction skipped", "trigger", trigger, "reason", err)
			} else {
				slog.Error("Pre-request compaction failed", "trigger", trigger, "error", err)
			}
		}
		app.server.EmitEvent(agent.NewCompactionEndEvent(compactionInfo))
	}

	app.updateCheckpointManager = func() error {
		app.stateMu.Lock()
		defer app.stateMu.Unlock()

		if app.checkpointMgr != nil {
			if err := app.checkpointMgr.Close(); err != nil {
				slog.Warn("Failed to close old checkpoint manager", "error", err)
			}
		}

		if app.sess != nil {
			sessionDir := app.sess.GetDir()
			if sessionDir != "" {
				mgr, err := agent.NewAgentContextCheckpointManager(sessionDir)
				if err != nil {
					slog.Warn("Failed to create checkpoint manager", "error", err)
					app.checkpointMgr = nil
				} else {
					app.checkpointMgr = mgr
					slog.Info("Updated checkpoint manager", "sessionDir", sessionDir)
				}
			} else {
				slog.Warn("Session directory is empty, checkpoint manager not updated")
				app.checkpointMgr = nil
			}
		}

		return nil
	}
}

// initEventEmitter starts the goroutine that reads agent events and forwards
// them to the RPC server. Returns shutdown channel and done channel.
func (app *rpcApp) initEventEmitter() (chan struct{}, chan struct{}) {
	eventEmitterDone := make(chan struct{})
	shutdownEmitter := make(chan struct{})

		processEvent := func(event agent.AgentEvent) {
		if event.Type == "agent_start" {
			app.stateMu.Lock()
			app.isStreaming = true
			app.stateMu.Unlock()
		}
		if event.Type == "agent_end" {
			app.stateMu.Lock()
			app.isStreaming = false
			app.isCompacting = false
			app.pendingSteer = false
			app.stateMu.Unlock()
		}
		if event.Type == "compaction_start" {
			app.stateMu.Lock()
			app.isCompacting = true
			app.stateMu.Unlock()
		}
		if event.Type == "compaction_end" {
			app.stateMu.Lock()
			app.isCompacting = false
			app.stateMu.Unlock()
		}

		if event.Type == "message_end" && event.Message != nil {
			if app.sessionWriter != nil {
				app.sessionWriter.Append(app.sess, *event.Message)
			}
		}
		if event.Type == "tool_execution_end" && event.Result != nil {
			if app.sessionWriter != nil {
				app.sessionWriter.Append(app.sess, *event.Result)
			}
		}

		emitAt := time.Now()
		if event.EventAt == 0 {
			event.EventAt = emitAt.UnixNano()
		}
		app.server.EmitEvent(event)

		if event.Type == "agent_end" {
			go func() {
				if err := app.sessionMgr.SaveCurrent(); err != nil {
					slog.Info("Failed to update session metadata:", "value", err)
				}
			}()
		}
	}

	go func() {
		defer close(eventEmitterDone)
		for {
			select {
			case event := <-app.ag.Events():
				processEvent(event)
			case <-shutdownEmitter:
				// Drain remaining events
				for {
					select {
					case event := <-app.ag.Events():
						processEvent(event)
					default:
						return
					}
				}
			}
		}
	}()

	return shutdownEmitter, eventEmitterDone
}

// startDebugServer starts the HTTP debug server if debugAddr is set.
func (app *rpcApp) startDebugServer() {
	if app.debugAddr == "" {
		return
	}
	go func() {
		// Register metrics endpoint on DefaultServeMux
		http.HandleFunc("/debug/metrics", func(w http.ResponseWriter, r *http.Request) {
			metrics := app.ag.GetMetrics()
			if metrics == nil {
				http.Error(w, "Metrics not available", http.StatusServiceUnavailable)
				return
			}

			fullMetrics := metrics.GetFullMetrics()
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(fullMetrics); err != nil {
				slog.Error("Failed to encode metrics:", "value", err)
				http.Error(w, "Failed to encode metrics", http.StatusInternalServerError)
			}
		})

		slog.Info("Debug server listening on", "value", app.debugAddr)
		slog.Info("Debug endpoints available at:")
		slog.Info("- http:///debug/pprof/          (profiling index)", "value", app.debugAddr)
		slog.Info("- http:///debug/pprof/profile   (CPU profile)", "value", app.debugAddr)
		slog.Info("- http:///debug/pprof/heap       (memory profile)", "value", app.debugAddr)
		slog.Info("- http:///debug/pprof/goroutine  (goroutine dump)", "value", app.debugAddr)
		slog.Info("- http:///debug/pprof/trace      (execution trace)", "value", app.debugAddr)
		slog.Info("- http:///debug/metrics         (agent metrics)", "value", app.debugAddr)

		if err := http.ListenAndServe(app.debugAddr, nil); err != nil {
			slog.Error("Debug server error:", "error", err)
		}
	}()
}

// emitStartEvent sends the initial server_start event with model and tool info.
func (app *rpcApp) emitStartEvent() {
	allTools := app.registry.All()
	toolNames := make([]string, len(allTools))
	for i, t := range allTools {
		toolNames[i] = t.Name()
	}
	app.server.EmitEvent(map[string]any{
		"type":  "server_start",
		"model": app.model.ID,
		"tools": toolNames,
	})
}

// --- Slash command aliases (registration helpers) ---

// registerHiddenAlias creates a hidden slash command that delegates to a canonical one.
func (app *rpcApp) registerHiddenAlias(alias, desc, canonical string) {
	app.server.RegisterHiddenSlash(alias, desc, func(args string) (any, error) {
		h, ok := app.server.GetSlashHandler(canonical)
		if !ok {
			return nil, fmt.Errorf("unknown command: /%s", canonical)
		}
		return h(args)
	})
}

// forwardToSet creates a handler that prepends a subcommand prefix and delegates to /set.
func (app *rpcApp) forwardToSet(subcmd string) func(string) (any, error) {
	return func(args string) (any, error) {
		h, ok := app.server.GetSlashHandler("set")
		if !ok {
			return nil, fmt.Errorf("unknown command: set")
		}
		return h(subcmd + " " + args)
	}
}

// collectSessionUsageFromAgent is a convenience wrapper around collectSessionUsage
// that reads from the current agent state.
func (app *rpcApp) collectSessionUsageFromAgent() (int, int, int, int, rpc.SessionTokenStats, float64) {
	return collectSessionUsage(app.ag.GetMessages())
}

// getSessionStats returns SessionStats for the current session state.
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

// getCurrentAILogPath returns the trace output path, checking the global handler first.
func (app *rpcApp) getCurrentAILogPath() string {
	aiLogPath := app.traceOutputPath
	if handler := traceevent.GetHandler(); handler != nil {
		if fh, ok := handler.(*traceevent.FileHandler); ok {
			aiLogPath = fh.TraceFilePath("")
		}
	}
	return aiLogPath
}

// setSession updates all session-related state after a session switch.
func (app *rpcApp) setSession(newSess *session.Session, newID, newName string) {
	app.sess = newSess
	app.sessionComp.Update(app.sess, app.compactor)
	app.setAgentContext(app.createBaseContext())

	if err := app.updateCheckpointManager(); err != nil {
		slog.Warn("Failed to update checkpoint manager", "error", err)
	}

	app.stateMu.Lock()
	app.sessionID = newID
	app.sessionName = newName
	app.stateMu.Unlock()
}

// handleModelSet sets the active model by provider/modelId.
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
	app.sessionComp.Update(app.sess, app.compactor)
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

// handleModelList returns the list of available models and marks the current one.
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

// handleNewSession creates a new session and switches to it.
func (app *rpcApp) handleNewSession(args string) (any, error) {
	var name, title string
	var jsonData struct {
		Name  string `json:"name"`
		Title string `json:"title"`
	}
	if app.parseJSONArgs(args, &jsonData) {
		name, title = jsonData.Name, jsonData.Title
	} else {
		parts := strings.SplitN(args, " ", 2)
		name = strings.TrimSpace(parts[0])
		if len(parts) > 1 {
			title = strings.TrimSpace(parts[1])
		}
	}
	slog.Info("Received new_session", "name", name, "title", title)
	if strings.TrimSpace(name) == "" {
		name = time.Now().Format("20060102-150405")
	}
	if strings.TrimSpace(title) == "" {
		title = name
	}
	newSess, err := app.sessionMgr.CreateSession(name, title)
	if err != nil {
		return nil, err
	}

	newSessionID := newSess.GetID()

	if err := app.sessionMgr.SetCurrent(newSessionID); err != nil {
		return nil, err
	}
	if err := app.sessionMgr.SaveCurrent(); err != nil {
		slog.Info("Failed to update session metadata:", "value", err)
	}

	app.setSession(newSess, newSessionID, name)

	slog.Info("Created new session", "name", name, "id", newSessionID)
	app.server.EmitEvent(map[string]any{"type": "session_switch", "session": newSessionID, "sessionName": name})
	return map[string]any{"sessionId": newSessionID, "cancelled": false}, nil
}

// handleResume handles the /resume command for session switching.
func (app *rpcApp) handleResume(args string) (any, error) {
	arg := strings.TrimSpace(args)
	if arg == "" {
		sessions, err := app.sessionMgr.ListSessions()
		if err != nil {
			return nil, fmt.Errorf("failed to list sessions: %w", err)
		}
		return map[string]any{"sessions": sessions}, nil
	}

	var targetID string
	sessions, err := app.sessionMgr.ListSessions()
	if err != nil {
		return nil, fmt.Errorf("failed to list sessions: %w", err)
	}

	if idx, err := strconv.Atoi(arg); err == nil {
		if idx < 0 || idx >= len(sessions) {
			return nil, fmt.Errorf("session index %d out of range (0-%d)", idx, len(sessions)-1)
		}
		targetID = sessions[idx].ID
	} else {
		for _, s := range sessions {
			if s.ID == arg {
				targetID = s.ID
				break
			}
		}
		if targetID == "" {
			for _, s := range sessions {
				if s.Name == arg {
					targetID = s.ID
					break
				}
			}
		}
		if targetID == "" {
			return nil, fmt.Errorf("session not found: %s", arg)
		}
	}

	newSess, err := app.sessionMgr.GetSession(targetID)
	if err != nil {
		return nil, fmt.Errorf("failed to load session %s: %w", targetID, err)
	}

	if err := app.sessionMgr.SetCurrent(targetID); err != nil {
		return nil, err
	}
	if err := app.sessionMgr.SaveCurrent(); err != nil {
		slog.Info("Failed to update session metadata:", "value", err)
	}

	newSessionName := resolveSessionName(app.sessionMgr, targetID)
	app.setSession(newSess, targetID, newSessionName)

	slog.Info("Switched to session", "id", targetID, "name", newSessionName)
	app.server.EmitEvent(map[string]any{"type": "session_switch", "session": targetID, "sessionName": newSessionName})
	return map[string]any{"sessionId": targetID, "sessionName": newSessionName}, nil
}

// handleCompact performs a manual compaction of the conversation history.
func (app *rpcApp) handleCompact(args string) (any, error) {
	_ = args
	slog.Info("Received compact")
	beforeCount := len(app.ag.GetMessages())

	estimatedTokens := app.compactor.EstimateContextTokensOld(app.ag.GetMessages())
	keepTokens := app.compactor.KeepRecentTokens()

	if !app.sess.CanCompact(app.compactor) {
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
	app.stateMu.Lock()
	app.isCompacting = true
	app.stateMu.Unlock()
	app.server.EmitEvent(agent.NewCompactionStartEvent(compactionInfo))
	defer func() {
		app.stateMu.Lock()
		app.isCompacting = false
		app.stateMu.Unlock()
		app.server.EmitEvent(agent.NewCompactionEndEvent(compactionInfo))
	}()

	var response *rpc.CompactResult
	err := runDetachedTraceSpan(
		"compaction",
		traceevent.CategoryEvent,
		[]traceevent.Field{{Key: "source", Value: "manual"}},
		func(_ context.Context, span *traceevent.Span) error {
			span.AddField("before_messages", beforeCount)

			result, err := app.sess.Compact(app.compactor)
			if err != nil {
				slog.Info("Compact failed:", "value", err)
				return err
			}

			app.ag.GetContext().RecentMessages = app.sess.GetMessages()

			afterCount := len(app.ag.GetMessages())
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

			if app.checkpointMgr != nil && app.checkpointMgr.ShouldCheckpoint() {
				agentCtx := app.ag.GetContext()
				slog.Info("[Loop] Creating checkpoint after manual compact", "trigger", "manual_command", "turn", agentCtx.AgentState.TotalTurns)
				checkpointTurn, err := app.checkpointMgr.CreateSnapshot(agentCtx, agentCtx.LLMContext, agentCtx.AgentState.TotalTurns)
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

// handleSetAutoCompaction handles the auto-compaction subcommand of /set.
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

// handleSetThinkingLevel handles the thinking-level subcommand of /set.
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

// handleSetFollowUpMode handles the follow-up-mode subcommand of /set.
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

// handleSetSteeringMode handles the steering-mode subcommand of /set.
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

// handleSetToolCallCutoff handles the tool-call-cutoff subcommand of /set.
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

// handleSetToolSummaryAutomation handles the tool-summary-automation subcommand of /set.
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

// handleSetToolSummaryStrategy handles the tool-summary-strategy subcommand of /set.
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

// handleSetSessionName handles the session-name subcommand of /set.
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

// handleSetTraceEvents handles the trace-events subcommand of /set.
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

// handleRewind handles the /rewind command for branch switching.
func (app *rpcApp) handleRewind(args string) (any, error) {
	var jsonData struct {
		EntryID string `json:"entryId"`
	}
	entryID := strings.TrimSpace(args)
	if app.parseJSONArgs(args, &jsonData) && jsonData.EntryID != "" {
		entryID = jsonData.EntryID
	}
	slog.Info("Received rewind", "entryId", entryID)
	app.stateMu.Lock()
	streaming := app.isStreaming
	app.stateMu.Unlock()
	if streaming {
		return nil, fmt.Errorf("agent is busy")
	}

	if entryID == "" {
		return nil, fmt.Errorf("entryId is required")
	}

	if entryID == "root" {
		app.sess.ResetLeaf()
	} else {
		if err := app.sess.Branch(entryID); err != nil {
			return nil, err
		}
	}

	app.setAgentContext(app.createBaseContext())
	app.restoreLLMContextFromCompaction(app.sess)

	if err := app.sessionMgr.SaveCurrent(); err != nil {
		slog.Info("Failed to update session metadata:", "value", err)
	}
	return map[string]any{"switched": true}, nil
}

// handleFork handles the /fork command for conversation forking.
func (app *rpcApp) handleFork(args string) (any, error) {
	var jsonData struct {
		EntryID string `json:"entryId"`
	}
	entryID := strings.TrimSpace(args)
	if app.parseJSONArgs(args, &jsonData) && jsonData.EntryID != "" {
		entryID = jsonData.EntryID
	}
	slog.Info("Received fork: entryId=", "value", entryID)
	entry, ok := app.sess.GetEntry(entryID)
	if !ok || entry.Type != session.EntryTypeMessage || entry.Message == nil || entry.Message.Role != "user" {
		return nil, fmt.Errorf("invalid entryId: %s", entryID)
	}

	text := entry.Message.ExtractText()
	name := fmt.Sprintf("fork-%s", time.Now().Format("20060102-150405"))
	title := "Forked Session"
	newSess, err := app.sessionMgr.ForkSessionFrom(app.sess, entry.ParentID, name, title)
	if err != nil {
		return nil, err
	}

	newSessionID := newSess.GetID()

	if err := app.sessionMgr.SetCurrent(newSessionID); err != nil {
		return nil, err
	}
	if err := app.sessionMgr.SaveCurrent(); err != nil {
		slog.Info("Failed to update session metadata:", "value", err)
	}

	app.setSession(newSess, newSessionID, name)

	slog.Info("Forked to new session", "name", name, "id", newSessionID)
	return &rpc.ForkResult{Cancelled: false, Text: text}, nil
}

// handleSessionGetState returns the current agent/session state.
func (app *rpcApp) handleSessionGetState() (any, error) {
	slog.Info("Received get_state")
	compactionState := buildCompactionState(app.compactorConfig, app.compactor)
	app.stateMu.Lock()
	currentSessionID := app.sessionID
	currentSessionName := app.sessionName
	streaming := app.isStreaming
	compacting := app.isCompacting
	thinkingLevel := app.currentThinkingLevel
	autoCompact := app.autoCompactionEnabled
	currentSteeringMode := app.steeringMode
	currentFollowUpMode := app.followUpMode
	modelInfo := app.currentModelInfo
	app.stateMu.Unlock()

	aiLogPath := app.getCurrentAILogPath()

	return &rpc.SessionState{
		Model:                 &modelInfo,
		ThinkingLevel:         thinkingLevel,
		IsStreaming:           streaming,
		IsCompacting:          compacting,
		SteeringMode:          currentSteeringMode,
		FollowUpMode:          currentFollowUpMode,
		SessionFile:           app.sess.GetPath(),
		SessionID:             currentSessionID,
		SessionName:           currentSessionName,
		AIPid:                 os.Getpid(),
		AILogPath:             aiLogPath,
		AIWorkingDir:          app.ws.GetCWD(),
		AIStartupPath:         app.ws.GetGitRoot(),
		AutoCompactionEnabled: autoCompact,
		MessageCount:          len(app.ag.GetMessages()),
		PendingMessageCount:   app.ag.GetPendingFollowUps(),
		Compaction:            compactionState,
	}, nil
}

// handleShowSettings returns the current settings for /show settings.
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

// handleSetAutoRetry handles the auto-retry subcommand of /set.
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

// handleSet is the /set command dispatcher.
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

// handlePrompt handles the prompt RPC command.
func (app *rpcApp) handlePrompt(cmd rpc.RPCCommand) (any, error) {
	var data struct {
		Message           string            `json:"message"`
		StreamingBehavior string            `json:"streamingBehavior"`
		Images            []json.RawMessage `json:"images"`
	}
	if len(cmd.Data) > 0 {
		if err := json.Unmarshal(cmd.Data, &data); err != nil {
			return nil, fmt.Errorf("invalid data: %w", err)
		}
	}
	message := cmd.Message
	if message == "" {
		message = data.Message
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return nil, fmt.Errorf("empty prompt message")
	}
	if len(data.Images) > 0 {
		return nil, fmt.Errorf("images are not supported in this RPC implementation")
	}

	// Expand /skill:name commands BEFORE generic slash dispatch.
	if skill.IsSkillCommand(message) {
		expandedMessage := app.expandSkillCommands(message)
		slog.Info("Expanded skill command", "original", message, "skill", skill.ExtractSkillName(message))

		app.stateMu.Lock()
		streaming := app.isStreaming
		app.stateMu.Unlock()

		if streaming {
			app.stateMu.Lock()
			app.pendingSteer = true
			app.stateMu.Unlock()
			app.ag.Steer(expandedMessage)
			return nil, nil
		}

		app.compactBeforeRequest("pre_request_prompt")
		return nil, app.ag.Prompt(expandedMessage)
	}

	// Intercept slash commands
	if message[0] == '/' {
		cmdName, args, err := command.ParseSlashCommand(message)
		if err != nil {
			return nil, fmt.Errorf("invalid slash command: %w", err)
		}
		handler, ok := app.server.GetSlashHandler(cmdName)
		if !ok {
			return nil, fmt.Errorf("unknown command: /%s", cmdName)
		}
		return handler(args)
	}

	app.stateMu.Lock()
	streaming := app.isStreaming
	mode := app.steeringMode
	followMode := app.followUpMode
	pending := app.pendingSteer
	app.stateMu.Unlock()

	if streaming {
		behavior := strings.TrimSpace(data.StreamingBehavior)
		if behavior == "" {
			behavior = "steer"
		}
		switch behavior {
		case "steer":
			if mode == "one-at-a-time" && pending {
				return nil, fmt.Errorf("steer already pending")
			}
			app.stateMu.Lock()
			app.pendingSteer = true
			app.stateMu.Unlock()
			app.ag.Steer(message)
			return nil, nil
		case "followUp", "follow_up":
			if followMode == "one-at-a-time" && app.ag.GetPendingFollowUps() > 0 {
				return nil, fmt.Errorf("follow-up queue already has a pending message")
			}
			return nil, app.ag.FollowUp(message)
		case "reject":
			return nil, fmt.Errorf("agent is streaming; rejected by busy-mode policy")
		default:
			return nil, fmt.Errorf("invalid streamingBehavior: %s", behavior)
		}
	}

	app.compactBeforeRequest("pre_request_prompt")
	return nil, app.ag.Prompt(message)
}

// handleSteer handles the steer RPC command.
func (app *rpcApp) handleSteer(cmd rpc.RPCCommand) (any, error) {
	message := cmd.Message
	if message == "" && len(cmd.Data) > 0 {
		var data struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(cmd.Data, &data); err != nil {
			return nil, fmt.Errorf("invalid data: %w", err)
		}
		message = data.Message
	}

	slog.Info("Received steer:", "value", message)

	if strings.TrimSpace(message) == "" {
		return nil, fmt.Errorf("empty steer message")
	}

	expandedMessage := app.expandSkillCommands(message)
	if skill.IsSkillCommand(message) {
		slog.Info("Expanded skill command in steer", "original", message, "skill", skill.ExtractSkillName(message))
	}

	app.stateMu.Lock()
	mode := app.steeringMode
	pending := app.pendingSteer
	streaming := app.isStreaming
	app.stateMu.Unlock()
	if mode == "one-at-a-time" && pending {
		return nil, fmt.Errorf("steer already pending")
	}
	if !streaming {
		app.compactBeforeRequest("pre_request_steer")
	}
	app.stateMu.Lock()
	app.pendingSteer = true
	app.stateMu.Unlock()
	app.ag.Steer(expandedMessage)
	return nil, nil
}

// handleFollowUp handles the follow_up RPC command.
func (app *rpcApp) handleFollowUp(cmd rpc.RPCCommand) (any, error) {
	message := cmd.Message
	if message == "" && len(cmd.Data) > 0 {
		var data struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(cmd.Data, &data); err != nil {
			return nil, fmt.Errorf("invalid data: %w", err)
		}
		message = data.Message
	}

	slog.Info("Received follow_up:", "value", message)
	if strings.TrimSpace(message) == "" {
		return nil, fmt.Errorf("empty follow-up message")
	}

	expandedMessage := app.expandSkillCommands(message)
	if skill.IsSkillCommand(message) {
		slog.Info("Expanded skill command in follow_up", "original", message, "skill", skill.ExtractSkillName(message))
	}

	app.stateMu.Lock()
	mode := app.followUpMode
	app.stateMu.Unlock()
	if mode == "one-at-a-time" && app.ag.GetPendingFollowUps() > 0 {
		return nil, fmt.Errorf("follow-up queue already has a pending message")
	}
	return nil, app.ag.FollowUp(expandedMessage)
}

// handleAbort handles the abort RPC command.
func (app *rpcApp) handleAbort(cmd rpc.RPCCommand) (any, error) {
	_ = cmd
	slog.Info("Received abort")
	app.ag.Abort()
	return nil, nil
}

// handleGetMessages handles the /messages slash command.
func (app *rpcApp) handleGetMessages(args string) (any, error) {
	slog.Info("Received get_messages", "args", args)
	const defaultCount = 20
	const maxPreviewLen = 200

	count := defaultCount
	args = strings.TrimSpace(args)
	if args != "" {
		if n, err := strconv.Atoi(args); err == nil && n > 0 {
			count = n
		}
	}

	messages := app.ag.GetMessages()
	return formatMessagesForDisplay(messages, count, maxPreviewLen), nil
}

// handleGetLastAssistantText returns the last assistant message text.
func (app *rpcApp) handleGetLastAssistantText(args string) (any, error) {
	_ = args
	slog.Info("Received get_last_assistant_text")
	messages := app.ag.GetMessages()
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" {
			return map[string]any{"text": messages[i].ExtractText()}, nil
		}
	}
	return "", nil
}

// handleGetForkMessages returns messages available for forking.
func (app *rpcApp) handleGetForkMessages(args string) (any, error) {
	_ = args
	slog.Info("Received get_fork_messages")
	forkMessages := app.sess.GetUserMessagesForForking()
	result := make([]rpc.ForkMessage, 0, len(forkMessages))
	for _, msg := range forkMessages {
		result = append(result, rpc.ForkMessage{
			EntryID: msg.EntryID,
			Text:    msg.Text,
		})
	}
	return map[string]any{"messages": result}, nil
}

// handleGetTree returns the conversation tree structure.
func (app *rpcApp) handleGetTree(args string) (any, error) {
	_ = args
	slog.Info("Received get_tree")
	entries := app.sess.GetEntries()
	tree := buildTreeEntries(entries, app.sess.GetLeafID())
	return map[string]any{"entries": tree}, nil
}

// handleModel handles the /model slash command.
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

// handleToggle handles the /toggle slash command.
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

// handleFollowUpSlash handles the /follow-up slash command.
func (app *rpcApp) handleFollowUpSlash(args string) (any, error) {
	message := strings.TrimSpace(args)
	if message == "" {
		return nil, fmt.Errorf("usage: /follow-up <message>")
	}

	slog.Info("Received follow-up command")
	app.stateMu.Lock()
	streaming := app.isStreaming
	app.stateMu.Unlock()

	if !streaming {
		return nil, fmt.Errorf("agent is not busy")
	}

	if app.followUpMode != "one-at-a-time" && app.followUpMode != "queue" {
		return nil, fmt.Errorf("follow-up mode is '%s', not enabled", app.followUpMode)
	}

	if len(app.followUpQueue) > 0 && app.followUpMode == "one-at-a-time" {
		return nil, fmt.Errorf("follow-up queue already has a pending message")
	}

	expandedMessage := app.expandSkillCommands(message)
	app.followUpQueue = append(app.followUpQueue, expandedMessage)
	return map[string]any{"status": "queued", "message": expandedMessage}, nil
}

// handleAbortSlash handles the /abort slash command.
func (app *rpcApp) handleAbortSlash(args string) (any, error) {
	_ = args
	slog.Info("Received abort command")
	app.stateMu.Lock()
	streaming := app.isStreaming
	app.stateMu.Unlock()

	if !streaming {
		return nil, fmt.Errorf("agent is not streaming")
	}

	app.ag.Abort()
	return map[string]any{"status": "aborting"}, nil
}

// handleExportHTML handles the /export_html slash command.
func (app *rpcApp) handleExportHTML(args string) (any, error) {
	slog.Info("Received export_html", "outputPath", args)
	return "", fmt.Errorf("export_html is not supported")
}

// handleGetWorkflowStatus handles the /get_workflow_status hidden slash command.
func (app *rpcApp) handleGetWorkflowStatus(args string) (any, error) {
	_ = args
	slog.Info("Received get_workflow_status")
	status, err := getWorkflowStatus(app.ws.GetCWD())
	if err != nil {
		return nil, err
	}
	if status == nil {
		return nil, nil
	}
	return status, nil
}

// registerHandlers registers all RPC command handlers and slash commands on the server.
// validXxx maps are passed in because they are compile-time constants.
func (app *rpcApp) registerHandlers(
	validToolSummaryStrategies, validToolSummaryAutomations, validSteeringModes, validFollowUpModes, validThinkingLevels map[string]bool,
) {
	// === Protocol command handlers ===
	app.server.Register(rpc.CommandPrompt, func(cmd rpc.RPCCommand) (any, error) {
		return app.handlePrompt(cmd)
	})

	app.server.Register(rpc.CommandSteer, func(cmd rpc.RPCCommand) (any, error) {
		return app.handleSteer(cmd)
	})

	app.server.Register(rpc.CommandFollowUp, func(cmd rpc.RPCCommand) (any, error) {
		return app.handleFollowUp(cmd)
	})

	app.server.Register(rpc.CommandAbort, func(cmd rpc.RPCCommand) (any, error) {
		return app.handleAbort(cmd)
	})

	// === Slash command handlers ===
	app.server.RegisterSlash("new", "Create a new session and switch to it", func(args string) (any, error) {
		return app.handleNewSession(args)
	})

	app.server.RegisterSlash("session", "Get the current agent state (model, session, streaming status)", func(args string) (any, error) {
		return app.handleSessionGetState()
	})

	app.server.RegisterSlash("messages", "Get formatted message summaries for the current session", func(args string) (any, error) {
		return app.handleGetMessages(args)
	})

	app.server.RegisterSlash("compact", "Compact conversation history to reduce context size", func(args string) (any, error) {
		return app.handleCompact(args)
	})

	app.server.RegisterHiddenSlash("set_model", "Set the active model by ID (internal, use /model instead)", func(args string) (any, error) {
		return app.handleModelSet(args)
	})

	app.server.RegisterSlash("export_html", "Export the current session as HTML", func(args string) (any, error) {
		return app.handleExportHTML(args)
	})

	app.server.RegisterSlash("set", "Configure agent settings", func(args string) (any, error) {
		return app.handleSet(args, validToolSummaryStrategies, validToolSummaryAutomations, validSteeringModes, validFollowUpModes, validThinkingLevels)
	})

	app.server.RegisterHiddenSlash("get_workflow_status", "Get workflow task status (internal)", func(args string) (any, error) {
		return app.handleGetWorkflowStatus(args)
	})

	app.server.RegisterHiddenSlash("get_last_assistant_text", "Get the last assistant text response (internal)", func(args string) (any, error) {
		return app.handleGetLastAssistantText(args)
	})

	app.server.RegisterHiddenSlash("get_fork_messages", "Get messages for a fork point (internal)", func(args string) (any, error) {
		return app.handleGetForkMessages(args)
	})

	app.server.RegisterHiddenSlash("get_tree", "Get the conversation tree structure (internal)", func(args string) (any, error) {
		return app.handleGetTree(args)
	})

	app.server.RegisterSlash("rewind", "Resume generation on a specific branch", func(args string) (any, error) {
		return app.handleRewind(args)
	})

	app.server.RegisterSlash("fork", "Fork the conversation at a specific entry point", func(args string) (any, error) {
		return app.handleFork(args)
	})

	// /help
	app.server.RegisterSlash("help", "Show available slash commands", func(args string) (any, error) {
		commands := app.server.ListSlashCommands()
		return map[string]any{"commands": commands}, nil
	})

	// /skills
	app.server.RegisterSlash("skills", "List available skills", func(args string) (any, error) {
		_ = args
		slog.Info("Received skills")
		return map[string]any{"commands": app.skillCommands}, nil
	})

	// /thinking → shortcut for /set thinking-level
	app.server.RegisterSlash("thinking", "Set thinking level (off/low/medium/high)", func(args string) (any, error) {
		h, ok := app.server.GetSlashHandler("set")
		if !ok {
			return nil, fmt.Errorf("unknown command: set")
		}
		return h("thinking-level " + args)
	})

	// /trace-events → shortcut for /set trace-events
	app.server.RegisterSlash("trace-events", "Configure trace events", func(args string) (any, error) {
		h, ok := app.server.GetSlashHandler("set")
		if !ok {
			return nil, fmt.Errorf("unknown command: set")
		}
		return h("trace-events " + args)
	})

	// Hidden aliases
	app.registerHiddenAlias("model-select", "Select a model (alias for /model)", "model")
	app.registerHiddenAlias("get_available_models", "List all available models (internal)", "model")
	app.registerHiddenAlias("get_messages", "Get session messages (internal)", "messages")
	app.registerHiddenAlias("get_state", "Get agent state (internal)", "session")
	app.registerHiddenAlias("get_commands", "List commands (internal)", "skills")

	// get_session_stats: compute real SessionStats
	app.server.RegisterHiddenSlash("get_session_stats", "Get session stats (internal)", func(args string) (any, error) {
		return app.getSessionStats()
	})

	app.registerHiddenAlias("new_session", "Create new session (internal)", "new")
	app.registerHiddenAlias("list_sessions", "List sessions (internal)", "resume")
	app.registerHiddenAlias("switch_session", "Switch session (internal)", "resume")

	// set_* backward-compatible forwarders
	app.server.RegisterHiddenSlash("set_auto_compaction", "Set auto-compaction (internal)", app.forwardToSet("auto-compaction"))
	app.server.RegisterHiddenSlash("set_thinking_level", "Set thinking level (internal)", app.forwardToSet("thinking-level"))
	app.server.RegisterHiddenSlash("set_trace_events", "Set trace events (internal)", app.forwardToSet("trace-events"))
	app.server.RegisterHiddenSlash("get_trace_events", "Get trace events (internal)", app.forwardToSet("trace-events"))

	// /model
	app.server.RegisterSlash("model", "List models or set the active model", func(args string) (any, error) {
		return app.handleModel(args)
	})

	// /resume
	app.server.RegisterSlash("resume", "List sessions or resume a session by ID/name", func(args string) (any, error) {
		return app.handleResume(args)
	})

	// /context — composite
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

	// /toggle
	app.server.RegisterSlash("toggle", "Toggle display settings (thinking, prefix, tools)", func(args string) (any, error) {
		return app.handleToggle(args)
	})

	// /show
	app.server.RegisterSlash("show", "Show agent settings or pipeline info", func(args string) (any, error) {
		subCmd := strings.TrimSpace(args)
		switch subCmd {
		case "settings", "":
			return app.handleShowSettings()
		case "pipeline":
			return map[string]any{"message": "pipeline info not yet available"}, nil
		default:
			return nil, fmt.Errorf("usage: /show settings|pipeline")
		}
	})

	// /quit
	app.server.RegisterSlash("quit", "Exit the application", func(args string) (any, error) {
		_ = args
		slog.Info("Received quit command, exiting application")
		os.Exit(0)
		return nil, nil
	})

	// /abort
	app.server.RegisterSlash("abort", "Abort the current agent execution", func(args string) (any, error) {
		return app.handleAbortSlash(args)
	})

	// /follow-up
	app.server.RegisterSlash("follow-up", "Add a follow-up message when agent is busy", func(args string) (any, error) {
		return app.handleFollowUpSlash(args)
	})
}