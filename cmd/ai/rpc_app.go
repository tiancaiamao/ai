package main

import (
	"context"
	"encoding/json"
	"fmt"
	agentctx "github.com/tiancaiamao/ai/pkg/context"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
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

	"github.com/tiancaiamao/ai/pkg/agentconfig"
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
	runID              string // run ID from parent ai serve (empty for standalone rpc)

	// --- Config ---
	cfg        *config.Config
	configPath string

	// --- Model ---
	model                llm.Model
	apiKey               string
	activeSpec           config.ModelSpec
	currentModelInfo     rpc.ModelInfo
	currentContextWindow int

	// --- Paths ---
	cwd      string
	agentDir string

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
	compactor       *compact.Compactor
	ctxManager      *compact.ContextManager
	compactorConfig *compact.Config

	// --- Tracing ---
	traceOutputPath string

	// --- Skills ---
	skillResult   *skill.LoadResult
	skillStats    *skill.SkillStatsFile
	skillCommands []rpc.SlashCommand

	// --- Agent ---
	ag               *agent.Agent
	agentCtx         *agentctx.AgentContext
	agentConfig      *agentconfig.AgentConfig
	loopCfg          *agent.LoopConfig
	executor         agent.ToolExecutor
	toolOutputConfig *config.ToolOutputConfig
	checkpointMgr    *agent.AgentContextCheckpointManager

	// --- Session I/O ---
	sessionWriter *sessionWriter
	sessionComp   *sessionCompactor

	// --- System prompt ---
	systemPrompt string

	// Agent context prefix combines skills + AGENTS.md into a single user message
	// injected before the first user message on each LLM call. Empty when neither
	// is available. Merged into one message for maximum prefix cache stability.
	agentContextPrefix string

	// --- RPC Server ---
	server *rpc.Server

	// --- Mutable state protected by stateMu ---
	stateMu                       sync.Mutex
	isStreaming                   bool
	isCompacting                  bool
	currentThinkingLevel          string
	autoCompactionEnabled         bool
	steeringMode                  string
	followUpMode                  string
	pendingSteer                  bool
	showThinking                  bool
	showTools                     bool
	showPrefix                    bool
	busyMode                      string
	consecutiveCompactionFailures int

	// --- Internal helper functions ---
	// These are assigned in initHelpers and used by handler closures.
	buildSystemPrompt               func(currentSess *session.Session) string
	buildAgentContextPrefix         func() string
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

// nuclearTruncate force-truncates oldest messages without LLM summary.
// This is the last-resort fallback when compaction repeatedly fails (e.g.,
// the summarization model cannot handle the conversation size).
func (app *rpcApp) nuclearTruncate() {
	if app.ag == nil {
		return
	}
	agentCtx := app.ag.GetContext()
	if agentCtx == nil {
		return
	}
	messages := agentCtx.RecentMessages
	if len(messages) <= 10 {
		return
	}

	// Keep only the last 20% of messages (minimum 10)
	keepCount := max(10, int(float64(len(messages))*0.2))
	truncatedCount := len(messages) - keepCount
	agentCtx.RecentMessages = messages[len(messages)-keepCount:]

	// Also sync to session
	if app.sess != nil {
		if err := app.sess.SaveMessages(agentCtx.RecentMessages); err != nil {
			slog.Error("[Compact] Nuclear truncation failed to save session", "error", err)
		}
	}

	// Reset failure counter after nuclear truncation
	app.stateMu.Lock()
	app.consecutiveCompactionFailures = 0
	app.stateMu.Unlock()

	slog.Warn("[Compact] Nuclear truncation completed",
		"messages_before", len(messages),
		"messages_after", keepCount,
		"truncated_count", truncatedCount)
}

// initHelpers creates the closures that need access to app fields.
// Must be called after all fields are populated.

func (app *rpcApp) initHelpers() {
	// buildSystemPrompt builds the full system prompt for the given session.
	app.buildSystemPrompt = func(currentSess *session.Session) string {
		// Agent config overrides the default system prompt.
		if app.agentConfig != nil {
			sp, err := app.agentConfig.ResolveSystemPrompt()
			if err != nil {
				slog.Error("Failed to resolve agent config system prompt", "error", err)
				// Fall through to default logic
			} else {
				slog.Info("Using agent config system prompt", "length", len(sp))
				return sp
			}
		}
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

	// buildAgentContextPrefix combines skills and AGENTS.md into a single
	// user message for prefix cache stability. Skills are wrapped in
	// <agent:skills> tags, instructions in <agent:instructions> tags.
	// Returns empty when neither is available.
	app.buildAgentContextPrefix = func() string {
		var parts []string

		promptBuilder := prompt.NewBuilderWithWorkspace("", app.ws)

		// Skills section
		promptBuilderForSkills := prompt.NewBuilderWithWorkspace("", app.ws)
		promptBuilderForSkills.SetSkills(app.skillResult.Skills).SetSkillStats(app.skillStats)
		if skills := promptBuilderForSkills.BuildSkillsMessage(); skills != "" {
			parts = append(parts, skills)
		}

		// Instructions section (AGENTS.md)
		if instructions := promptBuilder.BuildInstructionsMessage(); instructions != "" {
			parts = append(parts, instructions)
		}

		if len(parts) == 0 {
			return ""
		}
		return strings.Join(parts, "\n\n")
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
		app.agentContextPrefix = app.buildAgentContextPrefix()
		// Keep loopCfg in sync if it has been constructed (createBaseContext
		// may be re-invoked on session resume while loopCfg already exists).
		if app.loopCfg != nil {
			app.loopCfg.AgentContextPrefix = app.agentContextPrefix
		}
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
				// Resume path: load the latest checkpoint and replay any
				// journal entries written AFTER the checkpoint. This is the
				// single source of truth — see pkg/agent/resume.go.
				msgs, llmCtx, agentState, err := agent.LoadResumeState(sessionDir, ctx.RecentMessages)
				if err != nil {
					slog.Warn("Resume state load failed, using session messages",
						"error", err,
						"session_messages", len(ctx.RecentMessages))
				} else {
					if len(msgs) != len(ctx.RecentMessages) {
						slog.Info("Resumed messages from checkpoint + journal replay",
							"resumed_messages", len(msgs),
							"session_messages", len(ctx.RecentMessages),
							"replayed", len(msgs)-len(ctx.RecentMessages))
					}
					ctx.RecentMessages = msgs
					if llmCtx != "" {
						ctx.LLMContext = llmCtx
					}
					if agentState != nil {
						ctx.AgentState = agentState
						if agentState.CurrentWorkingDir != "" {
							if err := app.ws.SetCWD(agentState.CurrentWorkingDir); err != nil {
								slog.Warn("Failed to restore CWD from checkpoint",
									"cwd", agentState.CurrentWorkingDir, "error", err)
							}
						}
						slog.Info("Restored agent state from checkpoint",
							"turns", agentState.TotalTurns,
							"tokens", agentState.TokensUsed,
							"toolCallsSince", agentState.ToolCallsSinceLastTrigger,
							"cwd", agentState.CurrentWorkingDir,
						)
					}
				}

				// Legacy fallback: if LoadResumeState returned no AgentState
				// (e.g. a very old checkpoint with only agent_state.json and
				// no RecentMessages), try loading agent_state.json directly.
				if ctx.AgentState == nil {
					if cpInfo, err := agentctx.LoadLatestCheckpoint(sessionDir); err == nil && cpInfo != nil {
						cpPath := filepath.Join(sessionDir, cpInfo.Path)
						if savedState, err := agentctx.LoadCheckpointAgentState(cpPath); err == nil {
							ctx.AgentState = savedState
							if savedState.CurrentWorkingDir != "" {
								if err := app.ws.SetCWD(savedState.CurrentWorkingDir); err != nil {
									slog.Warn("Failed to restore CWD from checkpoint",
										"cwd", savedState.CurrentWorkingDir, "error", err)
								}
							}
							slog.Info("Restored agent state from checkpoint (legacy, no messages)",
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

		if !app.compactor.ShouldCompact(context.Background(), app.ag.GetContext()) {
			return
		}
		if !app.sess.CanCompact(app.compactor) {
			messages := app.ag.GetMessages()
			slog.Info("Pre-request compaction skipped: session not compactable",
				"trigger", trigger,
				"messages", len(messages),
				"estimatedTokens", app.compactor.EstimateTokens(messages))
			return
		}

		beforeCount := len(app.ag.GetMessages())
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

			// Nuclear fallback: after consecutive compaction failures, force-truncate
			// oldest messages without LLM summary. This prevents permanent session death
			// when the summarization model itself cannot handle the conversation size.
			const maxConsecutiveFailures = 3
			app.stateMu.Lock()
			app.consecutiveCompactionFailures++
			failures := app.consecutiveCompactionFailures
			app.stateMu.Unlock()

			if failures >= maxConsecutiveFailures {
				slog.Warn("[Compact] Nuclear fallback: force-truncating oldest messages after consecutive compaction failures",
					"failures", failures,
					"trigger", trigger)
				app.nuclearTruncate()
			}
		} else {
			app.stateMu.Lock()
			app.consecutiveCompactionFailures = 0
			app.stateMu.Unlock()
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

func (app *rpcApp) registerHiddenAlias(alias, desc, canonical string) {
	app.server.RegisterHiddenSlash(alias, desc, func(args string) (any, error) {
		h, ok := app.server.GetSlashHandler(canonical)
		if !ok {
			return nil, fmt.Errorf("unknown command: /%s", canonical)
		}
		return h(args)
	})
}

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

func (app *rpcApp) handleAbort(cmd rpc.RPCCommand) (any, error) {
	_ = cmd
	slog.Info("Received abort")
	app.ag.Abort()
	return nil, nil
}

// registerHandlers registers all RPC command handlers and slash commands.
// Handler methods are distributed across topic-specific files; this method
// wires up protocol commands and delegates slash command registration.
func (app *rpcApp) registerHandlers(
	validToolSummaryStrategies, validToolSummaryAutomations, validSteeringModes, validFollowUpModes, validThinkingLevels map[string]bool,
) {
	// === Protocol command handlers ===
	app.server.Register(rpc.CommandPrompt, app.handlePrompt)
	app.server.Register(rpc.CommandSteer, app.handleSteer)
	app.server.Register(rpc.CommandFollowUp, app.handleFollowUp)
	app.server.Register(rpc.CommandAbort, app.handleAbort)

	// === Slash command handlers (topic-specific registration) ===
	app.registerSessionHandlers()
	app.registerMessageHandlers()
	app.registerConfigHandlers(validToolSummaryStrategies, validToolSummaryAutomations, validSteeringModes, validFollowUpModes, validThinkingLevels)
	app.registerHelpHandlers()
}
